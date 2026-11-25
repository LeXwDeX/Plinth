'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { schemaText } from '@/lib/product-schema';
import { advanceWithQueue, threeViewQueueLimitMs } from '@/lib/workspace/three-view-queue';
import { uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import { groupsAreActive, remainingSlots, slotAction, slotReplaceable, viewLabelOfTask, type ThreeViewGroup, type ThreeViewSlot } from '@/lib/workspace/three-view';
import { createLineageRefreshGate, type LineageWatch } from '@/lib/workspace/lineage-refresh';
import type { CanvasNode } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

const NONE: ThreeViewGroup[] = [];

/** The gate every useThreeViews consumer shares, so one change asks for one read (as for the version chain). */
const claimThreeViewRefresh = createLineageRefreshGate();

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
  // #618: a group is made by a turn (or a step approved after it), not by this page, so with no group in flight
  // nothing above reads again. The changes that can make or move one are read once more: the session's turn
  // starting or ending, a tracked Task of it registered or settling, and the studio's versions bump (an approval).
  const revision = useStudioStore((state) => state.versionsRevision);
  const turnRunning = useChatStore((state) => Boolean(sessionId && (state.streamingBuffers[sessionId]
    || state.messagesBySession[sessionId]?.some((message) => message.isPending))));
  const runningTasks = useWorkspaceStore((state) => state.trackedTasks
    .filter((task) => task.sessionId === sessionId && !task.terminal)
    .map((task) => task.taskId).sort().join(','));
  const watched = useRef<LineageWatch | null>(null);
  useEffect(() => {
    if (!enabled || !sessionId) {
      watched.current = null;
      return;
    }
    const previous = watched.current;
    const next: LineageWatch = { sessionId, signature: `${revision}|${turnRunning ? 1 : 0}|${runningTasks}` };
    watched.current = next;
    // Every consumer sees the change; one asks (an open is read by SWR itself, deduplicated).
    if (claimThreeViewRefresh(previous, next)) void mutate();
  }, [enabled, sessionId, revision, turnRunning, runningTasks, mutate]);
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
    const limitMs = threeViewQueueLimitMs(schema);
    // Schema 没有声明排队时限时不提交：没有兜底时长，直接告诉用户不可用。
    if (limitMs === null) return schemaText(schema, 'ui.studio.three_view.queue_unavailable');
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
        limitMs,
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

  /**
   * #65: "generate remaining views" starts every required view that has not started, one request (and so one
   * approval) each, in the group's order. It stops at the first refusal and returns its message.
   */
  const startRemaining = useCallback(async (group: ThreeViewGroup): Promise<string | null> => {
    for (const slot of remainingSlots(group)) {
      const message = await advance(group, slot);
      if (message) return message;
    }
    return null;
  }, [advance]);

  /**
   * #65: puts a picture the person dropped or chose in place of one view. The picture becomes their input asset
   * first; the server then records it for the view and spends nothing. Returns an error message, or null.
   */
  const replace = useCallback(async (group: ThreeViewGroup, slot: ThreeViewSlot, picture: File): Promise<string | null> => {
    if (!sessionId || !slotReplaceable(slot)) return null;
    const busyKey = `${group.runId}:${slot.key}`;
    setBusy(busyKey);
    try {
      const { id } = await uploadWorkspaceInputAssetResult(schema, picture, picture.name || 'view.png');
      const next = await chat.replaceThreeView(sessionId, group.runId, slot.key, id);
      await mutate((current) => (current ?? NONE).map((item) => (item.runId === next.runId ? next : item)), { revalidate: false });
      return null;
    } catch (error) {
      void mutate();
      return error instanceof Error && error.message ? error.message : schemaText(schema, 'ui.studio.three_view.replace_failed');
    } finally {
      setBusy((current) => (current === busyKey ? null : current));
    }
  }, [chat, mutate, schema, sessionId]);

  return { groups: data ?? NONE, busy, queued, advance, startRemaining, replace, refresh: () => mutate() };
}

/**
 * #618: the three-view view ("正面") of every picture layer a three-view group made, by node id. Only the session in
 * view has its groups loaded; layers of another session, and layers no group made, are absent.
 */
export function useNodeViews(sessionId: string | null | undefined, nodes: readonly CanvasNode[]): ReadonlyMap<string, string> {
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { groups } = useThreeViews();
  return useMemo(() => {
    const result = new Map<string, string>();
    if (!sessionId || sessionId !== activeSessionId || groups.length === 0) return result;
    for (const node of nodes) {
      if (node.type !== 'image' || !node.taskId) continue;
      const view = viewLabelOfTask(groups, node.taskId);
      if (view) result.set(node.id, view);
    }
    return result;
  }, [activeSessionId, groups, nodes, sessionId]);
}
