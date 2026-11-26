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
  window.sessionStorage.clear();
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

describe('useThreeViews after a reload during a start (#636)', () => {
  const poll = schema.chat.frontend.fallback_poll_milliseconds;
  // The raw catalog has the queue limit under chat.three_view; the public projection the page reads carries it here.
  const limits = schema.chat.three_view_limits;
  const queueLimitSeconds = (defaultCatalog.application.chat as unknown as { three_view: { queue_limit_seconds: number } }).three_view.queue_limit_seconds;
  beforeEach(() => {
    schema.chat.frontend.fallback_poll_milliseconds = 20;
    schema.chat.three_view_limits = { queue_limit_seconds: queueLimitSeconds };
  });
  afterEach(() => {
    schema.chat.frontend.fallback_poll_milliseconds = poll;
    schema.chat.three_view_limits = limits;
  });

  const running = { ...group, slots: [group.slots[0], { key: 'left', label: '左侧', status: 'running', attempt: 1, task_id: 'tsk_left' }] };

  /** The three-views route answers with `group` (left not started) until `started()`, then with left running once and settled after. */
  function stubServerTakesStart() {
    let items: unknown[] = [group];
    let refuse = false;
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (!String(input).includes('/three-views')) return new Response('{}', { status: 404 });
      if (init?.method === 'POST' && refuse) return new Response(JSON.stringify({ title: 'refused' }), { status: 409 });
      if (init?.method === 'POST') throw new TypeError('Failed to fetch');
      return new Response(JSON.stringify({ items }), { status: 200 });
    });
    vi.stubGlobal('fetch', fetcher);
    const reads = () => fetcher.mock.calls.filter(([input, init]) => String(input).includes('/three-views') && init?.method !== 'POST').length;
    return { refuse: () => { refuse = true; }, started: () => { items = [running]; }, settled: () => { items = [{ ...running, slots: [group.slots[0], { ...running.slots[1], status: 'succeeded' }] }]; }, reads };
  }

  const remembered = (at: number) => JSON.stringify([{ sessionId: 's1', runId: 'run_1', slotKey: 'left', action: 'start', attempt: 0, at }]);

  it('keeps reading a view this tab started before the reload until the server takes it', async () => {
    window.sessionStorage.setItem('plinth:three-view-pending', remembered(Date.now()));
    const route = stubServerTakesStart();
    const { result } = renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(route.reads()).toBeGreaterThanOrEqual(3));
    expect(result.current.groups[0].slots[1].status).toBe('not_started');
    route.started();
    await waitFor(() => expect(result.current.groups[0].slots[1].status).toBe('running'));
    expect(window.sessionStorage.getItem('plinth:three-view-pending')).toBeNull();
    route.settled();
    await waitFor(() => expect(result.current.groups[0].slots[1].status).toBe('succeeded'));
    const settled = route.reads();
    await new Promise((resolve) => { setTimeout(resolve, 100); });
    expect(route.reads()).toBe(settled);
  });

  it('does not read again for a start older than the page keeps trying one', async () => {
    const limit = queueLimitSeconds * 1000;
    window.sessionStorage.setItem('plinth:three-view-pending', remembered(Date.now() - limit - 1));
    const route = stubServerTakesStart();
    renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(route.reads()).toBe(1));
    await new Promise((resolve) => { setTimeout(resolve, 100); });
    expect(route.reads()).toBe(1);
    expect(window.sessionStorage.getItem('plinth:three-view-pending')).toBeNull();
  });

  it('remembers a start whose connection was lost, and forgets one the server answered', async () => {
    const route = stubServerTakesStart();
    const { result } = renderHook(() => useThreeViews(), { wrapper });
    await waitFor(() => expect(result.current.groups).toHaveLength(1));
    const left = result.current.groups[0].slots[1];
    await act(async () => { await result.current.advance(result.current.groups[0], left); });
    expect(JSON.parse(window.sessionStorage.getItem('plinth:three-view-pending') ?? '[]')).toMatchObject([{ runId: 'run_1', slotKey: 'left', action: 'start', attempt: 0 }]);
    const lost = route.reads();
    await waitFor(() => expect(route.reads()).toBeGreaterThan(lost + 1));

    window.sessionStorage.clear();
    route.refuse();
    let message: string | null = null;
    await act(async () => { message = await result.current.advance(result.current.groups[0], left); });
    expect(message).toBeTruthy();
    expect(window.sessionStorage.getItem('plinth:three-view-pending')).toBeNull();
  });
});
