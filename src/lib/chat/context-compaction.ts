import type { ChatContextCompaction } from '@/lib/chat/browser-client';

export interface CompactionPlacement {
  /** Dividers shown right after a message, by message id, oldest first. */
  byMessage: Map<string, ChatContextCompaction[]>;
  /** Dividers before the first shown message (only when the whole history is loaded). */
  leading: ChatContextCompaction[];
}

/**
 * #322: where each compaction divider sits in the conversation: after the last shown message made no
 * later than the compaction. One older than every shown message belongs to history not loaded yet and
 * is left out until that page is read (`hasOlder`). One whose time cannot be read goes last. The
 * dividers are display records only; they are never messages and never sent.
 */
export function compactionPlacement(
  compactions: readonly ChatContextCompaction[],
  messages: readonly { id: string; createdAt: number }[],
  hasOlder: boolean,
): CompactionPlacement {
  const byMessage = new Map<string, ChatContextCompaction[]>();
  const leading: ChatContextCompaction[] = [];
  if (messages.length === 0) return { byMessage, leading };
  const dated = compactions
    .map((item) => ({ item, time: Date.parse(item.createdAt) }))
    .map((entry) => ({ ...entry, time: Number.isFinite(entry.time) ? entry.time : Number.POSITIVE_INFINITY }))
    .sort((left, right) => left.time - right.time);
  for (const { item, time } of dated) {
    let anchor: string | null = null;
    for (const message of messages) {
      if (message.createdAt <= time) anchor = message.id;
    }
    if (anchor === null) {
      if (!hasOlder) leading.push(item);
      continue;
    }
    byMessage.set(anchor, [...(byMessage.get(anchor) ?? []), item]);
  }
  return { byMessage, leading };
}
