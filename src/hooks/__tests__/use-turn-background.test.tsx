// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { asksTurnBackground, useTurnBackground } from '../use-turn-background';

const ready = {
  sessionId: 's1', streaming: true, taskRunning: true, route: '/api/chat/sessions/{sessionID}/turn/background',
  member: 'backgroundAvailable', intervalMilliseconds: 5000,
};

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

// #137: the composer only asks while a turn streams and a Task of the session runs, and only when the
// Schema declares the route, its member and a poll interval.
describe('asksTurnBackground', () => {
  it('asks only with every condition', () => {
    expect(asksTurnBackground(ready)).toBe(true);
    for (const missing of [
      { sessionId: null }, { streaming: false }, { taskRunning: false }, { route: undefined },
      { member: undefined }, { intervalMilliseconds: 0 }, { intervalMilliseconds: undefined },
    ]) {
      expect(asksTurnBackground({ ...ready, ...missing })).toBe(false);
    }
  });
});

describe('useTurnBackground', () => {
  it('follows the server answer while it asks and drops it when it stops asking', async () => {
    vi.useFakeTimers();
    const answers = [false, true];
    const read = vi.fn(async () => answers.shift() ?? true);
    const client = { turnBackgroundAvailable: read };
    const { result, rerender } = renderHook(({ asks }) => useTurnBackground(client, 's1', asks, 5000), {
      initialProps: { asks: true },
    });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(result.current).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(result.current).toBe(true);
    expect(read).toHaveBeenCalledWith('s1', expect.any(AbortSignal));
    rerender({ asks: false });
    expect(result.current).toBe(false);
    const calls = read.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(20000); });
    expect(read.mock.calls.length).toBe(calls);
  });

  it('never asks while it should not', async () => {
    vi.useFakeTimers();
    const read = vi.fn(async () => true);
    const { result } = renderHook(() => useTurnBackground({ turnBackgroundAvailable: read }, 's1', false, 5000));
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(result.current).toBe(false);
    expect(read).not.toHaveBeenCalled();
  });
});
