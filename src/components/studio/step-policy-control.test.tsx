// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { StepPolicyControl } from '@/components/studio/step-policy-control';
import type { AccountStepPolicy } from '@/hooks/use-account-step-policy';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';

const base = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const english = defaultCatalog.application.localization.bundles.en.messages as Record<string, string>;
let currentSchema: ProductSchema = base;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: currentSchema,
    text: (key: string) => currentSchema.content.messages[key] ?? '',
    refreshing: false,
  }),
}));

const auth = vi.hoisted(() => ({
  user: { id: 'u1', name: 'Person', email: 'person@example.com' } as { id: string; name: string; email: string } | null,
  isLoading: false,
}));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => auth }));

const route = '/api/v1/account/step-policy';

const policy: AccountStepPolicy = {
  version: 3,
  list: { mode: 'agent', cap: 20 },
  mcp: { mode: 'cap', cap: 12 },
  modes: [
    { value: 'agent', label: 'Agent 自己管理' },
    { value: 'unlimited', label: '不限制' },
    { value: 'cap', label: '按步数上限' },
  ],
  cap_range: { minimum: 1, maximum: 200 },
};

const text = (key: string) => messages[key];
const modeLabel = (mode: 'agent' | 'unlimited' | 'cap') => text(`ui.step_policy.mode.${mode}`);

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function renderControl() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>
      <StepPolicyControl />
    </SWRConfig>,
  );
}

function stub(fetcher: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('fetch', fetcher);
  return fetcher;
}

beforeEach(() => {
  auth.user = { id: 'u1', name: 'Person', email: 'person@example.com' };
  auth.isLoading = false;
  currentSchema = base;
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('the 3D step mode control (#330)', () => {
  it('stays hidden without a signed-in person or without the Schema section', async () => {
    stub(vi.fn(async () => json(policy)));
    auth.user = null;
    expect(renderControl().container.innerHTML).toBe('');
    cleanup();

    auth.user = { id: 'u1', name: 'Person', email: 'person@example.com' };
    currentSchema = { ...base, step_policy: undefined } as ProductSchema;
    expect(renderControl().container.innerHTML).toBe('');
  });

  it('shows the asset lists mode on the badge and both entries in the panel', async () => {
    stub(vi.fn(async () => json(policy)));
    const view = renderControl();
    const badge = await view.findByTestId('studio-step-policy-badge');
    await waitFor(() => expect(badge.textContent).toBe(
      formatSchemaText(text('ui.step_policy.badge'), { mode: modeLabel('agent') }),
    ));

    fireEvent.click(badge);
    const panel = await view.findByTestId('studio-step-policy-panel');
    await view.findByTestId('step-policy-list-mode-agent');
    expect(within(panel).getByText(text('ui.step_policy.title'))).toBeTruthy();
    expect(within(panel).getByText(text('ui.step_policy.list'))).toBeTruthy();
    expect(within(panel).getByText(text('ui.step_policy.mcp'))).toBeTruthy();
    expect(view.getByTestId('step-policy-list-mode-agent').getAttribute('aria-checked')).toBe('true');
    expect(view.getByTestId('step-policy-mcp-mode-cap').getAttribute('aria-checked')).toBe('true');
    expect(view.getByTestId('step-policy-list-mode-agent').textContent).toBe(modeLabel('agent'));
    expect(view.getByTestId('step-policy-cap-hint').textContent).toBe(
      formatSchemaText(text('ui.step_policy.cap_hint'), { minimum: 1, maximum: 200 }),
    );
    expect(view.queryByTestId('step-policy-list-cap')).toBeNull();
    expect(view.getByTestId('step-policy-mcp-cap').getAttribute('value')).toBe('12');
  });

  it('shows the cap input with the server range only for the cap mode', async () => {
    stub(vi.fn(async () => json(policy)));
    const view = renderControl();
    fireEvent.click(await view.findByTestId('studio-step-policy-badge'));
    await view.findByTestId('step-policy-list-mode-agent');

    fireEvent.click(view.getByTestId('step-policy-list-mode-cap'));
    const input = view.getByTestId('step-policy-list-cap');
    expect(input.getAttribute('min')).toBe('1');
    expect(input.getAttribute('max')).toBe('200');
    expect((input as HTMLInputElement).value).toBe('20');

    fireEvent.click(view.getByTestId('step-policy-list-mode-unlimited'));
    expect(view.queryByTestId('step-policy-list-cap')).toBeNull();
  });

  it('saves both entries with the version it read and reads again', async () => {
    let server = policy;
    const fetcher = stub(vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as {
          expected_version: number;
          list: { mode: string; cap: number };
          mcp: { mode: string; cap: number };
        };
        server = {
          ...server,
          version: body.expected_version + 1,
          list: body.list,
          mcp: body.mcp,
        };
        return json(server);
      }
      return json(server);
    }));
    const view = renderControl();
    fireEvent.click(await view.findByTestId('studio-step-policy-badge'));
    await view.findByTestId('step-policy-list-mode-agent');

    fireEvent.click(view.getByTestId('step-policy-list-mode-cap'));
    fireEvent.change(view.getByTestId('step-policy-list-cap'), { target: { value: '30' } });
    fireEvent.click(view.getByRole('button', { name: text('ui.step_policy.save') }));

    await waitFor(() => {
      expect(fetcher.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(true);
    });
    const put = fetcher.mock.calls.find(([, init]) => init?.method === 'PUT');
    expect(put?.[0]).toBe(route);
    expect(JSON.parse(String(put?.[1]?.body))).toEqual({
      expected_version: 3,
      list: { mode: 'cap', cap: 30 },
      mcp: { mode: 'cap', cap: 12 },
    });
    // Saving re-reads the setting; the panel closes and the badge follows it.
    await waitFor(() => expect(server.version).toBe(4));
    await waitFor(() => expect(view.getByTestId('studio-step-policy-badge').textContent).toBe(
      formatSchemaText(text('ui.step_policy.badge'), { mode: modeLabel('cap') }),
    ));
    expect(view.queryByTestId('studio-step-policy-panel')).toBeNull();
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'PUT')).toHaveLength(2);
  });

  it('reports a conflict, reads the values now in force and never overwrites them', async () => {
    let server = policy;
    const elsewhere: AccountStepPolicy = { ...policy, version: 9, list: { mode: 'unlimited', cap: 20 } };
    const fetcher = stub(vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        server = elsewhere;
        return json({ code: 'conflict', detail: 'conflict' }, 409);
      }
      return json(server);
    }));
    const view = renderControl();
    fireEvent.click(await view.findByTestId('studio-step-policy-badge'));
    await view.findByTestId('step-policy-list-mode-agent');

    fireEvent.click(view.getByTestId('step-policy-list-mode-cap'));
    fireEvent.change(view.getByTestId('step-policy-list-cap'), { target: { value: '30' } });
    fireEvent.click(view.getByRole('button', { name: text('ui.step_policy.save') }));

    const notice = await view.findByTestId('step-policy-notice');
    expect(notice.textContent).toBe(text('ui.step_policy.conflict'));
    // The answer the person sees is the other session's, not the draft sent.
    await waitFor(() => expect(view.getByTestId('step-policy-list-mode-unlimited').getAttribute('aria-checked')).toBe('true'));
    expect(view.queryByTestId('studio-step-policy-panel')).not.toBeNull();
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'PUT')).toHaveLength(2);
  });

  it('shows the server\'s refusal as readable copy and offers a retry', async () => {
    stub(vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        return json({ code: 'forbidden', detail: 'A browser session is required' }, 403);
      }
      return json(policy);
    }));
    const view = renderControl();
    fireEvent.click(await view.findByTestId('studio-step-policy-badge'));
    await view.findByTestId('step-policy-list-mode-agent');

    fireEvent.click(view.getByRole('button', { name: text('ui.step_policy.save') }));
    const notice = await view.findByTestId('step-policy-notice');
    expect(notice.textContent).toBe(
      formatSchemaText(text('ui.step_policy.error'), { reason: 'A browser session is required' }),
    );
    expect(view.getByRole('button', { name: text('ui.step_policy.retry') })).toBeTruthy();
  });

  // #670: a failure without the server's own words never shows `HTTP 500` or another internal reason.
  it('words a save failure without a server reason generically', async () => {
    stub(vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') return new Response('oops', { status: 500 });
      return json(policy);
    }));
    const view = renderControl();
    fireEvent.click(await view.findByTestId('studio-step-policy-badge'));
    await view.findByTestId('step-policy-list-mode-agent');

    fireEvent.click(view.getByRole('button', { name: text('ui.step_policy.save') }));
    const notice = await view.findByTestId('step-policy-notice');
    expect(notice.textContent).toBe(text('ui.step_policy.error_generic'));
    expect(notice.textContent).not.toContain('HTTP');
  });

  it('words a load failure without a server reason generically', async () => {
    stub(vi.fn(async () => json({ unexpected: true })));
    const view = renderControl();
    fireEvent.click(await view.findByTestId('studio-step-policy-badge'));
    await waitFor(() => expect(view.getByText(text('ui.step_policy.load_failed_generic'))).toBeTruthy());
    expect(view.queryByText(/invalid step policy response/)).toBeNull();
  });

  it('has both locales\' copy for every key the control reads', () => {
    const keys = [
      'ui.step_policy.title', 'ui.step_policy.badge', 'ui.step_policy.list', 'ui.step_policy.mcp',
      'ui.step_policy.mode.agent', 'ui.step_policy.mode.unlimited', 'ui.step_policy.mode.cap',
      'ui.step_policy.cap', 'ui.step_policy.cap_hint', 'ui.step_policy.cap_invalid',
      'ui.step_policy.save', 'ui.step_policy.saving', 'ui.step_policy.retry',
      'ui.step_policy.conflict', 'ui.step_policy.error', 'ui.step_policy.load_failed', 'ui.step_policy.loading',
      'ui.step_policy.error_generic', 'ui.step_policy.load_failed_generic',
    ];
    for (const key of keys) {
      expect(messages[key], key).toBeTruthy();
      expect(english[key], key).toBeTruthy();
    }
  });
});