'use client';

import { useCallback, useEffect, useMemo, useRef } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { useChatStore } from '@/store/chat-store';
import { compactSessionContext, loadSessionContext, useSessionContextStore, type CompactionResult } from '@/store/session-context-store';

/** True while a turn of the session runs: its streaming buffer exists from the send until the turn ends. */
export function useSessionTurnRunning(sessionId: string | null): boolean {
  return useChatStore((state) => Boolean(sessionId && state.streamingBuffers?.[sessionId]));
}

/**
 * #322: the session's context usage and compaction records. Read when the session opens, again when a
 * turn of it ends (its streaming buffer goes away: the reply ended, failed or was stopped), and taken
 * from the compaction's own answer after a compaction. `supported` is false when the Schema declares no
 * context route or no compaction settings (an older server): the meter is then hidden.
 */
export function useSessionContext(sessionId: string | null) {
  const { schema } = useProductSchema();
  const settings = schema.chat.context_compaction;
  const supported = Boolean(schema.runtime.http.routes.chat_session_context && settings);
  const canCompact = Boolean(supported && settings?.enabled && schema.runtime.http.routes.chat_session_context_compaction);
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const context = useSessionContextStore((state) => (sessionId ? state.context[sessionId] : undefined));
  const compacting = useSessionContextStore((state) => Boolean(sessionId && state.compacting[sessionId]));
  const turnRunning = useSessionTurnRunning(sessionId);

  useEffect(() => {
    if (!supported || !sessionId) return;
    const controller = new AbortController();
    loadSessionContext(client, sessionId, controller.signal).catch(() => undefined);
    return () => controller.abort();
  }, [client, sessionId, supported]);

  // A finished turn reported new usage (and may have compacted automatically at its start).
  const observed = useRef({ sessionId, turnRunning });
  useEffect(() => {
    const previous = observed.current;
    observed.current = { sessionId, turnRunning };
    if (!supported || !sessionId || previous.sessionId !== sessionId || !previous.turnRunning || turnRunning) return;
    loadSessionContext(client, sessionId).catch(() => undefined);
  }, [client, sessionId, supported, turnRunning]);

  const compact = useCallback(async (): Promise<CompactionResult> => {
    if (!canCompact || !sessionId) return { ok: false, message: '' };
    return compactSessionContext(client, sessionId);
  }, [canCompact, client, sessionId]);

  return { supported, canCompact, context: context ?? null, compacting, turnRunning, compact };
}

/** The compaction records of a session, for the conversation's dividers; read only, never sent. */
export function useSessionCompactions(sessionId: string | null) {
  return useSessionContextStore((state) => (sessionId ? state.context[sessionId]?.compactions : undefined));
}
