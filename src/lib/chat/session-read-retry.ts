import { ChatBrowserError } from '@/lib/chat/browser-client';
import type { ProductSchema } from '@/lib/product-schema';

/**
 * #314: the Schema's bounds (`runtime.frontend.session_read_retry`) for reading a session's chat model and thinking
 * level again after a failed read.
 */
export type SessionReadRetryPolicy = ProductSchema['runtime']['frontend']['session_read_retry'];

/**
 * How long to wait before retry number `retryCount` (0 for the first): exponential from the initial wait up to the
 * backoff cap, and never shorter than the Retry-After a rate limited read carried (itself capped). The same rule as
 * the sign-in session read (`sessionRetryDelayMs`), with its own Schema bounds.
 */
export function sessionReadRetryDelayMs(error: unknown, retryCount: number, policy: SessionReadRetryPolicy): number {
  const backoff = Math.min(policy.max_backoff_milliseconds, policy.initial_delay_milliseconds * 2 ** Math.min(retryCount, 16));
  const asked = error instanceof ChatBrowserError && error.retryAfterSeconds !== undefined ? error.retryAfterSeconds * 1000 : 0;
  return Math.min(policy.max_retry_after_milliseconds, Math.max(backoff, asked));
}

export interface SessionReadRetryOptions {
  /** One read of the value. It rejects when the read failed; `signal` is aborted by `stop()`. */
  read: (signal: AbortSignal) => Promise<unknown>;
  /** Whether the latest read failed. The window regaining focus or the network coming back read again only then. */
  failed: () => boolean;
  policy: SessionReadRetryPolicy;
}

export interface SessionReadRetry {
  /** Reads now, as a fresh cycle (the person asked); does nothing while a read is on its way. */
  retry: () => void;
  /** Cancels the read on its way and the wait for the next one, and stops listening. Nothing is read afterwards. */
  stop: () => void;
}

/**
 * Reads once now and, when that fails, reads again after growing waits, at most `policy.max_retries` more times. A
 * read that fails after that is not repeated by itself: the value stays unread until the person retries, the window
 * regains focus or the network comes back, each of which starts a fresh bounded cycle, so a persistent failure never
 * keeps requesting. Reads one at a time. A read cancelled by `stop()` says nothing about the server and schedules
 * nothing.
 */
export function startSessionReadRetry(options: SessionReadRetryOptions): SessionReadRetry {
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let reading = false;
  let stopped = false;
  // Failed reads of the current cycle; the cycle may retry while this has not passed `max_retries`.
  let failures = 0;

  const clearWait = () => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
  };

  const attempt = () => {
    if (stopped || reading) return;
    clearWait();
    reading = true;
    new Promise<unknown>((resolve) => resolve(options.read(controller.signal))).then(
      () => {
        reading = false;
        failures = 0;
      },
      (error: unknown) => {
        reading = false;
        if (stopped || controller.signal.aborted) return;
        failures += 1;
        if (failures > options.policy.max_retries) return;
        timer = setTimeout(attempt, sessionReadRetryDelayMs(error, failures - 1, options.policy));
      },
    );
  };

  const restart = () => {
    if (stopped || reading) return;
    failures = 0;
    attempt();
  };
  const wake = () => {
    if (options.failed()) restart();
  };
  const wakeWhenVisible = () => {
    if (document.visibilityState === 'visible') wake();
  };

  const listening = typeof window !== 'undefined' && typeof document !== 'undefined';
  if (listening) {
    window.addEventListener('focus', wake);
    window.addEventListener('online', wake);
    document.addEventListener('visibilitychange', wakeWhenVisible);
  }
  attempt();

  return {
    retry: restart,
    stop: () => {
      if (stopped) return;
      stopped = true;
      controller.abort();
      clearWait();
      if (listening) {
        window.removeEventListener('focus', wake);
        window.removeEventListener('online', wake);
        document.removeEventListener('visibilitychange', wakeWhenVisible);
      }
    },
  };
}
