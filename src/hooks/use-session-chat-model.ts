'use client';

import { useCallback, useEffect, useMemo, useRef } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useSessionTurnRunning } from '@/hooks/use-session-context';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { loadSessionChatModel, switchSessionChatModel, useSessionContextStore } from '@/store/session-context-store';

/**
 * #322: the session's chat model and its switch. The choice is stored per session on the server, so another
 * device reads the same one; the offered models come from the administrator's LLM settings and are read
 * again when the session opens and when one of its turns ends. `supported` is false when the Schema declares
 * no route (an older server).
 */
export function useSessionChatModel(sessionId: string | null) {
  const { schema } = useProductSchema();
  const supported = Boolean(schema.runtime.http.routes.chat_session_model);
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const choice = useSessionContextStore((state) => (sessionId ? state.chatModel[sessionId] : undefined));
  const busy = useSessionContextStore((state) => Boolean(sessionId && state.chatModelBusy[sessionId]));
  const turnRunning = useSessionTurnRunning(sessionId);

  useEffect(() => {
    if (!supported || !sessionId) return;
    const controller = new AbortController();
    loadSessionChatModel(client, sessionId, controller.signal).catch(() => undefined);
    return () => controller.abort();
  }, [client, sessionId, supported]);

  const observed = useRef({ sessionId, turnRunning });
  useEffect(() => {
    const previous = observed.current;
    observed.current = { sessionId, turnRunning };
    if (!supported || !sessionId || previous.sessionId !== sessionId || !previous.turnRunning || turnRunning) return;
    loadSessionChatModel(client, sessionId).catch(() => undefined);
  }, [client, sessionId, supported, turnRunning]);

  const setModel = useCallback(async (model: string): Promise<boolean> => {
    if (!supported || !sessionId) return false;
    return switchSessionChatModel(client, sessionId, model);
  }, [client, sessionId, supported]);

  return { supported, choice: choice ?? null, busy, setModel };
}
