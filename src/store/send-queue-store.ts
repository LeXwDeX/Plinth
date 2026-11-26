import { create } from 'zustand';

import {
  cancelQueuedTurn, claimStoredTurn, foreignHold, parseQueue, parseSendHold, SEND_HOLD_MS, SEND_HOLD_PREFIX,
  SEND_QUEUE_KEY, sendHoldKey, writeQueue, type QueueBySession, type SendHold,
} from '@/lib/chat/send-queue';

/**
 * #665: the messages queued while a turn runs, kept in this browser so a reload or leaving the page does not lose
 * them. A deleted session takes its queued messages with it, and a logout clears them all. Storage may be missing or
 * refuse writes (private windows); the queue then lives for the page only.
 *
 * Every tab of the browser shares the stored queue: each change is made on the stored queue as it is now (not on this
 * tab's copy, which another tab may have changed), the other tabs follow it through the `storage` event, and a queued
 * turn is claimed (taken out of the stored queue) before it is sent, so two tabs never both send it. The claim also
 * sets this tab's hold of the session's send (claimStoredTurn), refreshed while the turn it sent runs and released
 * when it ends; another tab does not drain that session while the hold lives, and re-reads the session first after
 * it changes (foreignChanges). Holds are kept per session. Where the browser has Web Locks, the claiming tab also keeps
 * the session's turn lock until it releases the hold: a hidden tab's heartbeat is slowed down and its stored hold may
 * lapse, but the lock does not, and another tab's claim is refused while it is kept. A refresh never writes over
 * another tab's hold: that tab took the send over, and this one gives its hold up.
 */
export { SEND_QUEUE_KEY };

/** This tab, as the owner of the holds it sets. */
const TAB_ID = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
  ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;

/** One session's send this tab holds: the heartbeat that keeps the stored hold alive, and the turn's Web Lock. */
interface Hold {
  heartbeat: ReturnType<typeof setInterval> | undefined;
  unlock: (() => void) | undefined;
}
/** #665: the sessions whose send this tab holds, each with its own heartbeat and lock. */
const holds = new Map<string, Hold>();

type Locks = Pick<LockManager, 'request'>;

function webLocks(): Locks | undefined {
  const locks = typeof navigator !== 'undefined' ? navigator.locks : undefined;
  return locks && typeof locks.request === 'function' ? locks : undefined;
}

/** The Web Lock a tab keeps while the turn it sent for the session runs. */
function turnLockName(sessionId: string): string {
  return `${sendHoldKey(sessionId)}:turn`;
}

/**
 * Takes the session's turn lock if no other tab has it, and keeps it until the returned function is called. A lock is
 * held without timers, so a hidden tab, whose timers the browser slows down, keeps it while its turn runs. Resolves
 * to null when another tab has it, and to undefined when the browser refuses the lock (the stored hold alone counts).
 */
function takeTurnLock(locks: Locks, sessionId: string): Promise<(() => void) | null | undefined> {
  return new Promise((resolve) => {
    let release: () => void = () => undefined;
    const held = new Promise<void>((done) => { release = done; });
    try {
      locks.request(turnLockName(sessionId), { ifAvailable: true }, (lock) => {
        if (!lock) {
          resolve(null);
          return undefined;
        }
        resolve(release);
        return held;
      }).catch(() => resolve(undefined));
    } catch {
      resolve(undefined);
    }
  });
}

/**
 * #665: while a claim is refused because another tab still keeps the session's turn lock (its stored hold lapsed while
 * the tab was hidden), the drain waits as for a stored hold, until this time, instead of reading again at once.
 */
const lockedOut = new Map<string, number>();

// Set once a write was refused (a full or read-only storage): the stored queue no longer follows this page's.
let writesRefused = false;

/** The stored queue; null when storage cannot be used (the page's own copy is then the queue). */
function readStored(): QueueBySession | null {
  if (writesRefused) return null;
  let raw: string | null;
  try {
    raw = localStorage.getItem(SEND_QUEUE_KEY);
  } catch {
    return null;
  }
  return parseQueue(raw);
}

function write(queue: QueueBySession): void {
  try {
    writeQueue(localStorage, queue);
  } catch {
    // No storage: the queue lives for this page.
    writesRefused = true;
  }
}

interface SendQueueState {
  queue: QueueBySession;
  update: (change: (queue: QueueBySession) => QueueBySession) => void;
  /**
   * Takes the queued turn out of the queue before it is sent, and holds the session's send for this tab. False when it
   * is no longer queued (another tab sent or cancelled it) or another tab holds the send: the caller must not send it.
   */
  claim: (sessionId: string, id: string) => Promise<boolean>;
  /** The sessions whose send this tab holds. */
  heldSessions: () => string[];
  /**
   * Keeps this tab's hold of the session's send alive (every third of SEND_HOLD_MS while its turn runs, and when the
   * tab is shown again). A hold another tab has taken over meanwhile is not written over: this tab's hold is lost.
   */
  refreshHold: (sessionId: string) => void;
  /** Ends this tab's hold once the turn it sent is over. */
  releaseHold: (sessionId: string) => void;
  /** Another tab's live hold of the session's send, or null. */
  foreignHold: (sessionId: string) => SendHold | null;
  /** #665: per session, how many times another tab's hold changed; a change means this tab's messages may be stale. */
  foreignChanges: Readonly<Record<string, number>>;
  forgetSession: (sessionId: string) => void;
  /** Keeps only the queues of these sessions; see pruneQueuedSessions. */
  forgetSessionsExcept: (keep: (sessionId: string) => boolean) => void;
  clear: () => void;
}

export const useSendQueueStore = create<SendQueueState>((set, get) => ({
  queue: readStored() ?? {},
  update: (change) => {
    const base = readStored() ?? get().queue;
    const next = change(base);
    if (next !== base) write(next);
    if (next !== get().queue) set({ queue: next });
  },
  foreignChanges: {},
  claim: async (sessionId, id) => {
    const claimLocally = () => {
      let claimed = false;
      get().update((queue) => {
        if (!(queue[sessionId] ?? []).some((turn) => turn.id === id)) return queue;
        claimed = true;
        return cancelQueuedTurn(queue, sessionId, id);
      });
      return claimed;
    };
    if (readStored() === null) return claimLocally();
    const locks = webLocks();
    const claimStored = async () => {
      // Another tab's turn still runs while it keeps the session's turn lock, whatever its stored hold says.
      let unlock: (() => void) | undefined;
      if (locks && !holds.has(sessionId)) {
        const taken = await takeTurnLock(locks, sessionId);
        if (taken === null) {
          lockedOut.set(sessionId, Date.now() + SEND_HOLD_MS / 3);
          return false;
        }
        unlock = taken;
      }
      let result: ReturnType<typeof claimStoredTurn>;
      try {
        result = claimStoredTurn(localStorage, sessionId, id, TAB_ID, Date.now());
      } catch {
        unlock?.();
        writesRefused = true;
        return claimLocally();
      }
      if (result === 'claimed') startHold(sessionId, unlock);
      else unlock?.();
      followStored();
      return result === 'claimed';
    };
    if (locks) {
      try {
        return await locks.request(`${SEND_QUEUE_KEY}:claim`, claimStored);
      } catch {
        // A lock the browser refuses (an opaque origin): claim without it.
      }
    }
    return claimStored();
  },
  heldSessions: () => [...holds.keys()],
  refreshHold: (sessionId) => {
    if (!holds.has(sessionId)) return;
    try {
      const key = sendHoldKey(sessionId);
      const now = Date.now();
      const stored = parseSendHold(localStorage.getItem(key), now);
      // Another tab took the send over (this tab's hold lapsed while it was hidden): never write over its hold.
      if (stored && stored.owner !== TAB_ID) {
        loseHold(sessionId);
        return;
      }
      localStorage.setItem(key, JSON.stringify({ owner: TAB_ID, expiresAt: now + SEND_HOLD_MS } satisfies SendHold));
    } catch {
      // No storage: no other tab reads the hold either.
    }
  },
  releaseHold: (sessionId) => {
    if (!stopHold(sessionId)) return;
    try {
      const key = sendHoldKey(sessionId);
      if (parseSendHold(localStorage.getItem(key))?.owner === TAB_ID) localStorage.removeItem(key);
    } catch {
      // No storage: nothing to release.
    }
  },
  foreignHold: (sessionId) => {
    const now = Date.now();
    const until = lockedOut.get(sessionId);
    if (until !== undefined && until > now) return { owner: turnLockName(sessionId), expiresAt: until };
    if (until !== undefined) lockedOut.delete(sessionId);
    try {
      return foreignHold(parseSendHold(localStorage.getItem(sendHoldKey(sessionId)), now), TAB_ID, now);
    } catch {
      return null;
    }
  },
  forgetSession: (sessionId) => {
    // A removed session has no turn left to hold.
    get().releaseHold(sessionId);
    get().update((queue) => {
      if (!(sessionId in queue)) return queue;
      const { [sessionId]: _removed, ...rest } = queue;
      return rest;
    });
  },
  forgetSessionsExcept: (keep) => get().update((queue) => {
    const kept = Object.entries(queue).filter(([sessionId]) => keep(sessionId));
    return kept.length === Object.keys(queue).length ? queue : Object.fromEntries(kept);
  }),
  clear: () => {
    releaseAllHolds();
    set({ queue: {} });
    write({});
  },
}));

/** Starts this tab's hold of the session's send: a heartbeat, and the turn lock when the claim took one. */
function startHold(sessionId: string, unlock: (() => void) | undefined): void {
  lockedOut.delete(sessionId);
  const current = holds.get(sessionId);
  if (current) {
    // A new claim of a session this tab already holds keeps the lock and heartbeat it has.
    unlock?.();
    return;
  }
  holds.set(sessionId, {
    heartbeat: setInterval(() => useSendQueueStore.getState().refreshHold(sessionId), SEND_HOLD_MS / 3),
    unlock,
  });
}

/** Stops the hold's heartbeat and lock; false when this tab did not hold the session. */
function stopHold(sessionId: string): boolean {
  const hold = holds.get(sessionId);
  if (!hold) return false;
  holds.delete(sessionId);
  if (hold.heartbeat !== undefined) clearInterval(hold.heartbeat);
  hold.unlock?.();
  return true;
}

/** Another tab took the session's send over: this tab stops holding it and reads the session again before draining. */
function loseHold(sessionId: string): void {
  stopHold(sessionId);
  bumpForeignChange(sessionId);
}

function bumpForeignChange(sessionId: string): void {
  useSendQueueStore.setState((state) => ({
    foreignChanges: { ...state.foreignChanges, [sessionId]: (state.foreignChanges[sessionId] ?? 0) + 1 },
  }));
}

function followStored(): void {
  const stored = readStored();
  if (stored) useSendQueueStore.setState({ queue: stored });
}

/** Reads the stored queue again (a new page load, or a test). */
export function reloadSendQueue(): void {
  writesRefused = false;
  followStored();
}

/**
 * Follows another tab's change of the stored queue (the `storage` event; a `clear()` of storage has no key). The
 * turns another tab sent or cancelled leave this tab's box with it. A tab whose writes were refused keeps its own.
 */
export function onSendQueueStorage(event: Pick<StorageEvent, 'key'>): void {
  if (event.key === SEND_QUEUE_KEY || event.key === null) followStored();
  // Another tab claimed a turn of the session or its turn ended: this tab's messages may not show it yet.
  if (event.key?.startsWith(SEND_HOLD_PREFIX)) {
    bumpForeignChange(event.key.slice(SEND_HOLD_PREFIX.length));
  }
}

/** A tab that goes away releases its holds at once instead of letting them lapse. */
export function releaseAllHolds(): void {
  for (const sessionId of [...holds.keys()]) useSendQueueStore.getState().releaseHold(sessionId);
}

/** A tab shown again refreshes its holds at once: its heartbeat may have been slowed down while it was hidden. */
export function refreshAllHolds(): void {
  for (const sessionId of [...holds.keys()]) useSendQueueStore.getState().refreshHold(sessionId);
}

/**
 * #665: removes the stored holds that lapsed or cannot be read (a tab that crashed never released its hold), except
 * this tab's own. Run on load; a hold a hidden tab still keeps a turn lock for is written again by its next refresh.
 */
export function sweepLapsedHolds(now = Date.now()): void {
  try {
    const keys: string[] = [];
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index);
      if (key?.startsWith(SEND_HOLD_PREFIX)) keys.push(key);
    }
    for (const key of keys) {
      if (holds.has(key.slice(SEND_HOLD_PREFIX.length))) continue;
      const hold = parseSendHold(localStorage.getItem(key), now);
      if (!hold || hold.expiresAt <= now) localStorage.removeItem(key);
    }
  } catch {
    // No storage, or one that cannot list its keys: nothing to sweep.
  }
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('storage', onSendQueueStorage);
  window.addEventListener('pagehide', releaseAllHolds);
  sweepLapsedHolds();
}
if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') refreshAllHolds();
  });
}
