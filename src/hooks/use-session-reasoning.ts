'use client';

import { useCallback, useEffect, useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { loadSessionReasoning, switchSessionReasoning, useSessionContextStore } from '@/store/session-context-store';

/**
 * #322: the session's thinking level and its switch. The level is stored per session on the server; a
 * switch shows at once and goes back when the server refuses. It applies to later messages only.
 * `supported` is false when the Schema declares no route (an older server): the picker is then hidden.
 */
export function useSessionReasoning(sessionId: string | null) {
  const { schema } = useProductSchema();
  const supported = Boolean(schema.runtime.http.routes.chat_session_reasoning);
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const reasoning = useSessionContextStore((state) => (sessionId ? state.reasoning[sessionId] : undefined));
  const busy = useSessionContextStore((state) => Boolean(sessionId && state.reasoningBusy[sessionId]));

  useEffect(() => {
    if (!supported || !sessionId) return;
    const controller = new AbortController();
    loadSessionReasoning(client, sessionId, controller.signal).catch(() => undefined);
    return () => controller.abort();
  }, [client, sessionId, supported]);

  const setLevel = useCallback(async (level: string): Promise<boolean> => {
    if (!supported || !sessionId) return false;
    return switchSessionReasoning(client, sessionId, level);
  }, [client, sessionId, supported]);

  return { supported, reasoning: reasoning ?? null, busy, setLevel };
}
