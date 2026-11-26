import { create } from 'zustand';

import type { QueueBySession } from '@/lib/chat/send-queue';

/**
 * #665: the messages queued while a turn runs, kept in this browser so a reload or leaving the page does not lose
 * them. A deleted session takes its queued messages with it, and a logout clears them all. Storage may be missing or
 * refuse writes (private windows); the queue then lives for the page only.
 */
const KEY = 'plinth.queuedTurns';

function read(): QueueBySession {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(KEY) ?? '{}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed as Record<string, unknown>).filter(
      (entry): entry is [string, QueueBySession[string]] => Array.isArray(entry[1]) && entry[1].length > 0,
    ));
  } catch {
    return {};
  }
}

function write(queue: QueueBySession): void {
  try {
    if (Object.keys(queue).length === 0) localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, JSON.stringify(queue));
  } catch { /* no storage: the queue lives for this page */ }
}

interface SendQueueState {
  queue: QueueBySession;
  update: (change: (queue: QueueBySession) => QueueBySession) => void;
  forgetSession: (sessionId: string) => void;
  clear: () => void;
}

export const useSendQueueStore = create<SendQueueState>((set, get) => ({
  queue: read(),
  update: (change) => {
    const next = change(get().queue);
    if (next === get().queue) return;
    set({ queue: next });
    write(next);
  },
  forgetSession: (sessionId) => get().update((queue) => {
    if (!(sessionId in queue)) return queue;
    const { [sessionId]: _removed, ...rest } = queue;
    return rest;
  }),
  clear: () => {
    set({ queue: {} });
    write({});
  },
}));

/** Reads the stored queue again (a new page load, or a test). */
export function reloadSendQueue(): void {
  useSendQueueStore.setState({ queue: read() });
}
