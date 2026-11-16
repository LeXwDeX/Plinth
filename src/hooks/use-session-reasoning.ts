'use client';

import { useCallback, useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useSessionReadRetry } from '@/hooks/use-session-read-retry';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { loadSessionReasoning, reasoningReadFailed, switchSessionReasoning, useSessionContextStore } from '@/store/session-context-store';

/**
 * #322: the session's thinking level and its switch. The level is stored per session on the server; a
 * switch shows at once and goes back when the server refuses. It applies to later messages only.
 * `supported` is false when the Schema declares no route (an older server): the picker is then hidden.
 * #314: a failed read is read again (bounded, with backoff; also when the window regains focus or the network comes
 * back). `failed` is true while the read failed and no level is held, so the picker can say so; `retry` reads again now.
 */
export function useSessionReasoning(sessionId: string | null) {
  const { schema } = useProductSchema();
  const supported = Boolean(schema.runtime.http.routes.chat_session_reasoning);
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const reasoning = useSessionContextStore((state) => (sessionId ? state.reasoning[sessionId] : undefined));
  const busy = useSessionContextStore((state) => Boolean(sessionId && state.reasoningBusy[sessionId]));
  const readFailed = useSessionContextStore((state) => Boolean(sessionId && state.reasoningFailed[sessionId]));
  const { retry } = useSessionReadRetry({ client, sessionId, supported, read: loadSessionReasoning, failed: reasoningReadFailed });

  const setLevel = useCallback(async (level: string): Promise<boolean> => {
    if (!supported || !sessionId) return false;
    return switchSessionReasoning(client, sessionId, level);
  }, [client, sessionId, supported]);

  return { supported, reasoning: reasoning ?? null, busy, failed: supported && readFailed && !reasoning, retry, setLevel };
}
