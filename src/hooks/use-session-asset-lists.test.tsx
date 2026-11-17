// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { AssetListSummary } from '@/lib/asset-lists/client';
import type { ProductSchema } from '@/lib/product-schema';
import { useSessionAssetLists } from './use-session-asset-lists';

const schema = defaultCatalog.application as unknown as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

const summary: AssetListSummary = {
  id: 'al_1',
  project_id: 'prj_1',
  source_session_id: 's1',
  title: 'Hero set',
  status: 'draft',
  effective_status: 'draft',
  item_count: 2,
  planned_step_count: 3,
  planned_model3d_step_count: 2,
  expires_at: '2026-11-25T00:00:00Z',
  created_at: '2026-11-18T00:00:00Z',
  updated_at: '2026-11-18T00:00:00Z',
};

function wrapper({ children }: { children: React.ReactNode }) {
  return <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>{children}</SWRConfig>;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('useSessionAssetLists', () => {
  it('reads the session\'s lists from the public route', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ data: [summary] }), { status: 200 })) as unknown as typeof fetch;
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useSessionAssetLists('s 1'), { wrapper });
    await waitFor(() => expect(result.current.lists).toEqual([summary]));
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeUndefined();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/chat/sessions/s%201/asset-lists');
  });

  it('sends no request without a session', () => {
    const fetcher = vi.fn() as unknown as typeof fetch;
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useSessionAssetLists(null), { wrapper });
    expect(result.current.lists).toEqual([]);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeUndefined();
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('publishes the server refusal and keeps the list empty', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({
      code: 'asset_list_expired', detail: '清单已过期',
    }), { status: 409 })) as unknown as typeof fetch;
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useSessionAssetLists('s1'), { wrapper });
    await waitFor(() => expect(result.current.error).toMatchObject({
      status: 409, code: 'asset_list_expired', detail: '清单已过期',
    }));
    expect(result.current.lists).toEqual([]);
    expect(result.current.loading).toBe(false);
  });

  it('reads again on refresh', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ data: [summary] }), { status: 200 })) as unknown as typeof fetch;
    vi.stubGlobal('fetch', fetcher);
    const { result } = renderHook(() => useSessionAssetLists('s1'), { wrapper });
    await waitFor(() => expect(result.current.lists).toEqual([summary]));
    await act(async () => { await result.current.refresh(); });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});