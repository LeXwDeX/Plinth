import { afterEach, describe, expect, it, vi } from 'vitest';

import { idleDelay, startPoller, type IdlePolling, type PollingGate } from './polling';

afterEach(() => {
  vi.useRealTimers();
});

const idle: IdlePolling = { backoff_factor: 2, max_interval_milliseconds: 800 };

/** A gate the test opens and closes, as the tab and the login do. */
function controllableGate() {
  const listeners = new Set<() => void>();
  const state = { visible: true, halted: false };
  const gate: PollingGate = {
    visible: () => state.visible,
    halted: () => state.halted,
    subscribe(listener) {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
  };
  return {
    gate,
    set(next: Partial<typeof state>) {
      Object.assign(state, next);
      listeners.forEach((listener) => listener());
    },
    listeners,
  };
}

/** The moments (ms after start) at which `read` ran, for a read that settles at once. */
function recordedReads(result: () => boolean | void | Promise<boolean | void> = () => false) {
  const times: number[] = [];
  const started = Date.now();
  const read = vi.fn(async () => {
    times.push(Date.now() - started);
    return result();
  });
  return { read, times };
}

describe('idleDelay', () => {
  it('grows by the factor after each quiet read, up to the ceiling, and never below the interval itself', () => {
    expect([0, 1, 2, 3, 4, 5].map((quiet) => idleDelay(100, quiet, idle))).toEqual([100, 200, 400, 800, 800, 800]);
    expect(idleDelay(100, 7, undefined)).toBe(100);
    // A ceiling under the interval must not make a read faster than its own interval.
    expect(idleDelay(1000, 3, { backoff_factor: 2, max_interval_milliseconds: 500 })).toBe(1000);
    // A factor of one is the old fixed interval.
    expect(idleDelay(3000, 9, { backoff_factor: 1, max_interval_milliseconds: 60000 })).toBe(3000);
    expect(idleDelay(3000, 5000, { backoff_factor: 2, max_interval_milliseconds: 60000 })).toBe(60000);
  });
});

describe('startPoller', () => {
  it('reads at once, then waits the interval after each read has settled', async () => {
    vi.useFakeTimers();
    const { gate } = controllableGate();
    const { read, times } = recordedReads(() => true);
    const poller = startPoller({ read, intervalMilliseconds: 100, gate });
    await vi.advanceTimersByTimeAsync(350);
    expect(times).toEqual([0, 100, 200, 300]);
    poller.stop();
  });

  it('waits longer after each read that found nothing new, and returns to the interval on a change', async () => {
    vi.useFakeTimers();
    const { gate } = controllableGate();
    const answers = [false, false, false, false, false, true, false];
    const { read, times } = recordedReads(() => answers.shift() ?? false);
    const poller = startPoller({ read, intervalMilliseconds: 100, idle, gate });
    await vi.advanceTimersByTimeAsync(5000);
    // Each quiet read doubles the next wait (200, 400, 800), up to the ceiling (800); the read that finds a change is
    // followed by the interval again (100), and the quiet reads after it start growing again (200).
    expect(times.slice(0, 8)).toEqual([0, 200, 600, 1400, 2200, 3000, 3100, 3300]);
    poller.stop();
  });

  it('does not slow down while a turn or a task runs, and counts a failed read as nothing new', async () => {
    vi.useFakeTimers();
    const { gate } = controllableGate();
    let busy = true;
    const { read, times } = recordedReads(() => { throw new Error('unavailable'); });
    const poller = startPoller({ read, intervalMilliseconds: 100, idle, busy: () => busy, gate });
    await vi.advanceTimersByTimeAsync(450);
    expect(times).toEqual([0, 100, 200, 300, 400]);
    busy = false;
    await vi.advanceTimersByTimeAsync(5000);
    // Failing reads are not asked faster than quiet ones.
    expect(times.slice(5, 9)).toEqual([500, 700, 1100, 1900]);
    poller.stop();
  });

  it('does not read in a hidden tab, reads once when it is shown, and goes on from the interval', async () => {
    vi.useFakeTimers();
    const control = controllableGate();
    const { read, times } = recordedReads(() => true);
    const poller = startPoller({ read, intervalMilliseconds: 100, gate: control.gate });
    await vi.advanceTimersByTimeAsync(150);
    expect(times).toEqual([0, 100]);

    control.set({ visible: false });
    await vi.advanceTimersByTimeAsync(3_600_000);
    expect(times).toEqual([0, 100]);

    control.set({ visible: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(times).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(100);
    expect(times).toHaveLength(4);
    poller.stop();
  });

  it('a tab that starts hidden stays quiet until it is shown', async () => {
    vi.useFakeTimers();
    const control = controllableGate();
    control.set({ visible: false });
    const { read } = recordedReads();
    const poller = startPoller({ read, intervalMilliseconds: 100, gate: control.gate });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(read).not.toHaveBeenCalled();
    control.set({ visible: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(1);
    poller.stop();
  });

  it('a read that finishes after the tab was hidden does not schedule the next one', async () => {
    vi.useFakeTimers();
    const control = controllableGate();
    let finish: (() => void) | undefined;
    const read = vi.fn(() => new Promise<boolean>((resolve) => { finish = () => resolve(true); }));
    const poller = startPoller({ read, intervalMilliseconds: 100, gate: control.gate });
    control.set({ visible: false });
    finish?.();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(read).toHaveBeenCalledTimes(1);
    poller.stop();
  });

  it('stops for good once the login has ended, and only a signed-in page reads again', async () => {
    vi.useFakeTimers();
    const control = controllableGate();
    const { read } = recordedReads(() => true);
    const poller = startPoller({ read, intervalMilliseconds: 100, gate: control.gate });
    await vi.advanceTimersByTimeAsync(250);
    expect(read).toHaveBeenCalledTimes(3);

    control.set({ halted: true });
    await vi.advanceTimersByTimeAsync(3_600_000);
    expect(read).toHaveBeenCalledTimes(3);
    // Showing the tab again does not read as long as the login is over.
    control.set({ visible: false });
    control.set({ visible: true });
    await vi.advanceTimersByTimeAsync(3_600_000);
    expect(read).toHaveBeenCalledTimes(3);

    control.set({ halted: false });
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(4);
    poller.stop();
  });

  it('refresh reads now and starts again from the interval; a refresh during a read reads once more after it', async () => {
    vi.useFakeTimers();
    const { gate } = controllableGate();
    const { read, times } = recordedReads(() => false);
    const poller = startPoller({ read, intervalMilliseconds: 100, idle, gate });
    await vi.advanceTimersByTimeAsync(1500);
    expect(times).toEqual([0, 200, 600, 1400]);
    poller.refresh();
    await vi.advanceTimersByTimeAsync(0);
    expect(times).toEqual([0, 200, 600, 1400, 1500]);
    // The wait that was running (to 2200) is gone: it starts again from the interval, which the quiet read doubles.
    await vi.advanceTimersByTimeAsync(200);
    expect(times.at(-1)).toBe(1700);

    let finish: (() => void) | undefined;
    const slow = vi.fn(() => new Promise<boolean>((resolve) => { finish = () => resolve(true); }));
    const second = startPoller({ read: slow, intervalMilliseconds: 100, gate });
    second.refresh();
    expect(slow).toHaveBeenCalledTimes(1);
    finish?.();
    await vi.advanceTimersByTimeAsync(0);
    expect(slow).toHaveBeenCalledTimes(2);
    second.stop();
    poller.stop();
  });

  it('stop aborts the read in flight, leaves nothing scheduled and lets go of the gate', async () => {
    vi.useFakeTimers();
    const control = controllableGate();
    let signal: AbortSignal | undefined;
    let finish: (() => void) | undefined;
    const read = vi.fn((request: AbortSignal) => {
      signal = request;
      return new Promise<boolean>((resolve) => { finish = () => resolve(true); });
    });
    const poller = startPoller({ read, intervalMilliseconds: 100, gate: control.gate });
    expect(control.listeners.size).toBe(1);
    poller.stop();
    expect(signal?.aborted).toBe(true);
    expect(control.listeners.size).toBe(0);
    finish?.();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(read).toHaveBeenCalledTimes(1);
  });
});
