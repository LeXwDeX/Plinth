'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import {
  assetListClient,
  type AssetListClient,
  type AssetListItem,
  type AssetListRun,
  type AssetListRunAction,
  type AssetListRunState,
  type AssetListSummary,
} from '@/lib/asset-lists/client';

/** The brief's 3–5s window: the runner ticks every 2s, so 4s keeps the card current. */
const DEFAULT_INTERVAL_MS = 4000;

export interface UseAssetListRunOptions {
  /** The open session's project; nothing is read while empty. */
  projectId: string | null;
  /** One list to follow; by default the project's most recent confirmed list. */
  listId?: string | null;
  /** Test seam; the real hook builds its client from the Schema. */
  client?: AssetListClient;
  /** Overrides the 4s polling interval. */
  intervalMs?: number;
  /** False on a Catalog without the run route: no request is sent. */
  enabled?: boolean;
}

/** The newest confirmed, unexpired list: the one the progress chip follows. */
export function latestConfirmedList(
  lists: AssetListSummary[],
  listId?: string | null,
): AssetListSummary | null {
  if (listId) return lists.find((list) => list.id === listId) ?? null;
  const candidates = lists.filter((list) =>
    list.status === 'confirmed' && list.effective_status !== 'expired' && list.effective_status !== 'superseded');
  if (candidates.length === 0) return null;
  return [...candidates].sort((left, right) =>
    (right.decided_at ?? right.updated_at).localeCompare(left.decided_at ?? left.updated_at))[0];
}

function documentVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

/**
 * #328: the run summary of one project list. The hook reads the project's list
 * summaries, picks the tracked (or newest confirmed) list and polls it every few
 * seconds while the tab is visible; controls resolve and then re-read at once so
 * the card never shows the state it just left.
 */
export function useAssetListRun(options: UseAssetListRunOptions) {
  const { schema } = useProductSchema();
  const { projectId, listId = null, intervalMs = DEFAULT_INTERVAL_MS, enabled = true } = options;
  const fallbackClient = useMemo(() => assetListClient(schema), [schema]);
  const client = options.client ?? fallbackClient;
  const [visible, setVisible] = useState(documentVisible);

  // Poll only while the workspace can be seen; a hidden tab revalidates when it
  // becomes visible again (SWR's focus revalidation covers the switch too).
  useEffect(() => {
    if (typeof document === 'undefined') return;
    const sync = () => setVisible(documentVisible());
    sync();
    document.addEventListener('visibilitychange', sync);
    return () => document.removeEventListener('visibilitychange', sync);
  }, []);

  const active = Boolean(projectId) && enabled;
  const { data, error, isLoading, mutate } = useSWR<AssetListSummary[]>(
    active ? ['asset-list-run', projectId, listId ?? ''] : null,
    ([, id]: readonly [string, string, string]) => client.listProjectLists(id),
    {
      refreshInterval: visible ? intervalMs : 0,
      revalidateOnFocus: true,
      keepPreviousData: false,
    },
  );

  const lists = active ? data ?? [] : [];
  const list = active ? latestConfirmedList(lists, listId) : null;
  const run: AssetListRun | null = list?.run ?? null;

  const refresh = useCallback(async () => { await mutate(); }, [mutate]);

  const control = useCallback(async (
    action: AssetListRunAction,
    expectedVersion: number,
    reason?: string,
  ): Promise<AssetListRunState> => {
    if (!projectId || !list) throw new Error('asset list run is not loaded');
    const next = await client.controlRun(projectId, list.id, action, expectedVersion, reason);
    await mutate();
    return next;
  }, [projectId, list, client, mutate]);

  const skip = useCallback(async (
    itemID: string,
    expectedVersion: number,
    reason?: string,
  ): Promise<AssetListItem> => {
    if (!projectId || !list) throw new Error('asset list run is not loaded');
    const item = await client.skipItem(projectId, list.id, itemID, expectedVersion, reason);
    await mutate();
    return item;
  }, [projectId, list, client, mutate]);

  return {
    list,
    lists,
    run,
    loading: active && isLoading,
    error: active ? error : undefined,
    refresh,
    control,
    skip,
  };
}