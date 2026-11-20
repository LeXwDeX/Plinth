import { signedInGate, startPoller } from '@/lib/polling';

/**
 * Run a poll only after the previous request has settled. It stands still while the tab is hidden or the login has
 * ended, and reads once more when the tab is shown again (#385). `whileHidden` keeps it going in a hidden tab, for
 * work the person is waiting for whose end the page announces from the background.
 */
export function startSerialPoller(
  poll: (signal: AbortSignal) => Promise<void>,
  intervalMilliseconds: number,
  options: { whileHidden?: boolean } = {},
): () => void {
  // A failed poll is retried on the next cycle; the caller owns error UI.
  return startPoller({
    read: poll,
    intervalMilliseconds,
    ...(options.whileHidden ? { gate: signedInGate } : {}),
  }).stop;
}
