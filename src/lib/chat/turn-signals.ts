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

/**
 * #691: sends a stage request the chat column took (step `drop` or `send`). The request stays pending without a gap
 * from the moment it was asked for until its send is over: `begin` runs before `clear`, and `end` runs however the
 * send ends, a turn that ran or a send that returned without one (no Agent, a model no longer offered). A dropped
 * request is cleared and sends nothing. The turn buttons that asked for it read this state, never a flag of their own.
 */
export function sendStageRequest(
  step: 'drop' | 'send',
  handlers: { clear(): void; begin(): void; end(): void; send(): Promise<void> },
): Promise<void> | null {
  if (step === 'drop') {
    handlers.clear();
    return null;
  }
  handlers.begin();
  handlers.clear();
  return handlers.send().finally(() => handlers.end());
}
