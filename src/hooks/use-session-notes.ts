'use client';

import { useCallback, useEffect, useState } from 'react';

import type { ChatBrowserClient } from '@/lib/chat/browser-client';

/**
 * 当前会话的设定摘要。文本按会话 ID 记住：切换会话后不会把上一个会话的摘要
 * 显示（或保存）到新会话上。Agent 在一轮里更新摘要后，调用方用 `refresh` 取回。
 */
export function useSessionNotes(client: ChatBrowserClient, sessionId: string | null) {
  const [state, setState] = useState<{ sessionId: string | null; notes: string }>({ sessionId: null, notes: '' });
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (!sessionId) return;
    try {
      const notes = await client.getNotes(sessionId, signal);
      if (signal?.aborted) return;
      setState({ sessionId, notes });
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
  }, [sessionId, refresh]);

  const save = useCallback(async (notes: string) => {
    if (!sessionId) return false;
    setBusy(true);
    try {
      const stored = await client.setNotes(sessionId, notes);
      setState({ sessionId, notes: stored });
      return true;
    } catch {
      return false;
    } finally {
      setBusy(false);
    }
  }, [client, sessionId]);

  return {
    notes: state.sessionId === sessionId ? state.notes : '',
    busy,
    failed,
    refresh,
    save,
  };
}
