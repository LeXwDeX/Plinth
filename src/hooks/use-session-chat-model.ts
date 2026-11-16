'use client';

import { useCallback, useEffect, useMemo, useRef } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useSessionTurnRunning } from '@/hooks/use-session-context';
import { useSessionReadRetry } from '@/hooks/use-session-read-retry';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { chatModelReadFailed, loadSessionChatModel, switchSessionChatModel, useSessionContextStore } from '@/store/session-context-store';

/**
 * #322: the session's chat model and its switch. The choice is stored per session on the server, so another
 * device reads the same one; the offered models come from the administrator's LLM settings and are read
 * again when the session opens and when one of its turns ends. `supported` is false when the Schema declares
 * no route (an older server).
 * #314: a failed read is read again (bounded, with backoff; also when the window regains focus or the network comes
 * back). `failed` is true while the read failed and no model list is held, so the picker can say so; `retry` reads
 * again now.
 */
export function useSessionChatModel(sessionId: string | null) {
  const { schema } = useProductSchema();
  const supported = Boolean(schema.runtime.http.routes.chat_session_model);
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const choice = useSessionContextStore((state) => (sessionId ? state.chatModel[sessionId] : undefined));
  const busy = useSessionContextStore((state) => Boolean(sessionId && state.chatModelBusy[sessionId]));
  const readFailed = useSessionContextStore((state) => Boolean(sessionId && state.chatModelFailed[sessionId]));
  const turnRunning = useSessionTurnRunning(sessionId);
  const { retry } = useSessionReadRetry({ client, sessionId, supported, read: loadSessionChatModel, failed: chatModelReadFailed });

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

  return { supported, choice: choice ?? null, busy, failed: supported && readFailed && !choice, retry, setModel };
}
