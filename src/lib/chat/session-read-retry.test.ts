// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatBrowserError } from '@/lib/chat/browser-client';
import {
  sessionReadRetryDelayMs,
  startSessionReadRetry,
  type SessionReadRetry,
  type SessionReadRetryPolicy,
} from '@/lib/chat/session-read-retry';

// #314: the page reads a session's chat model and thinking level again when the first read failed. These tests drive the
// retry controller alone (fake clock, a read the test rejects or resolves), so the schedule, the bound and what wakes it
// up are pinned without a page around them.

const policy: SessionReadRetryPolicy = {
  max_retries: 4,
  initial_delay_milliseconds: 1000,
  max_backoff_milliseconds: 8000,
  max_retry_after_milliseconds: 60_000,
};

describe('the Schema declares the retry bounds the page uses (#314)', () => {
  it('is a bounded schedule: a few retries, waits that grow, a Retry-After cap above the backoff cap', () => {
    const declared = defaultCatalog.application.runtime.frontend.session_read_retry;
    expect(declared.max_retries).toBeGreaterThanOrEqual(1);
    expect(declared.max_retries).toBeLessThanOrEqual(10);
    expect(declared.initial_delay_milliseconds).toBeGreaterThanOrEqual(100);
    expect(declared.max_backoff_milliseconds).toBeGreaterThanOrEqual(declared.initial_delay_milliseconds);
    expect(declared.max_retry_after_milliseconds).toBeGreaterThanOrEqual(declared.max_backoff_milliseconds);
  });
});

describe('sessionReadRetryDelayMs (#314)', () => {
  it('doubles from the initial wait and stops growing at the backoff cap', () => {
    const waits = [0, 1, 2, 3, 4, 5, 40].map((count) => sessionReadRetryDelayMs(new Error('down'), count, policy));
    expect(waits).toEqual([1000, 2000, 4000, 8000, 8000, 8000, 8000]);
  });

  it('waits at least as long as the server asked, up to the Retry-After cap', () => {
    const limited = (seconds: number) => new ChatBrowserError('', 429, '', seconds);
    expect(sessionReadRetryDelayMs(limited(30), 0, policy)).toBe(30_000);
    expect(sessionReadRetryDelayMs(limited(1), 3, policy)).toBe(8000);
    expect(sessionReadRetryDelayMs(limited(3600), 0, policy)).toBe(60_000);
  });

  it('ignores a Retry-After that is not on a ChatBrowserError', () => {
    expect(sessionReadRetryDelayMs(Object.assign(new Error('x'), { retryAfterSeconds: 30 }), 0, policy)).toBe(1000);
  });
});

describe('startSessionReadRetry (#314)', () => {
  let failed = false;
  const outcomes: Array<{ settle: (error?: unknown) => void; signal: AbortSignal }> = [];
  const read = vi.fn((signal: AbortSignal) => new Promise<void>((resolve, reject) => {
    outcomes.push({ signal, settle: (error) => (error === undefined ? resolve() : reject(error)) });
  }));
  /** The read on its way is answered; the controller sees the outcome before the test goes on. */
  const answer = async (error?: unknown) => {
    const outcome = outcomes.shift()!;
    failed = error !== undefined;
    outcome.settle(error);
    await vi.advanceTimersByTimeAsync(0);
  };
  const started: SessionReadRetry[] = [];
  const start = () => {
    const running = startSessionReadRetry({ read, failed: () => failed, policy });
    started.push(running);
    return running;
  };

  beforeEach(() => {
    vi.useFakeTimers();
    failed = false;
    outcomes.length = 0;
    read.mockClear();
  });
  afterEach(() => {
    // Every controller listens on the window: stop them all so one test's controller never answers another's event.
    started.splice(0).forEach((running) => running.stop());
    vi.useRealTimers();
  });

  it('reads once at start and leaves a successful read alone', async () => {
    start();
    expect(read).toHaveBeenCalledTimes(1);
    await answer();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(read).toHaveBeenCalledTimes(1);
  });

  it('reads again after the growing waits and stops asking once a read succeeds', async () => {
    start();
    await answer(new Error('down'));
    await vi.advanceTimersByTimeAsync(999);
    expect(read).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(2);
    await answer(new Error('down'));
    await vi.advanceTimersByTimeAsync(1999);
    expect(read).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(3);
    await answer();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it('gives up after max_retries more reads, so a persistent failure cannot keep asking', async () => {
    start();
    for (let round = 0; round < policy.max_retries + 1; round += 1) {
      await answer(new Error('down'));
      await vi.advanceTimersByTimeAsync(policy.max_backoff_milliseconds);
    }
    expect(read).toHaveBeenCalledTimes(1 + policy.max_retries);
    expect(outcomes).toEqual([]);
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(read).toHaveBeenCalledTimes(1 + policy.max_retries);
  });

  it('waits for the Retry-After a rate limited read asked for', async () => {
    start();
    await answer(new ChatBrowserError('', 429, '', 30));
    await vi.advanceTimersByTimeAsync(29_999);
    expect(read).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('cancels the read on its way and the waiting retry when stopped', async () => {
    const running = start();
    const [first] = outcomes;
    expect(first.signal.aborted).toBe(false);
    running.stop();
    expect(first.signal.aborted).toBe(true);
    // The aborted read ends in a rejection; it neither schedules a retry nor counts as a failure.
    await answer(new DOMException('aborted', 'AbortError'));
    await vi.advanceTimersByTimeAsync(120_000);
    expect(read).toHaveBeenCalledTimes(1);

    const second = start();
    await answer(new Error('down'));
    second.stop();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('starts a fresh bounded cycle when a person retries after it gave up', async () => {
    const running = start();
    for (let round = 0; round < policy.max_retries + 1; round += 1) {
      await answer(new Error('down'));
      await vi.advanceTimersByTimeAsync(policy.max_backoff_milliseconds);
    }
    expect(read).toHaveBeenCalledTimes(5);
    running.retry();
    expect(read).toHaveBeenCalledTimes(6);
    await answer(new Error('down'));
    await vi.advanceTimersByTimeAsync(1000);
    expect(read).toHaveBeenCalledTimes(7);
    await answer();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(read).toHaveBeenCalledTimes(7);
  });

  it('does not start a second read while one is on its way', async () => {
    const running = start();
    running.retry();
    window.dispatchEvent(new Event('focus'));
    expect(read).toHaveBeenCalledTimes(1);
  });

  describe.each([
    ['the window regains focus', () => window.dispatchEvent(new Event('focus'))],
    ['the network comes back', () => window.dispatchEvent(new Event('online'))],
    ['the tab becomes visible', () => document.dispatchEvent(new Event('visibilitychange'))],
  ])('when %s', (_name, wake) => {
    it('reads again at once if the last read failed, ahead of the waiting retry', async () => {
      start();
      await answer(new Error('down'));
      wake();
      expect(read).toHaveBeenCalledTimes(2);
      await answer(new Error('down'));
      // The wait for the next retry started from the woken read; the cancelled one fires nothing extra.
      await vi.advanceTimersByTimeAsync(1000);
      expect(read).toHaveBeenCalledTimes(3);
    });

    it('reads again after the cycle gave up, one read per wake and a fresh bounded cycle', async () => {
      start();
      for (let round = 0; round < policy.max_retries + 1; round += 1) {
        await answer(new Error('down'));
        await vi.advanceTimersByTimeAsync(policy.max_backoff_milliseconds);
      }
      expect(read).toHaveBeenCalledTimes(5);
      wake();
      expect(read).toHaveBeenCalledTimes(6);
      await answer();
      expect(read).toHaveBeenCalledTimes(6);
    });

    it('does nothing when the last read did not fail', async () => {
      start();
      await answer();
      wake();
      await vi.advanceTimersByTimeAsync(10_000);
      expect(read).toHaveBeenCalledTimes(1);
    });

    it('does nothing once stopped', async () => {
      const running = start();
      await answer(new Error('down'));
      running.stop();
      wake();
      await vi.advanceTimersByTimeAsync(120_000);
      expect(read).toHaveBeenCalledTimes(1);
    });
  });

  it('ignores the tab turning hidden', async () => {
    start();
    await answer(new Error('down'));
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    visibility.mockRestore();
    expect(read).toHaveBeenCalledTimes(1);
  });
});
