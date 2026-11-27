import type { ChatBrowserClient } from './browser-client';

/**
 * #665: the toast id of an error the stream reports. The same error replayed when the stream is joined again (its
 * reply message announced again first) updates the toast already shown; the same words in another session, or in
 * another turn's reply, are their own toast. Before the reply message is known, errors of the session share one.
 */
export function chatErrorToastId(sessionId: string, replyMessageId: string, content: string): string {
  return `chat-error:${sessionId}:${replyMessageId}:${content}`;
}

/**
 * #665: asks the server to stop the session's turn (with its running generation). Only a stop the server took ends
 * the local stream; a refused one is said, and the turn is still followed because it still runs.
 */
export function requestTurnStop(
  client: Pick<ChatBrowserClient, 'cancelTurn'>,
  sessionId: string,
  handlers: { onStopped(): void; onRefused(): void },
): Promise<void> {
  return client.cancelTurn(sessionId).then(() => handlers.onStopped(), () => handlers.onRefused());
}

/**
 * #665: what to do with a turn a stage action asks for. Without an open session it is dropped; while attachments are
 * still uploading it waits (and is said to wait) instead of being refused, and goes out when they finish.
 */
export function stageRequestStep(input: { hasSession: boolean; uploadsPending: boolean }): 'drop' | 'wait' | 'send' {
  if (!input.hasSession) return 'drop';
  return input.uploadsPending ? 'wait' : 'send';
}
