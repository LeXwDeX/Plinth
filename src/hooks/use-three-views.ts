'use client';

import { useCallback, useMemo, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { groupsAreActive, slotAction, type ThreeViewGroup, type ThreeViewSlot } from '@/lib/workspace/three-view';
import { useChatStore } from '@/store/chat-store';

const NONE: ThreeViewGroup[] = [];

/**
 * The session's three-view groups. They are read from the server's projection and
 * read again every few seconds while a view is in flight, so a started view moves
 * from "starting" to "running" to a picture without a reload. Starting or retrying
 * one view only asks the server; what the group looks like comes back from it.
 */
export function useThreeViews() {
  const { schema } = useProductSchema();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const sessionId = useChatStore((state) => state.activeSessionId);
  const enabled = Boolean(sessionId && schema.runtime.http.routes.chat_session_three_views);
  const { data, mutate } = useSWR(
    enabled ? ['session-three-views', sessionId] as const : null,
    ([, id]) => chat.listThreeViews(id as string),
    {
      revalidateOnFocus: false,
      keepPreviousData: false,
      refreshInterval: (latest) => (groupsAreActive(latest) ? schema.chat.frontend.fallback_poll_milliseconds : 0),
    },
  );
  const [busy, setBusy] = useState<string | null>(null);

  /** Returns an error message, or null when the server took the request. */
  const advance = useCallback(async (group: ThreeViewGroup, slot: ThreeViewSlot): Promise<string | null> => {
    const action = slotAction(slot);
    if (!sessionId || !action) return null;
    const busyKey = `${group.runId}:${slot.key}`;
    setBusy(busyKey);
    try {
      const next = await chat.advanceThreeView(sessionId, group.runId, slot.key, action, slot.attempt);
      await mutate((current) => (current ?? NONE).map((item) => (item.runId === next.runId ? next : item)), { revalidate: false });
      return null;
    } catch (error) {
      void mutate();
      return error instanceof Error ? error.message : schema.chat.presentation.messages.internal_error;
    } finally {
      setBusy((current) => (current === busyKey ? null : current));
    }
  }, [chat, mutate, schema, sessionId]);

  return { groups: data ?? NONE, busy, advance, refresh: () => mutate() };
}
