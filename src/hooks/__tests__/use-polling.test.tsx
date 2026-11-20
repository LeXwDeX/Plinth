// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import useSWR, { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { watchConnectivity } from '@/lib/connectivity-watch';
import { swrPollingConfig, type IdlePolling } from '@/lib/polling';
import { useChatStore } from '@/store/chat-store';
import { useConnectivityStore } from '@/store/connectivity-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useIdleInterval, useSessionBusy } from '../use-polling';

const idle: IdlePolling = { backoff_factor: 2, max_interval_milliseconds: 800 };

let visibility: DocumentVisibilityState = 'visible';
beforeEach(() => {
  visibility = 'visible';
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
  useConnectivityStore.setState({ online: true, sessionExpired: false, reconnectTick: 0 });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function show(next: DocumentVisibilityState) {
  visibility = next;
  document.dispatchEvent(new Event('visibilitychange'));
}

// The page runs every SWR read under this configuration (web/src/main.tsx), with a fresh cache per test.
const wrapper = ({ children }: { children: React.ReactNode }) => (
  <SWRConfig value={{ ...swrPollingConfig, provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>
    {children}
  </SWRConfig>
);

/** An SWR read that polls the way the page's reads do; `reads` are the moments the server was asked. */
function probe(options: { answer?: () => unknown; busy?: boolean } = {}) {
  const reads: number[] = [];
  const started = Date.now();
  const fetcher = vi.fn(async () => {
    reads.push(Date.now() - started);
    return options.answer ? options.answer() : { same: true };
  });
  const view = renderHook(({ busy }: { busy: boolean }) => {
    const refreshInterval = useIdleInterval<unknown>(100, idle, { busy });
    return useSWR('/api/v1/probe', fetcher, { refreshInterval });
  }, { wrapper, initialProps: { busy: options.busy ?? false } });
  return { ...view, reads, fetcher };
}

describe('SWR reads under the page configuration', () => {
  it('a hidden tab sends no polling request, and the tab shown again reads once', async () => {
    vi.useFakeTimers();
    const { reads, result } = probe();
    await act(async () => { await vi.advanceTimersByTimeAsync(700); });
    const before = reads.length;
    expect(before).toBeGreaterThan(2);

    await act(async () => { show('hidden'); });
    await act(async () => { await vi.advanceTimersByTimeAsync(3_600_000); });
    expect(reads).toHaveLength(before);

    await act(async () => { show('visible'); await vi.advanceTimersByTimeAsync(0); });
    expect(reads).toHaveLength(before + 1);
    expect(result.current.data).toEqual({ same: true });
  });

  it('after a 401 no read goes out any more, and the answer already on the page stays', async () => {
    vi.useFakeTimers();
    const { reads, result } = probe();
    await act(async () => { await vi.advanceTimersByTimeAsync(700); });
    const before = reads.length;

    await act(async () => { useConnectivityStore.getState().markSessionExpired(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(3_600_000); });
    expect(reads).toHaveLength(before);
    // Showing the tab again does not read either: the login is over.
    await act(async () => { show('hidden'); show('visible'); await vi.advanceTimersByTimeAsync(60_000); });
    expect(reads).toHaveLength(before);
    expect(result.current.data).toEqual({ same: true });
  });

  it('a 401 from the page\'s own API, seen through the connectivity watch, stops every read', async () => {
    vi.useFakeTimers();
    let status = 200;
    const calls: string[] = [];
    const original = window.fetch;
    window.fetch = vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input));
      return new Response(JSON.stringify({ ok: true }), { status });
    }) as typeof window.fetch;
    const unwatch = watchConnectivity('/api/auth/login');
    try {
      const view = renderHook(() => {
        const refreshInterval = useIdleInterval<unknown>(100, idle);
        return useSWR('/api/v1/agent/consent', (url: string) => window.fetch(url).then((response) => {
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          return response.json();
        }), { refreshInterval });
      }, { wrapper });
      await act(async () => { await vi.advanceTimersByTimeAsync(700); });
      const healthy = calls.length;
      expect(healthy).toBeGreaterThan(1);
      expect(useConnectivityStore.getState().sessionExpired).toBe(false);

      // The login ends: the next read comes back 401 ...
      status = 401;
      await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
      const atExpiry = calls.length;
      expect(atExpiry).toBeGreaterThan(healthy);
      expect(useConnectivityStore.getState().sessionExpired).toBe(true);
      // ... and that was the last request: nothing is asked for the next hour.
      await act(async () => { await vi.advanceTimersByTimeAsync(3_600_000); });
      expect(calls).toHaveLength(atExpiry);
      view.unmount();
    } finally {
      unwatch();
      window.fetch = original;
    }
  });

  it('waits longer while the answer stays the same, and from the interval again after a change', async () => {
    vi.useFakeTimers();
    let generation = 0;
    const seen: Array<{ at: number; generation: number }> = [];
    const started = Date.now();
    const { reads } = probe({ answer: () => { seen.push({ at: Date.now() - started, generation }); return { generation }; } });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    const gaps = reads.slice(1).map((moment, index) => moment - reads[index]);
    // The wait doubles (the first read of an answer counts as a change), up to the ceiling of the Schema.
    expect(gaps).toEqual([100, 100, 200, 400, 800, 800]);

    generation = 1;
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    const changed = seen.findIndex((read) => read.generation === 1);
    const after = seen.slice(changed).map((read) => read.at);
    // The read that found the change is followed by the interval again (100), and the quiet reads after it grow again.
    expect(after.slice(1, 5).map((moment, index) => moment - after[index])).toEqual([100, 200, 400, 800]);
  });

  it('does not slow down while a turn or task runs, and drops the long wait at once when one starts', async () => {
    vi.useFakeTimers();
    const { reads, rerender } = probe();
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    const quiet = reads.slice(-3).map((moment, index, all) => (index ? moment - all[index - 1] : 0)).slice(1);
    expect(quiet).toEqual([800, 800]);

    const startedBusy = reads.length;
    rerender({ busy: true });
    // The wait of 800 that was running is replaced by the interval, not waited out.
    await act(async () => { await vi.advanceTimersByTimeAsync(650); });
    expect(reads.length - startedBusy).toBeGreaterThanOrEqual(5);
    const gaps = reads.slice(startedBusy).map((moment, index, all) => (index ? moment - all[index - 1] : 100));
    expect(new Set(gaps)).toEqual(new Set([100]));
  });
});

describe('useSessionBusy', () => {
  it('is true while the session streams a turn or one of its tasks is not finished', () => {
    useChatStore.setState({ streamingBuffers: {} });
    useWorkspaceStore.setState({ trackedTasks: [] });
    const { result } = renderHook(({ id }: { id: string | null }) => useSessionBusy(id), { initialProps: { id: 's1' as string | null } });
    expect(result.current).toBe(false);

    act(() => { useChatStore.getState().initStreaming('s1', { content: '' }); });
    expect(result.current).toBe(true);
    act(() => { useChatStore.getState().finishStreaming('s1'); });
    expect(result.current).toBe(false);

    act(() => {
      useWorkspaceStore.getState().registerTask({ taskId: 't1', sessionId: 's1' } as Parameters<ReturnType<typeof useWorkspaceStore.getState>['registerTask']>[0]);
    });
    expect(result.current).toBe(true);
    act(() => { useWorkspaceStore.getState().markTaskNotFound('t1'); });
    expect(result.current).toBe(false);
  });
});
