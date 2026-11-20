import type { ChatMessage } from '@/store/chat-store';

/**
 * #403: whether the turn of a session still runs, read from what the message projection already says: the server
 * marks the reply of a turn that has not ended as pending, and a stream buffer means this browser follows one. No
 * list of turn states is kept in the browser. A session whose messages have not been read yet (`undefined`) counts as
 * running: nothing may be called finished, or not run, before it is known.
 */
export function sessionTurnRunning(messages: readonly ChatMessage[] | undefined, streaming: boolean): boolean {
  if (streaming || messages === undefined) return true;
  return messages.some((message) => message.isPending === true);
}

/**
 * #403: the sent words a stream buffer shows until the server has stored them. A stream joined after a reload (the
 * session recovery) has none of its own, because the stored user message is already in the list, so an empty one is
 * not shown as a blank message.
 */
export function hasSentWords(message: { content: string; images?: readonly unknown[] } | null | undefined): boolean {
  return message != null && (message.content !== '' || (message.images?.length ?? 0) > 0);
}
