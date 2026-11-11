'use client';

import { useCallback } from 'react';
import useSWR, { useSWRConfig } from 'swr';

import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import type { ChatBrowserClient, ChatPreferenceEdit } from '@/lib/chat/browser-client';
import type { ChatPreference, PreferenceSignal } from '@/lib/chat/preferences';
import { preferencesAvailable } from '@/lib/chat/preferences';
import { useProductSchema } from '@/components/product-schema-provider';

const preferencesKey = (sessionId: string | null) =>
  (sessionId ? ['session-preferences', sessionId] as const : null);

/**
 * 会话的偏好记录（#241）。读取按会话 ID 缓存：侧栏、结果卡和发送动作共享同一个列表，
 * 一处写出后其余位置立即一致；服务端没有声明偏好功能时保持空列表且不请求。
 */
export function useSessionPreferences(client: ChatBrowserClient, sessionId: string | null) {
  const { schema } = useProductSchema();
  const { mutate } = useSWRConfig();
  const available = preferencesAvailable(schema);
  const key = available ? preferencesKey(sessionId) : null;
  const { data, error, isLoading } = useSWR<ChatPreference[]>(
    key,
    ([, id]: readonly [string, string]) => client.listPreferences(id).catch(() => []),
    { revalidateOnFocus: false },
  );

  const replace = useCallback((next: ChatPreference[]) => mutate(key, next, { revalidate: false }), [key, mutate]);
  const append = useCallback((created: ChatPreference[]) => {
    void mutate(key, (current: ChatPreference[] | undefined) => {
      const known = new Set((current ?? []).map((preference) => preference.id));
      return [...created.filter((preference) => !known.has(preference.id)), ...(current ?? [])];
    }, { revalidate: false });
  }, [key, mutate]);

  /** Report "not this one"; true when the server stored the record. */
  const reject = useCallback(async (claim: ChatAttachmentClaim, reasonPreset: string, reasonText: string) => {
    if (!key) return false;
    try {
      append(await client.rejectCandidate(sessionId!, claim, reasonPreset, reasonText));
      return true;
    } catch {
      return false;
    }
  }, [append, client, key, sessionId]);

  /** Report a weak redo signal; a failure is ignored: the signal is only a hint. */
  const signal = useCallback(async (kind: PreferenceSignal, claims: readonly ChatAttachmentClaim[]) => {
    if (!key || claims.length === 0) return;
    try {
      append(await client.recordPreferenceSignal(sessionId!, kind, claims));
    } catch {
      // A weak signal must never interrupt the action it describes.
    }
  }, [append, client, key, sessionId]);

  const update = useCallback(async (preferenceID: string, edit: ChatPreferenceEdit) => {
    if (!key) return false;
    try {
      const updated = await client.updatePreference(sessionId!, preferenceID, edit);
      void mutate(key, (current: ChatPreference[] | undefined) =>
        (current ?? []).map((preference) => preference.id === updated.id ? updated : preference),
      { revalidate: false });
      return true;
    } catch {
      return false;
    }
  }, [client, key, mutate, sessionId]);

  const remove = useCallback(async (preferenceID: string) => {
    if (!key) return false;
    try {
      await client.deletePreference(sessionId!, preferenceID);
      void mutate(key, (current: ChatPreference[] | undefined) =>
        (current ?? []).filter((preference) => preference.id !== preferenceID),
      { revalidate: false });
      return true;
    } catch {
      return false;
    }
  }, [client, key, mutate, sessionId]);

  return {
    available,
    preferences: sessionId ? data ?? [] : [],
    loaded: data !== undefined,
    failed: Boolean(error),
    reject,
    signal,
    update,
    remove,
  };
}