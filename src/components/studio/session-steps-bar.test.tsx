// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { SessionStepsBar } from '@/components/studio/session-steps-bar';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

const base = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
let currentSchema: ProductSchema = base;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: currentSchema,
    text: (key: string) => currentSchema.content.messages[key] ?? '',
    refreshing: false,
  }),
}));

const text = (key: string) => schemaText(currentSchema, key) || messages[key];

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function renderBar() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>
      <SessionStepsBar sessionID="ses_mcp" name="MCP · 木桶" />
    </SWRConfig>,
  );
}

/** Answers the standing read and records the write. */
function serveSteps(read: unknown, write: unknown) {
  const calls: Array<{ method: string; url: string; body: unknown }> = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : null });
    if (method === 'PUT') return json(write);
    return json(read);
  });
  vi.stubGlobal('fetch', fetchMock);
  return calls;
}

beforeEach(() => {
  currentSchema = base;
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('SessionStepsBar (#330 §6)', () => {
  it('shows a capped MCP asset and grants more steps through the session route', async () => {
    const calls = serveSteps(
      { mode: 'cap', used: 1, cap: 1, extra: 0 },
      { mode: 'cap', used: 1, cap: 1, extra: 5 },
    );
    renderBar();

    const bar = await screen.findByTestId('session-steps-bar');
    expect(bar.dataset.mode).toBe('cap');
    expect(bar.dataset.exhausted).toBe('true');
    expect(screen.getByTestId('session-steps-standing').textContent).toBe(
      formatSchemaText(text('ui.step_policy.session_steps'), { used: 1, limit: 1 }),
    );
    expect(calls[0]).toMatchObject({ method: 'GET', url: '/api/v1/chat/sessions/ses_mcp/steps' });

    fireEvent.click(screen.getByTestId('session-steps-extend'));
    const dialog = await screen.findByTestId('session-steps-extend-dialog');
    expect(dialog.dataset.session).toBe('ses_mcp');
    fireEvent.change(screen.getByTestId('asset-steps-extend-amount'), { target: { value: '5' } });
    fireEvent.click(screen.getByTestId('asset-steps-extend-confirm'));

    await waitFor(() => {
      const put = calls.find((call) => call.method === 'PUT');
      expect(put?.url).toBe('/api/v1/chat/sessions/ses_mcp/steps');
      expect(put?.body).toEqual({ extra_steps: 5 });
    });
    await waitFor(() => {
      expect(screen.getByTestId('session-steps-bar').dataset.limit).toBe('6');
    });
    // The grant is no longer used up, so the entry disappears.
    expect(screen.queryByTestId('session-steps-extend')).toBeNull();
  });

  it('stays hidden for a non-steps session (404) and for the other modes', async () => {
    serveSteps({ mode: 'cap', used: 1, cap: 1, extra: 0 }, {});
    vi.stubGlobal('fetch', vi.fn(async () => json({ code: 'not_found' }, 404)));
    renderBar();
    await waitFor(() => expect(screen.queryByTestId('session-steps-bar')).toBeNull());

    cleanup();
    serveSteps({ mode: 'agent', used: 3 }, {});
    renderBar();
    // The read finished but the mode is not capped: nothing to extend.
    await waitFor(() => expect(screen.queryByTestId('session-steps-bar')).toBeNull());
  });

  it('makes no request when the deployment declares no session route', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    currentSchema = {
      ...base,
      runtime: {
        ...base.runtime,
        http: {
          ...base.runtime.http,
          routes: { ...base.runtime.http.routes, chat_session_steps: undefined },
        },
      },
    } as ProductSchema;
    renderBar();
    await waitFor(() => expect(screen.queryByTestId('session-steps-bar')).toBeNull());
    expect(fetchMock).not.toHaveBeenCalled();
  });
});