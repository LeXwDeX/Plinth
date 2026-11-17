'use client';

import { useCallback, useMemo } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { assetListClient, type AssetListSummary } from '@/lib/asset-lists/client';

const NO_LISTS: AssetListSummary[] = [];

/**
 * #327: the production lists proposed in one chat session, read from the
 * server so the list card looks the same after a reload or in another session
 * switch. Read again when the window regains focus; an earlier session's
 * answer is never kept on screen after a switch.
 */
export function useSessionAssetLists(sessionId: string | null) {
  const { schema } = useProductSchema();
  const client = useMemo(() => assetListClient(schema), [schema]);
  const enabled = Boolean(sessionId && schema.runtime.http.routes.chat_session_asset_lists);
  const { data, error, isLoading, mutate } = useSWR<AssetListSummary[]>(
    enabled ? ['session-asset-lists', sessionId] : null,
    ([, id]: readonly [string, string]) => client.listSessionLists(id),
    { revalidateOnFocus: true, keepPreviousData: false },
  );
  const refresh = useCallback(async () => { await mutate(); }, [mutate]);
  return {
    lists: enabled ? data ?? NO_LISTS : NO_LISTS,
    loading: enabled && isLoading,
    error: enabled ? error : undefined,
    refresh,
  };
}