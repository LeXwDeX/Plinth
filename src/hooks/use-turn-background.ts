'use client';

import { useEffect, useState } from 'react';

import type { ChatBrowserClient } from '@/lib/chat/browser-client';
import { startSerialPoller } from '@/lib/workspace/serial-poller';

/**
 * Whether the composer asks the server about "stop waiting, keep the task running" (#137): only while
 * the session's turn streams, a Task of the session runs and the Schema declares the route, its member
 * and a poll interval. The rules themselves (an eligible paid task queued upstream or past the
 * Schema threshold; a SkillRun's task only when it waits in a batch, never a Processing Plan) stay
 * on the server.
 */
export function asksTurnBackground(input: {
  sessionId: string | null;
  streaming: boolean;
  taskRunning: boolean;
  route: string | undefined;
  member: string | undefined;
  intervalMilliseconds: number | undefined;
}): boolean {
  return Boolean(input.sessionId && input.streaming && input.taskRunning && input.route && input.member &&
    input.intervalMilliseconds && input.intervalMilliseconds > 0);
}

/**
 * Polls the server's answer every `intervalMilliseconds` while `asks` holds; false otherwise, before
 * the first answer and after a failed read. An answer only counts for the session it was asked for.
 */
export function useTurnBackground(
  client: Pick<ChatBrowserClient, 'turnBackgroundAvailable'>,
  sessionId: string | null,
  asks: boolean,
  intervalMilliseconds: number | undefined,
): boolean {
  const [answer, setAnswer] = useState<{ sessionId: string; available: boolean } | null>(null);
  useEffect(() => {
    if (!asks || !sessionId || !intervalMilliseconds) return;
    setAnswer(null);
    return startSerialPoller(async (signal) => {
      const available = await client.turnBackgroundAvailable(sessionId, signal);
      if (!signal.aborted) setAnswer({ sessionId, available });
    }, intervalMilliseconds);
  }, [asks, client, intervalMilliseconds, sessionId]);
  return asks && answer !== null && answer.sessionId === sessionId && answer.available;
}
