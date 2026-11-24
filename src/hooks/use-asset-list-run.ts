'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { useIdleInterval } from '@/hooks/use-polling';
import {
  assetListClient,
  type AssetListClient,
  type AssetListItem,
  type AssetListRun,
  type AssetListRunAction,
  type AssetListRunState,
  type AssetListSummary,
} from '@/lib/asset-lists/client';

export interface UseAssetListRunOptions {
  /** The open session's project; nothing is read while empty. */
  projectId: string | null;
  /** One list to follow; by default the project's most recent confirmed list. */
  listId?: string | null;
  /** Test seam; the real hook builds its client from the Schema. */
  client?: AssetListClient;
  /** Overrides the Schema's `runtime.frontend.task_polling_interval_milliseconds`. */
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

/**
 * #328: the run summary of one project list. The hook reads the project's list
 * summaries, picks the tracked (or newest confirmed) list and polls it; controls
 * resolve and then re-read at once so the card never shows the state it just
 * left.
 *
 * #511: the poll follows the page's rules (#385): the interval is the Schema's
 * task polling interval, the page's SWR gate stops it in a hidden tab and after
 * the login ended, and while the followed list is not running and its summary
 * stays the same the wait grows up to the Schema's `idle_polling` ceiling.
 */
export function useAssetListRun(options: UseAssetListRunOptions) {
  const { schema } = useProductSchema();
  const {
    projectId,
    listId = null,
    intervalMs = schema.runtime.frontend.task_polling_interval_milliseconds,
    enabled = true,
  } = options;
  const fallbackClient = useMemo(() => assetListClient(schema), [schema]);
  const client = options.client ?? fallbackClient;
  // A running list moves on its own: its reads keep the interval. Known from the previous read, because the interval
  // is handed to the read that produces the next one.
  const [running, setRunning] = useState(false);
  const refreshInterval = useIdleInterval<AssetListSummary[]>(
    intervalMs,
    schema.runtime.frontend.idle_polling,
    { busy: running },
  );

  const active = Boolean(projectId) && enabled;
  const { data, error, isLoading, mutate } = useSWR<AssetListSummary[]>(
    active ? ['asset-list-run', projectId, listId ?? ''] : null,
    ([, id]: readonly [string, string, string]) => client.listProjectLists(id),
    {
      refreshInterval,
      revalidateOnFocus: true,
      keepPreviousData: false,
    },
  );

  const lists = active ? data ?? [] : [];
  const list = active ? latestConfirmedList(lists, listId) : null;
  const run: AssetListRun | null = list?.run ?? null;
  const runStatus = run?.status ?? '';
  useEffect(() => { setRunning(runStatus === 'running'); }, [runStatus]);

  const refresh = useCallback(async () => { await mutate(); }, [mutate]);

  // #328: expectedVersion is the run's opaque `version` string (its updated_at
  // token); the list's numeric version belongs to decisions and edits.
  const control = useCallback(async (
    action: AssetListRunAction,
    expectedVersion: string,
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