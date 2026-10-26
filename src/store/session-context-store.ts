import { create } from 'zustand';

import type { ChatBrowserClient, ChatContextUsage, ChatModelChoice, ChatReasoning } from '@/lib/chat/browser-client';

/**
 * #322: per-session thinking level and context usage, read from the server. Kept by session id, so
 * switching sessions never shows another session's numbers; both are stored server side, so a reload
 * reads them back.
 */
interface SessionContextStore {
  reasoning: Record<string, ChatReasoning>;
  reasoningBusy: Record<string, boolean>;
  context: Record<string, ChatContextUsage>;
  compacting: Record<string, boolean>;
  /** The session's chat model and the models offered now. */
  chatModel: Record<string, ChatModelChoice>;
  chatModelBusy: Record<string, boolean>;
}

export const useSessionContextStore = create<SessionContextStore>(() => ({
  reasoning: {},
  reasoningBusy: {},
  context: {},
  compacting: {},
  chatModel: {},
  chatModelBusy: {},
}));

function put<K extends keyof SessionContextStore>(key: K, sessionId: string, value: SessionContextStore[K][string]): void {
  useSessionContextStore.setState((state) => ({ [key]: { ...state[key], [sessionId]: value } }) as Partial<SessionContextStore>);
}

function drop<K extends 'reasoningBusy' | 'compacting' | 'chatModelBusy'>(key: K, sessionId: string): void {
  useSessionContextStore.setState((state) => {
    const { [sessionId]: _, ...rest } = state[key];
    return { [key]: rest } as Partial<SessionContextStore>;
  });
}

export async function loadSessionReasoning(client: ChatBrowserClient, sessionId: string, signal?: AbortSignal): Promise<void> {
  const value = await client.getReasoning(sessionId, signal);
  // A switch made meanwhile wins over the read it raced with.
  if (useSessionContextStore.getState().reasoningBusy[sessionId]) return;
  put('reasoning', sessionId, value);
}

/** Shows the new level at once; puts the previous one back when the server refuses. */
export async function switchSessionReasoning(client: ChatBrowserClient, sessionId: string, level: string): Promise<boolean> {
  const previous = useSessionContextStore.getState().reasoning[sessionId];
  if (previous) put('reasoning', sessionId, { ...previous, effort: level });
  put('reasoningBusy', sessionId, true);
  try {
    put('reasoning', sessionId, await client.setReasoning(sessionId, level));
    return true;
  } catch {
    if (previous) put('reasoning', sessionId, previous);
    return false;
  } finally {
    drop('reasoningBusy', sessionId);
  }
}

export async function loadSessionContext(client: ChatBrowserClient, sessionId: string, signal?: AbortSignal): Promise<void> {
  const value = await client.getContext(sessionId, signal);
  if (useSessionContextStore.getState().compacting[sessionId]) return;
  put('context', sessionId, value);
}

export type CompactionResult = { ok: true } | { ok: false; message: string; cause?: unknown };

/** Compacts now; the chat history stays as it is, only the context the model sees changes. */
export async function compactSessionContext(client: ChatBrowserClient, sessionId: string): Promise<CompactionResult> {
  if (useSessionContextStore.getState().compacting[sessionId]) return { ok: false, message: '' };
  put('compacting', sessionId, true);
  try {
    put('context', sessionId, await client.compactContext(sessionId));
    return { ok: true };
  } catch (cause) {
    return { ok: false, message: cause instanceof Error ? cause.message : '', cause };
  } finally {
    drop('compacting', sessionId);
  }
}

export async function loadSessionChatModel(client: ChatBrowserClient, sessionId: string, signal?: AbortSignal): Promise<void> {
  const value = await client.getChatModel(sessionId, signal);
  if (useSessionContextStore.getState().chatModelBusy[sessionId]) return;
  put('chatModel', sessionId, value);
}

/**
 * Switches the session's chat model, then reads its thinking level and context window again: both follow
 * the model (the level falls back to what the new model offers). The previous model stays on a refusal.
 */
export async function switchSessionChatModel(client: ChatBrowserClient, sessionId: string, model: string): Promise<boolean> {
  if (useSessionContextStore.getState().chatModelBusy[sessionId]) return false;
  put('chatModelBusy', sessionId, true);
  try {
    put('chatModel', sessionId, await client.setChatModel(sessionId, model));
  } catch {
    return false;
  } finally {
    drop('chatModelBusy', sessionId);
  }
  await Promise.all([
    loadSessionReasoning(client, sessionId).catch(() => undefined),
    loadSessionContext(client, sessionId).catch(() => undefined),
  ]);
  return true;
}
