// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import { useConnectivityStore } from '@/store/connectivity-store';
import { startSerialPoller } from './serial-poller';

let visibility: DocumentVisibilityState = 'visible';

beforeEach(() => {
  visibility = 'visible';
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
  useConnectivityStore.setState({ online: true, sessionExpired: false, reconnectTick: 0 });
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function show(next: DocumentVisibilityState) {
  visibility = next;
  document.dispatchEvent(new Event('visibilitychange'));
}

// #385: a poll started with the page's own gate stands still in a hidden tab and reads once when the tab is shown.
it('does not poll in a hidden tab, and polls once when the tab is shown', async () => {
  const poll = vi.fn(async () => undefined);
  const stop = startSerialPoller(poll, 100);
  await vi.advanceTimersByTimeAsync(250);
  expect(poll).toHaveBeenCalledTimes(3);

  show('hidden');
  await vi.advanceTimersByTimeAsync(3_600_000);
  expect(poll).toHaveBeenCalledTimes(3);

  show('visible');
  await vi.advanceTimersByTimeAsync(0);
  expect(poll).toHaveBeenCalledTimes(4);
  stop();
});

// The task tracker: a finished task is announced from a background tab (title, notification), so its progress is read
// there as well; only an ended login stops it.
it('keeps polling in a hidden tab when asked to, until the login ends', async () => {
  const poll = vi.fn(async () => undefined);
  const stop = startSerialPoller(poll, 100, { whileHidden: true });
  show('hidden');
  await vi.advanceTimersByTimeAsync(1000);
  expect(poll.mock.calls.length).toBeGreaterThanOrEqual(10);

  useConnectivityStore.getState().markSessionExpired();
  const atExpiry = poll.mock.calls.length;
  await vi.advanceTimersByTimeAsync(3_600_000);
  expect(poll).toHaveBeenCalledTimes(atExpiry);
  stop();
});

it('does not poll after the login ended, even in a visible tab', async () => {
  const poll = vi.fn(async () => undefined);
  const stop = startSerialPoller(poll, 100);
  await vi.advanceTimersByTimeAsync(150);
  const before = poll.mock.calls.length;
  useConnectivityStore.getState().markSessionExpired();
  await vi.advanceTimersByTimeAsync(3_600_000);
  expect(poll).toHaveBeenCalledTimes(before);
  stop();
});
