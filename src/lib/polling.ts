import { useConnectivityStore } from '@/store/connectivity-store';

/**
 * The page's repeating reads (#385). Three rules hold for every one of them, so they live here and not in each read:
 *
 * - a hidden tab does not read, and reads once when it is shown again;
 * - a login that has ended does not read: every read would be a 401 (the connectivity store raises `sessionExpired`
 *   from the first 401 of the page's own API);
 * - while nothing changes, and nothing is running, the wait grows up to a ceiling (`idle_polling` of the Product
 *   Schema), and it returns to the read's own interval on a change, on becoming visible, or while a turn or a task runs.
 *
 * The intervals themselves are Schema (`runtime.frontend`); nothing here is a product setting.
 */

/** Schema `runtime.frontend.idle_polling`. */
export interface IdlePolling {
  backoff_factor: number;
  max_interval_milliseconds: number;
}

/** Why a read must stand still: the tab is hidden, or the login has ended. */
export interface PollingGate {
  visible(): boolean;
  halted(): boolean;
  /** Calls `listener` whenever either changes; returns the way to stop. */
  subscribe(listener: () => void): () => void;
}

export const pageGate: PollingGate = {
  visible: () => typeof document === 'undefined' || document.visibilityState !== 'hidden',
  halted: () => useConnectivityStore.getState().sessionExpired,
  subscribe(listener) {
    if (typeof document !== 'undefined') document.addEventListener('visibilitychange', listener);
    const unsubscribe = useConnectivityStore.subscribe((state, previous) => {
      if (state.sessionExpired !== previous.sessionExpired) listener();
    });
    return () => {
      if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', listener);
      unsubscribe();
    };
  },
};

/**
 * The gate of a read that must go on in a hidden tab: it stops only when the login has ended. For work the person has
 * started and is waiting for, whose end the page announces while it is in the background (the tab title and the
 * browser notification of a finished task).
 */
export const signedInGate: PollingGate = {
  visible: () => true,
  halted: pageGate.halted,
  subscribe: pageGate.subscribe,
};

export function pollingAllowed(gate: PollingGate = pageGate): boolean {
  return gate.visible() && !gate.halted();
}

/**
 * The page's SWR configuration for every repeating read (#385). SWR runs its interval, focus, reconnect and error-retry
 * revalidations only while `isVisible()` is true, so this one function stops all of them in a hidden tab and after the
 * login ended, with the data they already hold left on the page. SWR reads once more when the tab is shown again.
 */
export const swrPollingConfig = { isVisible: () => pollingAllowed() };

/**
 * The wait after `unchanged` reads in a row found nothing new: the read's own interval, multiplied by the Schema's
 * factor each time, never above the Schema's ceiling and never below the interval itself.
 */
export function idleDelay(baseMilliseconds: number, unchanged: number, idle: IdlePolling | undefined): number {
  if (!idle || unchanged <= 0) return baseMilliseconds;
  const grown = Math.round(baseMilliseconds * idle.backoff_factor ** unchanged);
  return Math.max(baseMilliseconds, Math.min(idle.max_interval_milliseconds, grown));
}

export interface PollClock {
  setTimeout(callback: () => void, milliseconds: number): unknown;
  clearTimeout(handle: unknown): void;
}

const browserClock: PollClock = {
  setTimeout: (callback, milliseconds) => globalThis.setTimeout(callback, milliseconds),
  clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
};

export interface PollerOptions {
  /**
   * One read. Resolve `false` when it found nothing new; anything else counts as a change. A failed read counts as
   * nothing new, so a failing endpoint is not asked faster than a quiet one.
   */
  read(signal: AbortSignal): Promise<boolean | void>;
  intervalMilliseconds: number;
  /** Omitted keeps the interval constant. */
  idle?: IdlePolling;
  /** While true the wait does not grow: a turn or a task is running and may change what is read. */
  busy?: () => boolean;
  gate?: PollingGate;
  clock?: PollClock;
}

export interface Poller {
  stop(): void;
  /** Reads now, and starts again from the read's own interval. */
  refresh(): void;
}

/**
 * Reads, waits, reads again, never two reads at once. Reads at once when started; stands still while the gate is closed
 * and reads once more as it opens.
 */
export function startPoller(options: PollerOptions): Poller {
  const gate = options.gate ?? pageGate;
  const clock = options.clock ?? browserClock;
  let stopped = false;
  let running = false;
  let again = false;
  let timer: unknown;
  let unchanged = 0;
  let current: AbortController | null = null;

  const schedule = () => {
    if (stopped || running || timer !== undefined || !pollingAllowed(gate)) return;
    const wait = options.busy?.() ? options.intervalMilliseconds : idleDelay(options.intervalMilliseconds, unchanged, options.idle);
    timer = clock.setTimeout(() => {
      timer = undefined;
      void run();
    }, wait);
  };

  const run = async (): Promise<void> => {
    if (stopped || running || !pollingAllowed(gate)) return;
    running = true;
    const request = new AbortController();
    current = request;
    let changed = false;
    try {
      changed = (await options.read(request.signal)) !== false;
    } catch {
      // A failed read is tried again on the next turn of the loop; the reader owns any error display.
    } finally {
      running = false;
      if (current === request) current = null;
    }
    if (stopped) return;
    unchanged = changed || options.busy?.() ? 0 : unchanged + 1;
    if (again) {
      again = false;
      unchanged = 0;
      void run();
      return;
    }
    schedule();
  };

  const wake = () => {
    if (stopped) return;
    if (pollingAllowed(gate)) {
      // Shown again (or signed in again): read once now, then from the read's own interval.
      if (!running && timer === undefined) {
        unchanged = 0;
        void run();
      }
    } else if (timer !== undefined) {
      clock.clearTimeout(timer);
      timer = undefined;
    }
  };

  const unsubscribe = gate.subscribe(wake);
  void run();
  return {
    stop() {
      if (stopped) return;
      stopped = true;
      unsubscribe();
      if (timer !== undefined) clock.clearTimeout(timer);
      timer = undefined;
      current?.abort();
    },
    refresh() {
      if (stopped) return;
      unchanged = 0;
      if (running) {
        again = true;
        return;
      }
      if (timer !== undefined) {
        clock.clearTimeout(timer);
        timer = undefined;
      }
      void run();
    },
  };
}
