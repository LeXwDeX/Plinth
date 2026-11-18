// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { AccountStepPolicy } from '@/hooks/use-account-step-policy';
import { useAccountStepPolicy } from '@/hooks/use-account-step-policy';
import type { ProductSchema } from '@/lib/product-schema';

const base = defaultCatalog.application as unknown as ProductSchema;
let currentSchema: ProductSchema = base;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: currentSchema }),
}));

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

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function wrapper({ children }: { children: React.ReactNode }) {
  return (
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>
      {children}
    </SWRConfig>
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  currentSchema = base;
});

describe('useAccountStepPolicy', () => {
  it('reads the person\'s setting from the public route', async () => {
    const fetcher = vi.fn(async () => json(policy));
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useAccountStepPolicy(), { wrapper });

    await waitFor(() => expect(result.current.policy).toEqual(policy));
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeUndefined();
    expect(fetcher).toHaveBeenCalledWith(route);
  });

  it('sends every member the server requires, merged from the current read', async () => {
    const saved: AccountStepPolicy = { ...policy, version: 4, list: { mode: 'cap', cap: 30 } };
    let current = policy;
    const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        current = saved;
        return json(saved);
      }
      return json(current);
    });
    vi.stubGlobal('fetch', fetcher);

    const { result } = renderHook(() => useAccountStepPolicy(), { wrapper });
    await waitFor(() => expect(result.current.policy).toEqual(policy));

    let outcome: Awaited<ReturnType<typeof result.current.update>> | undefined;
    await act(async () => {
      outcome = await result.current.update({ expected_version: 3, list: { mode: 'cap', cap: 30 } });
    });

    expect(outcome).toMatchObject({ status: 'saved', policy: saved });
    const put = fetcher.mock.calls.find(([, init]) => init?.method === 'PUT');
    expect(put?.[0]).toBe(route);
    expect(JSON.parse(String(put?.[1]?.body))).toEqual({
      expected_version: 3,
      list: { mode: 'cap', cap: 30 },
      // The entry the caller did not name is carried over from the read.
      mcp: { mode: 'cap', cap: 12 },
    });
    // A save reads again, so the panel shows what the server accepted.
    await waitFor(() => expect(result.current.policy).toEqual(saved));
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'PUT')).toHaveLength(2);
  });

  it('re-reads on a conflict and reports the values now in force', async () => {
    const elsewhere: AccountStepPolicy = { ...policy, version: 9, list: { mode: 'unlimited', cap: 20 } };
    let current = policy;
    const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') return json({ code: 'conflict', detail: 'conflict' }, 409);
      return json(current);
    });
    vi.stubGlobal('fetch', fetcher);

    const { result } = renderHook(() => useAccountStepPolicy(), { wrapper });
    await waitFor(() => expect(result.current.policy).toEqual(policy));

    let outcome: Awaited<ReturnType<typeof result.current.update>> | undefined;
    await act(async () => {
      current = elsewhere;
      outcome = await result.current.update({ expected_version: 3, list: { mode: 'cap', cap: 30 } });
    });

    expect(outcome).toEqual({ status: 'conflict', policy: elsewhere });
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'PUT')).toHaveLength(2);
    await waitFor(() => expect(result.current.policy).toEqual(elsewhere));
  });

  it('reports a refused save with the server\'s own message', async () => {
    const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') return json({ code: 'forbidden', detail: 'A browser session is required' }, 403);
      return json(policy);
    });
    vi.stubGlobal('fetch', fetcher);

    const { result } = renderHook(() => useAccountStepPolicy(), { wrapper });
    await waitFor(() => expect(result.current.policy).toEqual(policy));

    let outcome: Awaited<ReturnType<typeof result.current.update>> | undefined;
    await act(async () => {
      outcome = await result.current.update({ expected_version: 3 });
    });

    expect(outcome?.status).toBe('failed');
    if (outcome?.status === 'failed') {
      expect(outcome.error.status).toBe(403);
      expect(outcome.error.message).toBe('A browser session is required');
    }
    // A refused save does not read again; the draft stays as the person left it.
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'PUT')).toHaveLength(1);
  });

  it('makes no request and refuses a save without the route', async () => {
    currentSchema = {
      ...base,
      runtime: {
        ...base.runtime,
        http: {
          ...base.runtime.http,
          routes: { ...base.runtime.http.routes, account_step_policy: undefined },
        },
      },
    } as ProductSchema;
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useAccountStepPolicy(), { wrapper });

    expect(result.current.policy).toBeNull();
    expect(result.current.loading).toBe(false);
    expect(fetcher).not.toHaveBeenCalled();

    let outcome: Awaited<ReturnType<typeof result.current.update>> | undefined;
    await act(async () => {
      outcome = await result.current.update({ expected_version: 0 });
    });
    expect(outcome?.status).toBe('failed');
    expect(fetcher).not.toHaveBeenCalled();
  });
});