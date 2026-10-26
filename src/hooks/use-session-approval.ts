'use client';

import { useCallback, useEffect, useState } from 'react';

import type { ChatBrowserClient } from '@/lib/chat/browser-client';
import { useChatStore, type ApprovalMode } from '@/store/chat-store';

/**
 * 当前会话的批准方式与累计估算积分。数据存在聊天 store 的会话上，切换会话时
 * 按会话 ID 取回，避免把一个会话的方式显示在另一个会话上。
 */
export function useSessionApproval(
  client: ChatBrowserClient,
  sessionId: string | null,
  defaultMode: ApprovalMode,
) {
  const session = useChatStore((state) => state.sessions.find((item) => item.id === sessionId));
  const updateSessionApproval = useChatStore((state) => state.updateSessionApproval);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (!sessionId) return;
    try {
      const approval = await client.getApproval(sessionId, signal);
      updateSessionApproval(sessionId, approval);
      setFailed(false);
    } catch {
      if (!signal?.aborted) setFailed(true);
    }
  }, [client, sessionId, updateSessionApproval]);

  useEffect(() => {
    if (!sessionId) return;
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [sessionId, refresh]);

  const setMode = useCallback(async (mode: ApprovalMode) => {
    if (!sessionId) return false;
    setBusy(true);
    try {
      const approval = await client.setApprovalMode(sessionId, mode);
      updateSessionApproval(sessionId, approval);
      return true;
    } catch {
      return false;
    } finally {
      setBusy(false);
    }
  }, [client, sessionId, updateSessionApproval]);

  return {
    mode: session?.approvalMode ?? defaultMode,
    credits: session?.estimatedCredits ?? 0,
    priceTotals: session?.priceTotals,
    busy,
    failed,
    refresh,
    setMode,
  };
}
