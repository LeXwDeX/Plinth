'use client';

import { useCallback, useState } from 'react';
import useSWR, { useSWRConfig } from 'swr';

import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import type { ChatBrowserClient } from '@/lib/chat/browser-client';
import type { ChatAttachment } from '@/store/chat-store';

const pickKey = (sessionId: string | null) => (sessionId ? ['session-pick', sessionId] as const : null);

/**
 * 当前会话选用的候选图。读取按会话 ID 缓存并在对话列和版本条之间共享：任何一处
 * 选用，另一处立即一致；切换会话后不会把上一个会话的选用显示在新会话上；写入
 * 失败时保持服务端的原状。
 */
export function useSessionPick(client: ChatBrowserClient, sessionId: string | null) {
  const { mutate } = useSWRConfig();
  const [busy, setBusy] = useState(false);
  const { data } = useSWR<ChatAttachment | null>(
    pickKey(sessionId),
    ([, id]: readonly [string, string]) => client.getPick(id).catch(() => null),
    { revalidateOnFocus: false },
  );

  const choose = useCallback(async (claim: ChatAttachmentClaim) => {
    if (!sessionId) return false;
    setBusy(true);
    try {
      const picked = await client.setPick(sessionId, claim);
      await mutate(pickKey(sessionId), picked, { revalidate: false });
      return true;
    } catch {
      return false;
    } finally {
      setBusy(false);
    }
  }, [client, mutate, sessionId]);

  // `loaded` is false until the server has answered, so a caller can wait instead of treating it as no pick.
  return { pick: sessionId ? data ?? null : null, loaded: data !== undefined, busy, choose };
}
