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
 * it changes (foreignChanges).
 */
export { SEND_QUEUE_KEY };

/** This tab, as the owner of the holds it sets. */
const TAB_ID = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
  ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
/** The sessions whose send this tab holds. */
const heldSessions = new Set<string>();

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
  /** Keeps this tab's hold of the session's send alive (every third of SEND_HOLD_MS while its turn runs). */
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
    const claimStored = () => {
      try {
        const result = claimStoredTurn(localStorage, sessionId, id, TAB_ID, Date.now());
        if (result === 'claimed') heldSessions.add(sessionId);
        followStored();
        return result === 'claimed';
      } catch {
        writesRefused = true;
        return claimLocally();
      }
    };
    const locks = typeof navigator !== 'undefined' ? navigator.locks : undefined;
    if (locks && typeof locks.request === 'function') {
      try {
        return await locks.request(`${SEND_QUEUE_KEY}:claim`, claimStored);
      } catch {
        // A lock the browser refuses (an opaque origin): claim without it.
      }
    }
    return claimStored();
  },
  refreshHold: (sessionId) => {
    if (!heldSessions.has(sessionId)) return;
    try {
      localStorage.setItem(sendHoldKey(sessionId), JSON.stringify({ owner: TAB_ID, expiresAt: Date.now() + SEND_HOLD_MS } satisfies SendHold));
    } catch {
      // No storage: no other tab reads the hold either.
    }
  },
  releaseHold: (sessionId) => {
    if (!heldSessions.delete(sessionId)) return;
    try {
      if (parseSendHold(localStorage.getItem(sendHoldKey(sessionId)))?.owner === TAB_ID) localStorage.removeItem(sendHoldKey(sessionId));
    } catch {
      // No storage: nothing to release.
    }
  },
  foreignHold: (sessionId) => {
    try {
      return foreignHold(parseSendHold(localStorage.getItem(sendHoldKey(sessionId))), TAB_ID, Date.now());
    } catch {
      return null;
    }
  },
  forgetSession: (sessionId) => get().update((queue) => {
    if (!(sessionId in queue)) return queue;
    const { [sessionId]: _removed, ...rest } = queue;
    return rest;
  }),
  forgetSessionsExcept: (keep) => get().update((queue) => {
    const kept = Object.entries(queue).filter(([sessionId]) => keep(sessionId));
    return kept.length === Object.keys(queue).length ? queue : Object.fromEntries(kept);
  }),
  clear: () => {
    set({ queue: {} });
    write({});
  },
}));

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
    const sessionId = event.key.slice(SEND_HOLD_PREFIX.length);
    useSendQueueStore.setState((state) => ({
      foreignChanges: { ...state.foreignChanges, [sessionId]: (state.foreignChanges[sessionId] ?? 0) + 1 },
    }));
  }
}

/** A tab that goes away releases its holds at once instead of letting them lapse. */
export function releaseAllHolds(): void {
  for (const sessionId of [...heldSessions]) useSendQueueStore.getState().releaseHold(sessionId);
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('storage', onSendQueueStorage);
  window.addEventListener('pagehide', releaseAllHolds);
}
