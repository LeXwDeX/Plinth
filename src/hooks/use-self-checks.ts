'use client';

import { useMemo } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import type { SelfCheckRecord } from '@/lib/chat/self-check';

const NO_RECORDS: SelfCheckRecord[] = [];

/**
 * #246: the session's persisted 3D self-check records. Read from the server so
 * a reload, another device and a later turn show the same delivery state.
 * `refreshKey` changes when a reply settles, which reads the records once the
 * turn's tools have written them.
 */
export function useSelfChecks(sessionId: string | null | undefined, refreshKey = '', wanted = true) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const enabled = wanted && Boolean(sessionId) && Boolean(schema.runtime.http.routes.chat_session_self_checks);
  const { data, error, isLoading, mutate } = useSWR<SelfCheckRecord[]>(
    enabled ? ['chat-self-checks', sessionId, refreshKey] : null,
    () => client.getSelfChecks(sessionId as string),
    { revalidateOnFocus: false, keepPreviousData: true },
  );
  return {
    records: enabled && Array.isArray(data) ? data : NO_RECORDS,
    failed: Boolean(error),
    loading: isLoading,
    refresh: mutate,
  };
}