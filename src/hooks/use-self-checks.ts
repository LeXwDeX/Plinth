'use client';

import { useEffect, useMemo, useRef } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import type { SelfCheckRecord } from '@/lib/chat/self-check';

const NO_RECORDS: SelfCheckRecord[] = [];

/**
 * #246: the session's persisted 3D self-check records. Read from the server so
 * a reload, another device and a later turn show the same delivery state.
 *
 * Every reply of a session subscribes to one cache entry, not just the reply
 * that shows a model card: a later turn's inspect/reference calls and
 * record_self_check write records for an earlier turn's result, and its
 * settle is the event that reads them. Re-reading only when a reply's own
 * key changes would leave that earlier result's card and badge at their first
 * (empty) answer until a reload. `settled` is this reply's streaming state;
 * its first false→true flip revalidates the shared entry for every subscriber.
 */
export function useSelfChecks(sessionId: string | null | undefined, settled = false) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const enabled = Boolean(sessionId) && Boolean(schema.runtime.http.routes.chat_session_self_checks);
  const { data, error, isLoading, mutate } = useSWR<SelfCheckRecord[]>(
    enabled ? ['chat-self-checks', sessionId] : null,
    () => client.getSelfChecks(sessionId as string),
    { revalidateOnFocus: false, keepPreviousData: true },
  );
  const previousSettled = useRef(settled);
  useEffect(() => {
    const wasSettled = previousSettled.current;
    previousSettled.current = settled;
    if (enabled && settled && !wasSettled) void mutate();
  }, [enabled, settled, mutate]);
  return {
    records: enabled && Array.isArray(data) ? data : NO_RECORDS,
    failed: Boolean(error),
    loading: isLoading,
    refresh: mutate,
  };
}