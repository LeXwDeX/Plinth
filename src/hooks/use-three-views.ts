'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { advanceWithQueue } from '@/lib/workspace/three-view-queue';
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
  /** `${runId}:${slotKey}` of views waiting for a free admission slot; they resubmit on their own. */
  const [queued, setQueued] = useState<ReadonlySet<string>>(() => new Set());
  const aborter = useRef<AbortController | null>(null);

  // 会话切换或卸载时中止所有排队等待，定时器随之清掉。
  useEffect(() => {
    const controller = new AbortController();
    aborter.current = controller;
    return () => {
      controller.abort();
      setQueued(new Set());
    };
  }, [sessionId]);

  /** Returns an error message, or null when the server took the request. */
  const advance = useCallback(async (group: ThreeViewGroup, slot: ThreeViewSlot): Promise<string | null> => {
    const action = slotAction(slot);
    if (!sessionId || !action) return null;
    const busyKey = `${group.runId}:${slot.key}`;
    const controller = aborter.current;
    if (!controller) return null;
    setBusy(busyKey);
    try {
      const next = await advanceWithQueue({
        call: () => chat.advanceThreeView(sessionId, group.runId, slot.key, action, slot.attempt),
        onQueued: () => {
          // 排队中不占用 busy，其它视图仍可开始；本视图由 queued 禁用按钮。
          setQueued((current) => new Set(current).add(busyKey));
          setBusy((current) => (current === busyKey ? null : current));
        },
        signal: controller.signal,
      });
      if (!next) return null;
      await mutate((current) => (current ?? NONE).map((item) => (item.runId === next.runId ? next : item)), { revalidate: false });
      return null;
    } catch (error) {
      void mutate();
      return error instanceof Error ? error.message : schema.chat.presentation.messages.internal_error;
    } finally {
      setBusy((current) => (current === busyKey ? null : current));
      setQueued((current) => {
        if (!current.has(busyKey)) return current;
        const rest = new Set(current);
        rest.delete(busyKey);
        return rest;
      });
    }
  }, [chat, mutate, schema, sessionId]);

  return { groups: data ?? NONE, busy, queued, advance, refresh: () => mutate() };
}
