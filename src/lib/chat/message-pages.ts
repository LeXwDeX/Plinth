import { keepUnchanged } from '@/lib/core/keep-unchanged';

/** What the chat column keeps of one conversation: the messages read so far and where earlier ones start. */
export interface LoadedConversation<Message extends { id: string }> {
  messages: Message[];
  /** Cursor of the messages before `messages`; '' when the conversation is read to its start. */
  olderCursor: string;
}

/**
 * Puts a fresh newest page in place of the end of what is loaded. Earlier pages the person already
 * read stay in front as long as the fresh page joins onto them (its first message is one of them);
 * otherwise, or when the page already reaches the start, the page alone is the conversation.
 */
export function mergeNewestPage<Message extends { id: string }>(
  loaded: LoadedConversation<Message> | undefined,
  page: { messages: Message[]; nextCursor: string },
): LoadedConversation<Message> {
  // A message read again unchanged keeps its object, so the chat list redraws only what changed (#291).
  const kept = keepUnchanged(loaded?.messages, page.messages, (message) => message.id);
  const fresh = { messages: kept, olderCursor: page.nextCursor };
  if (!loaded || page.nextCursor === '' || page.messages.length === 0) return fresh;
  const joint = loaded.messages.findIndex((message) => message.id === page.messages[0].id);
  if (joint <= 0) return fresh;
  return { messages: [...loaded.messages.slice(0, joint), ...kept], olderCursor: loaded.olderCursor };
}

/** Adds an earlier page in front of what is loaded; a message already shown is not repeated. */
export function prependOlderPage<Message extends { id: string }>(
  loaded: LoadedConversation<Message>,
  page: { messages: Message[]; nextCursor: string },
): LoadedConversation<Message> {
  const shown = new Set(loaded.messages.map((message) => message.id));
  const older = page.messages.filter((message) => !shown.has(message.id));
  return { messages: [...older, ...loaded.messages], olderCursor: page.nextCursor };
}

