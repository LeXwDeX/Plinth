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
  /**
   * #314: the latest read of the session's thinking level failed (cleared by the next one that succeeds). It does not
   * remove a level already held; the picker says so only while it holds none, and a returning window reads again.
   */
  reasoningFailed: Record<string, boolean>;
  context: Record<string, ChatContextUsage>;
  compacting: Record<string, boolean>;
  /** The session's chat model and the models offered now. */
  chatModel: Record<string, ChatModelChoice>;
  chatModelBusy: Record<string, boolean>;
  /** #314: the same for the session's chat model. */
  chatModelFailed: Record<string, boolean>;
}

export const useSessionContextStore = create<SessionContextStore>(() => ({
  reasoning: {},
  reasoningBusy: {},
  reasoningFailed: {},
  context: {},
  compacting: {},
  chatModel: {},
  chatModelBusy: {},
  chatModelFailed: {},
}));

function put<K extends keyof SessionContextStore>(key: K, sessionId: string, value: SessionContextStore[K][string]): void {
  useSessionContextStore.setState((state) => ({ [key]: { ...state[key], [sessionId]: value } }) as Partial<SessionContextStore>);
}

function drop<K extends 'reasoningBusy' | 'reasoningFailed' | 'compacting' | 'chatModelBusy' | 'chatModelFailed'>(key: K, sessionId: string): void {
  // Nothing to remove: leave the store (and everyone subscribed to it) alone, as every successful read ends here.
  if (!(sessionId in useSessionContextStore.getState()[key])) return;
  useSessionContextStore.setState((state) => {
    const { [sessionId]: _, ...rest } = state[key];
    return { [key]: rest } as Partial<SessionContextStore>;
  });
}

/** Whether the latest read of the session's thinking level failed (the page reads again when its window regains focus). */
export function reasoningReadFailed(sessionId: string): boolean {
  return Boolean(useSessionContextStore.getState().reasoningFailed[sessionId]);
}

/** Whether the latest read of the session's chat model failed. */
export function chatModelReadFailed(sessionId: string): boolean {
  return Boolean(useSessionContextStore.getState().chatModelFailed[sessionId]);
}

/**
 * Reads the session's thinking level. A failure is handed on to the caller (which may read again) and remembered; a
 * read the caller cancelled says nothing about the server and leaves everything as it was, even when its answer was
 * already on the way.
 */
export async function loadSessionReasoning(client: ChatBrowserClient, sessionId: string, signal?: AbortSignal): Promise<void> {
  let value: ChatReasoning;
  try {
    value = await client.getReasoning(sessionId, signal);
  } catch (error) {
    if (!signal?.aborted) put('reasoningFailed', sessionId, true);
    throw error;
  }
  if (signal?.aborted) return;
  drop('reasoningFailed', sessionId);
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

/** Reads the session's chat model and the models offered; failures and cancelled reads are handled as in `loadSessionReasoning`. */
export async function loadSessionChatModel(client: ChatBrowserClient, sessionId: string, signal?: AbortSignal): Promise<void> {
  let value: ChatModelChoice;
  try {
    value = await client.getChatModel(sessionId, signal);
  } catch (error) {
    if (!signal?.aborted) put('chatModelFailed', sessionId, true);
    throw error;
  }
  if (signal?.aborted) return;
  drop('chatModelFailed', sessionId);
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
