'use client';

import useSWR from 'swr';

import { AssetListCard } from '@/components/studio/asset-list-card';
import type { AssetListClient, AssetListDetail, AssetListSummary } from '@/lib/asset-lists/client';
import { schemaText, type ProductSchema } from '@/lib/product-schema';

export interface AssetListCardLoaderProps {
  /** The list as the session or project listing showed it; the card reads the detail itself. */
  summary: AssetListSummary;
  schema: ProductSchema;
  client: AssetListClient;
  variant?: 'chat' | 'panel';
  /** A decision succeeded; the parent re-reads its own listing in addition to this detail. */
  onDecided?: () => void;
  /** #328: a run control or a skip succeeded; re-read this detail and the parent's listing. */
  onRunChanged?: () => void;
  /** #331: an edit succeeded; the returned detail replaces the cached one and the parent may refresh its listing. */
  onEdited?: (detail: AssetListDetail) => void;
  /** #331: the list changed under the editor (409 or a status change); re-read this detail and let the parent refresh its summary. */
  onStale?: () => void;
}

/**
 * #327: a list card whose detail is read on demand. The conversation and the
 * project panel know a list's summary first; the item rows, brief and version
 * come from the single-list route like the card's decision does.
 */
export function AssetListCardLoader({
  summary, schema, client, variant = 'chat', onDecided, onRunChanged, onEdited, onStale,
}: AssetListCardLoaderProps) {
  const { data, error, isLoading, mutate } = useSWR<AssetListDetail>(
    ['asset-list-detail', summary.project_id, summary.id] as const,
    ([, projectID, listID]: readonly [string, string, string]) => client.getList(projectID, listID),
  );
  if (error && !data) {
    return (
      <div
        className="flex flex-col gap-1.5 rounded-lg border border-destructive/40 p-3 text-xs"
        data-testid="asset-list-load-failed"
      >
        <p className="text-destructive" data-testid="asset-list-load-failed-text">
          {schemaText(schema, 'ui.asset_list.load_failed')}
        </p>
        <button
          type="button"
          className="self-start rounded px-1.5 py-0.5 text-[11px] font-medium underline hover:bg-muted"
          onClick={() => void mutate()}
          data-testid="asset-list-retry"
        >
          {schemaText(schema, 'ui.workspace.model_formats.retry')}
        </button>
      </div>
    );
  }
  if (isLoading || !data) {
    return (
      <div
        aria-hidden
        className="h-16 animate-pulse rounded-lg border border-border bg-muted/40"
        data-testid="asset-list-loading"
      />
    );
  }
  return (
    <AssetListCard
      detail={data}
      schema={schema}
      projectId={summary.project_id}
      client={client}
      variant={variant}
      onDecided={() => {
        void mutate();
        onDecided?.();
      }}
      onRunChanged={() => {
        void mutate();
        onRunChanged?.();
      }}
      onEdited={(updated) => {
        // The server already returned the stored list: show it without a
        // second read, then let the parent refresh its own listing.
        void mutate(updated, { revalidate: false });
        onEdited?.(updated);
      }}
      onStale={() => {
        // A concurrent edit (409) or a status change is re-read; the card
        // keeps its draft, and the parent refreshes the summary it listed.
        void mutate();
        onStale?.();
      }}
    />
  );
}