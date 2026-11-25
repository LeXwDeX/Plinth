// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useThreeViews } from './use-three-views';

const schema = defaultCatalog.application as unknown as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

const group = {
  run_id: 'run_1', label: '三视图', source_asset_id: 'ast_src', created_at: '2026-11-26T00:00:00Z', complete: false,
  slots: [
    { key: 'front', label: '正面', status: 'succeeded', task_id: 'tsk_front', output_asset_id: 'ast_front' },
    { key: 'left', label: '左侧', status: 'not_started', startable: true },
  ],
  model_source: { slot: 'front', available: true, task_id: 'tsk_front', asset_id: 'ast_front' },
};

function wrapper({ children }: { children: React.ReactNode }) {
  return <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>{children}</SWRConfig>;
}

/** The three-views route answers with no group until `ready()` is called, then with one. */
function stubThreeViews() {
  let items: unknown[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    if (!String(input).includes('/three-views')) return new Response('{}', { status: 404 });
    return new Response(JSON.stringify({ items }), { status: 200 });
  });
  vi.stubGlobal('fetch', fetcher);
  const reads = () => fetcher.mock.calls.filter(([input]) => String(input).includes('/three-views')).length;
  return { ready: () => { items = [group]; }, reads };
}

// The refresh gate is shared by the module; each test starts from a revision of its own so a change one test
// claimed is not taken for the next test's.
let testRun = 0;

beforeEach(() => {
  testRun += 1;
  useStudioStore.setState({ versionsRevision: testRun * 1000 });
  useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {}, messagesBySession: {} });
  useWorkspaceStore.setState({ trackedTasks: [] });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('useThreeViews (#618)', () => {
  it('reads the groups again when the turn that made one ends', async () => {
    const route = stubThreeViews();
    const { result } = renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(route.reads()).toBe(1));
    act(() => { useChatStore.setState({ streamingBuffers: { s1: {} as never } }); });
    route.ready();
    act(() => { useChatStore.setState({ streamingBuffers: {} }); });
    await waitFor(() => expect(result.current.groups.map((item) => item.runId)).toEqual(['run_1']));
  });

  it('reads the groups again when a task of the session is registered and when it ends', async () => {
    const route = stubThreeViews();
    const { result } = renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(route.reads()).toBe(1));
    route.ready();
    act(() => { useWorkspaceStore.getState().registerTask({ taskId: 'tsk_front', sessionId: 's1', capability: 'image-to-image', progressMaximum: 100 }); });
    await waitFor(() => expect(result.current.groups).toHaveLength(1));
    const before = route.reads();
    act(() => { useWorkspaceStore.getState().applyTaskLive('tsk_front', { state: 'succeeded', terminal: true }); });
    await waitFor(() => expect(route.reads()).toBe(before + 1));
  });

  it('reads the groups again when the studio is told the versions changed (an approval)', async () => {
    const route = stubThreeViews();
    const { result } = renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(route.reads()).toBe(1));
    route.ready();
    act(() => { useStudioStore.getState().bumpVersions(); });
    await waitFor(() => expect(result.current.groups).toHaveLength(1));
  });

  it('asks once for every consumer that sees the same change', async () => {
    const route = stubThreeViews();
    renderHook(() => [useThreeViews(), useThreeViews(), useThreeViews()], { wrapper });
    await waitFor(() => expect(route.reads()).toBe(1));
    act(() => { useStudioStore.getState().bumpVersions(); });
    await waitFor(() => expect(route.reads()).toBe(2));
    await new Promise((resolve) => { setTimeout(resolve, 50); });
    expect(route.reads()).toBe(2);
  });

  it('does not read again while nothing changes', async () => {
    const route = stubThreeViews();
    const { rerender } = renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(route.reads()).toBe(1));
    rerender();
    await new Promise((resolve) => { setTimeout(resolve, 50); });
    expect(route.reads()).toBe(1);
  });
});
