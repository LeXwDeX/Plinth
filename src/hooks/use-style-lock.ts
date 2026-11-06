'use client';

import { useCallback, useEffect, useState } from 'react';

import type { ChatBrowserClient, ChatStyleLock } from '@/lib/chat/browser-client';

export const EMPTY_STYLE_LOCK: ChatStyleLock = { enabled: false, text: '', references: [] };

function copyLock(lock: ChatStyleLock): ChatStyleLock {
  return { enabled: lock.enabled === true, text: typeof lock.text === 'string' ? lock.text : '', references: Array.isArray(lock.references) ? [...lock.references] : [] };
}

/**
 * Loads and saves the owner-scoped style lock. The session key prevents a late
 * response from one session from replacing the next session's local state.
 */
export function useStyleLock(client: ChatBrowserClient, sessionId: string | null) {
  const [state, setState] = useState<{ sessionId: string | null; lock: ChatStyleLock }>({ sessionId: null, lock: EMPTY_STYLE_LOCK });
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const [saveError, setSaveError] = useState<Error | null>(null);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (!sessionId) return;
    try {
      const lock = await client.getStyleLock(sessionId, signal);
      if (signal?.aborted) return;
      setState({ sessionId, lock: copyLock(lock) });
      setFailed(false);
    } catch {
      if (!signal?.aborted) setFailed(true);
    }
  }, [client, sessionId]);

  useEffect(() => {
    if (!sessionId) return;
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [refresh, sessionId]);

  const save = useCallback(async (lock: ChatStyleLock) => {
    if (!sessionId) return false;
    setBusy(true);
    setSaveError(null);
    try {
      const stored = await client.setStyleLock(sessionId, lock);
      setState({ sessionId, lock: copyLock(stored ?? lock) });
      return true;
    } catch (error) {
      setSaveError(error instanceof Error ? error : new Error(String(error)));
      return false;
    } finally {
      setBusy(false);
    }
  }, [client, sessionId]);

  return {
    lock: state.sessionId === sessionId ? state.lock : EMPTY_STYLE_LOCK,
    busy,
    failed,
    saveError,
    refresh,
    save,
  };
}
