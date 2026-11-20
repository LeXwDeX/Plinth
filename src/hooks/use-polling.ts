'use client';

import { useCallback, useEffect, useReducer, useRef } from 'react';

import { idleDelay, pageGate, type IdlePolling } from '@/lib/polling';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';

/**
 * Whether the session has something going on that its reads may see change: a turn is streaming, or a task of the
 * session is not finished. Idle backoff never applies while this is true.
 */
export function useSessionBusy(sessionId: string | null): boolean {
  const streaming = useChatStore((state) => Boolean(sessionId && state.streamingBuffers[sessionId]));
  const taskRunning = useWorkspaceStore((state) => Boolean(sessionId && state.trackedTasks.some((task) =>
    task.sessionId === sessionId && !task.notFound && (!task.terminal || task.resourcesPending))));
  return streaming || taskRunning;
}

/**
 * The `refreshInterval` of an SWR read that slows down while its data does not change (#385). SWR already skips a
 * hidden tab and reads again when it is shown; this adds the wait that grows from `baseMilliseconds` while the data
 * stays the same (`signature` says what "the same" is; by default the data itself, which SWR keeps identical while it
 * is deep-equal), and goes back to it on a change, while `busy`, and when the tab is shown again.
 *
 * The returned function changes with `busy` and with every return to visibility, which restarts SWR's timer from the
 * read's own interval at once instead of letting a long wait run out.
 */
export function useIdleInterval<Data>(
  baseMilliseconds: number,
  idle: IdlePolling | undefined,
  options: { busy?: boolean; signature?: (data: Data | undefined) => unknown } = {},
): (latest: Data | undefined) => number {
  const { busy = false, signature } = options;
  const state = useRef<{ seen: boolean; last: unknown; unchanged: number }>({ seen: false, last: undefined, unchanged: 0 });
  const [shown, shownAgain] = useReducer((count: number) => count + 1, 0);
  useEffect(() => pageGate.subscribe(() => {
    if (!pageGate.visible()) return;
    // SWR asks for the interval again as the function changes: that call is not a quiet read, so it must give 0.
    state.current.unchanged = -1;
    shownAgain();
  }), []);
  const signatureRef = useRef(signature);
  signatureRef.current = signature;
  return useCallback((latest: Data | undefined) => {
    const current = state.current;
    const mark = signatureRef.current ? signatureRef.current(latest) : latest;
    current.unchanged = busy || !current.seen || !Object.is(mark, current.last) ? 0 : current.unchanged + 1;
    current.seen = true;
    current.last = mark;
    return idleDelay(baseMilliseconds, current.unchanged, idle);
    // `shown` is not read: it only gives the function a new identity when the tab is shown again.
  }, [baseMilliseconds, idle?.backoff_factor, idle?.max_interval_milliseconds, busy, shown]);
}
