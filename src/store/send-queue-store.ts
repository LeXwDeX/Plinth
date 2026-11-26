import { create } from 'zustand';

import { cancelQueuedTurn, type QueueBySession } from '@/lib/chat/send-queue';

/**
 * #665: the messages queued while a turn runs, kept in this browser so a reload or leaving the page does not lose
 * them. A deleted session takes its queued messages with it, and a logout clears them all. Storage may be missing or
 * refuse writes (private windows); the queue then lives for the page only.
 *
 * Every tab of the browser shares the stored queue: each change is made on the stored queue as it is now (not on this
 * tab's copy, which another tab may have changed), the other tabs follow it through the `storage` event, and a queued
 * turn is claimed (taken out of the stored queue) before it is sent, so two tabs never both send it.
 */
export const SEND_QUEUE_KEY = 'plinth.queuedTurns';

function parse(raw: string | null): QueueBySession {
  try {
    const parsed: unknown = JSON.parse(raw ?? '{}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed as Record<string, unknown>).filter(
      (entry): entry is [string, QueueBySession[string]] => Array.isArray(entry[1]) && entry[1].length > 0,
    ));
  } catch {
    return {};
  }
}

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
  return parse(raw);
}

function write(queue: QueueBySession): void {
  try {
    if (Object.keys(queue).length === 0) localStorage.removeItem(SEND_QUEUE_KEY);
    else localStorage.setItem(SEND_QUEUE_KEY, JSON.stringify(queue));
  } catch {
    // No storage: the queue lives for this page.
    writesRefused = true;
  }
}

interface SendQueueState {
  queue: QueueBySession;
  update: (change: (queue: QueueBySession) => QueueBySession) => void;
  /**
   * Takes the queued turn out of the queue before it is sent. False when it is no longer queued (another tab sent or
   * cancelled it): the caller must not send it.
   */
  claim: (sessionId: string, id: string) => boolean;
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
  claim: (sessionId, id) => {
    let claimed = false;
    get().update((queue) => {
      if (!(queue[sessionId] ?? []).some((turn) => turn.id === id)) return queue;
      claimed = true;
      return cancelQueuedTurn(queue, sessionId, id);
    });
    return claimed;
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
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('storage', onSendQueueStorage);
}
