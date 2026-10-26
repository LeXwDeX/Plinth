'use client';

import { useEffect, useMemo, useRef } from 'react';
import useSWRInfinite from 'swr/infinite';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { useThreeViews } from '@/hooks/use-three-views';
import { mergedVersionsOf } from '@/lib/workspace/merge-version';
import { useCanvasStore } from '@/store/canvas-store';
import { buildVersionTiles, type VersionTile, type VersionTiles } from '@/lib/workspace/versions';
import { mergeLineagePages } from '@/lib/workspace/lineage';
import { claimLineageRefresh, countSettledChanges, lineageWatchSignature, settledOutsideChain, type LineageWatch } from '@/lib/workspace/lineage-refresh';
import { useChatStore } from '@/store/chat-store';
import { VERSIONS_WATCH_INTERVAL_MS, useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';
import { taskStatePresentation } from '@/lib/product-schema';
import { keepUnchanged } from '@/lib/core/keep-unchanged';

/**
 * 本会话的版本：服务端版本链（已成功的任务）加上本会话跟踪中的任务。有任务成功
 * 时重新读取版本链，让新版本以 2D vN / 3D vN 的编号出现。
 */
export function useSessionVersions(sessionOverride?: string | null) {
  const { schema } = useProductSchema();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const sessionId = sessionOverride === undefined ? activeSessionId : sessionOverride;
  const revision = useStudioStore((state) => state.versionsRevision);
  const watchUntil = useStudioStore((state) => state.versionsWatchUntil);
  const tracked = useWorkspaceStore((state) => state.trackedTasks);
  const enabled = Boolean(sessionId && schema.runtime.http.routes.chat_session_lineage);
  const { groups: activeViews } = useThreeViews();
  const threeViews = sessionId === activeSessionId ? activeViews : undefined;
  // The chain changes when a task of this session reaches a successful end.
  const settledIds = useMemo(
    () => tracked
      .filter((task) => task.sessionId === sessionId && task.terminal && !task.failure && !task.liveError && !task.notFound)
      .map((task) => task.taskId),
    [tracked, sessionId],
  );
  // A view finishing outside the task tracker (the card's own polling) also changes the chain.
  const viewsDone = useMemo(
    () => (threeViews ?? []).flatMap((group) => group.slots.filter((slot) => slot.status === 'succeeded').map((slot) => slot.taskId)).sort().join(','),
    [threeViews],
  );
  const { data, error, isLoading, isValidating, size, setSize, mutate } = useSWRInfinite(
    (index, previous) => {
      if (!enabled || (index > 0 && !previous?.graph.nextCursor)) return null;
      return ['session-lineage-page', sessionId, index === 0 ? '' : previous.graph.nextCursor] as const;
    },
    // The answer carries the session it belongs to: while another session's chain is kept on
    // screen between a switch and the new answer it must not pass for this session's versions.
    async ([, id, cursor]) => ({ id: id as string, graph: await chat.getLineagePage(id as string, cursor) }),
    {
      revalidateOnFocus: false,
      revalidateAll: true,
      // Work the tracker does not know about (a step approved after its turn ended) is looked for
      // every few seconds while the studio is told to watch.
      refreshInterval: (latest) => (Date.now() < watchUntil || latest?.some((page) => page.graph.nodes.some((node) => node.state && !taskStatePresentation(schema, node.state).terminal)) ? VERSIONS_WATCH_INTERVAL_MS : 0),
    },
  );
  const graph = useMemo(() => data?.[0]?.id === sessionId
    ? mergeLineagePages(data.filter((page) => page.id === sessionId).map((page) => page.graph)) : undefined, [data, sessionId]);
  // Finished tasks the chain already shows (an open loads them into the tracker too) are not a change.
  const chainFinished = useMemo(() => (graph
    ? new Set(graph.nodes.filter((node) => !node.state || taskStatePresentation(schema, node.state).terminal).map((node) => node.taskId))
    : null), [graph, schema]);
  const settled = useMemo(() => settledOutsideChain(settledIds, chainFinished), [settledIds, chainFinished]);
  // Opening a session is read once by SWR itself (shared, deduplicated key); only a later change of the chain
  // is refreshed explicitly, by one consumer for all of them (lib/workspace/lineage-refresh).
  const watched = useRef<LineageWatch | null>(null);
  useEffect(() => {
    if (!enabled || !sessionId) {
      watched.current = null;
      return;
    }
    const previous = watched.current;
    const next: LineageWatch = { sessionId, signature: lineageWatchSignature(countSettledChanges(sessionId, settled), viewsDone, revision) };
    watched.current = next;
    if (claimLineageRefresh(previous, next)) void mutate();
  }, [enabled, sessionId, settled, viewsDone, revision, mutate]);
  const boardNodes = useCanvasStore((state) => state.nodesBySession[sessionId ?? '']);
  const merged = useMemo(() => (boardNodes ? mergedVersionsOf(boardNodes) : []), [boardNodes]);
  // Rebuilt on every task update (progress included); a tile that did not change keeps its object, and
  // the list keeps its identity when no tile changed, so memoized tiles are not redrawn (#291).
  const previousTiles = useRef<VersionTile[] | undefined>(undefined);
  const versions = useMemo<VersionTiles>(() => {
    const built = buildVersionTiles({ schema, graph, tracked, sessionId, threeViews, merged });
    const tiles = keepUnchanged(previousTiles.current, built.tiles, (tile) => tile.key);
    previousTiles.current = tiles;
    return { ...built, tiles };
  }, [schema, graph, tracked, sessionId, threeViews, merged]);
  return { ...versions, truncated: graph?.truncated === true, error,
    hasEarlier: Boolean(graph?.nextCursor), loadingEarlier: isValidating && (size > (data?.length ?? 0)),
    loadEarlier: async () => {
      const next = await setSize((data?.length ?? 0) + 1);
      if (!next || next.length <= (data?.length ?? 0)) throw new Error('lineage page unavailable');
      return next;
    },
    isLoading: isLoading || (enabled && graph === undefined && !error), refresh: () => mutate() };
}

/** The version a current object belongs to (a candidate group is one version). */
export function tileOfObject(
  versions: Pick<VersionTiles, 'tiles' | 'byTask'> & Partial<Pick<VersionTiles, 'byCapture'>>,
  object: (Pick<CurrentObject, 'taskId'> & Partial<Pick<CurrentObject, 'captureId'>>) | null,
): VersionTile | null {
  if (!object) return null;
  // A screenshot carries the task of the model it was taken from, so its capture record names it.
  const captured = object.captureId ? versions.byCapture?.get(object.captureId) : undefined;
  const key = captured ?? versions.byTask.get(object.taskId);
  return key ? versions.tiles.find((tile) => tile.key === key) ?? null : null;
}
