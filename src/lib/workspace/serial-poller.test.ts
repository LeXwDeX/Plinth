import { afterEach, expect, it, vi } from 'vitest';

import { startSerialPoller } from './serial-poller';

afterEach(() => {
  vi.useRealTimers();
});

it('waits for a slow request before scheduling the next poll', async () => {
  vi.useFakeTimers();
  let finishFirst: (() => void) | undefined;
  const poll = vi.fn()
    .mockImplementationOnce(() => new Promise<void>((resolve) => { finishFirst = resolve; }))
    .mockResolvedValue(undefined);
  const stop = startSerialPoller(poll, 100);

  await vi.advanceTimersByTimeAsync(500);
  expect(poll).toHaveBeenCalledTimes(1);

  finishFirst?.();
  await Promise.resolve();
  await vi.advanceTimersByTimeAsync(99);
  expect(poll).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1);
  expect(poll).toHaveBeenCalledTimes(2);
  stop();
});

it('aborts the current request and prevents another poll after cleanup', async () => {
  vi.useFakeTimers();
  let finish: (() => void) | undefined;
  let requestSignal: AbortSignal | undefined;
  const poll = vi.fn((signal: AbortSignal) => {
    requestSignal = signal;
    return new Promise<void>((resolve) => { finish = resolve; });
  });
  const stop = startSerialPoller(poll, 100);
  stop();
  expect(requestSignal?.aborted).toBe(true);
  finish?.();
  await vi.advanceTimersByTimeAsync(500);
  expect(poll).toHaveBeenCalledTimes(1);
});
