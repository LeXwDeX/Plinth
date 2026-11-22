'use client';

import { useEffect, useMemo, useRef } from 'react';
import { Image as ImageIcon } from 'lucide-react';

import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import {
  boardCropGroups,
  boardItemIndex,
  boardTaskID,
  coverAsset,
  inputAssetPath,
  taskAssetPath,
} from '@/lib/asset-lists/board';
import type { AssetListBoardItem } from '@/lib/asset-lists/client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

/**
 * #329d: the state tone of a box or result cell. Color alone never carries the
 * meaning: the legend names every state, each box is labelled with its number
 * and state for assistive technology, and the result cell prints the label.
 */
const STATE_TONE: Record<string, string> = {
  todo: 'border-border bg-muted/30',
  working: 'border-warning bg-warning/15',
  review: 'border-primary bg-primary/15',
  accepted: 'border-success bg-success/15',
  returned: 'border-destructive bg-destructive/15',
  failed: 'border-destructive bg-destructive/25',
  skipped: 'border-border bg-muted/40',
};

function stateTone(state: string): string {
  return STATE_TONE[state] ?? 'border-border bg-muted/30';
}

export interface AssetBoardCompareProps {
  schema: ProductSchema;
  /** Every board item in position order; the numbering follows this order. */
  items: readonly AssetListBoardItem[];
  /** The items the active filter keeps; only crop items are drawn. */
  shown: readonly AssetListBoardItem[];
  /** The item both panes highlight; clicks in either pane set it. */
  focusedID: string | null;
  onFocus: (itemID: string) => void;
}

/**
 * #329d: the original-picture comparison. Each crop source is drawn once with
 * one numbered box per item, colored by the item's state; the results of the
 * same group sit beside it. Clicking a box or a result focuses the other side
 * and scrolls it into view. The legend names every state in text, so the
 * colors are never the only signal.
 */
export function AssetBoardCompare({ schema, items, shown, focusedID, onFocus }: AssetBoardCompareProps) {
  const { previewAssetUrl } = useAssetURLRewriter();
  const text = (key: string) => schemaText(schema, key);
  const groups = useMemo(() => boardCropGroups(shown), [shown]);
  const boxRefs = useRef(new Map<string, HTMLButtonElement | null>());
  const cellRefs = useRef(new Map<string, HTMLButtonElement | null>());

  // A click on either pane scrolls the paired control into view. The guard
  // keeps jsdom (no scrollIntoView) and a missing node harmless.
  useEffect(() => {
    if (!focusedID) return;
    boxRefs.current.get(focusedID)?.scrollIntoView?.({ block: 'nearest' });
    cellRefs.current.get(focusedID)?.scrollIntoView?.({ block: 'nearest' });
  }, [focusedID]);

  const stateLabel = (state: string) =>
    schema.asset_list?.item_states.find((candidate) => candidate.id === state)?.label || state;

  if (groups.length === 0) {
    return (
      <p className="p-6 text-sm text-muted-foreground" data-testid="asset-board-compare-empty">
        {text('ui.asset_list.board.compare_empty')}
      </p>
    );
  }

  const legend = (schema.asset_list?.item_states ?? [])
    .map((state) => ({
      id: state.id,
      label: state.label,
      count: shown.filter((item) => item.state === state.id).length,
    }))
    .filter((entry) => entry.count > 0);

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="asset-board-compare">
      <section className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2" aria-label={text('ui.asset_list.board.compare_legend')}>
        <h3 className="text-xs font-semibold">{text('ui.asset_list.board.compare_legend')}</h3>
        <ul className="flex flex-wrap items-center gap-x-4 gap-y-1" data-testid="asset-board-compare-legend">
          {legend.map((entry) => (
            <li key={entry.id} className="flex items-center gap-1.5 text-[11px] text-muted-foreground" data-testid={`asset-board-compare-legend-${entry.id}`}>
              <span aria-hidden className={cn('size-3 shrink-0 rounded-sm border', stateTone(entry.id))} />
              <span className="text-foreground">{entry.label}</span>
              <span className="tabular-nums">{entry.count}</span>
            </li>
          ))}
        </ul>
        <p className="basis-full text-[11px] leading-4 text-muted-foreground">
          {text('ui.asset_list.board.compare_hint')}
        </p>
      </section>

      <div className="min-h-0 flex-1 overflow-y-auto p-4" data-testid="asset-board-compare-scroll">
        <div className="flex flex-col gap-8">
          {groups.map((group, groupIndex) => {
            // #425: the comparison is drawn large on purpose, so it needs the original picture.
            // `previewAssetUrl` only answers a 512px preview (asset-url-rewriter.ts), which is
            // too coarse to judge a crop; the large compare panes use the original as well.
            const sourceURL = safeAssetURL(inputAssetPath(schema, group.sourceAssetID));
            return (
              <section
                key={group.sourceAssetID}
                aria-label={formatSchemaText(text('ui.asset_list.board.compare_source_index'), { index: groupIndex + 1 })}
                className="flex flex-col gap-4 lg:flex-row lg:items-start"
                data-testid={`asset-board-compare-group-${group.sourceAssetID}`}
              >
                <div className="flex min-w-0 flex-col gap-2 lg:flex-1">
                  <h3 className="text-xs font-semibold">
                    {formatSchemaText(text('ui.asset_list.board.compare_source_index'), { index: groupIndex + 1 })}
                  </h3>
                  {sourceURL ? (
                    <span className="relative block w-fit max-w-full self-center">
                      <img
                        src={sourceURL}
                        alt={text('ui.asset_list.board.compare_source')}
                        className="block h-auto max-h-[60vh] w-auto max-w-full rounded-md border border-border bg-muted"
                        data-testid={`asset-board-compare-source-${group.sourceAssetID}`}
                      />
                      {group.items.map((item) => {
                        const region = item.region;
                        if (!region) return null;
                        const index = boardItemIndex(items, item.id);
                        const label = stateLabel(item.state);
                        const boxLabel = formatSchemaText(text('ui.asset_list.board.compare_box'), {
                          index, name: item.name, state: label,
                        });
                        return (
                          <HintTooltip
                            key={item.id}
                            name={boxLabel}
                            className="absolute flex"
                            triggerProps={{
                              style: {
                                left: `${region.x * 100}%`,
                                top: `${region.y * 100}%`,
                                width: `${region.width * 100}%`,
                                height: `${region.height * 100}%`,
                              },
                            }}
                          >
                            <button
                              type="button"
                              ref={(element) => { boxRefs.current.set(item.id, element); }}
                              onClick={() => onFocus(item.id)}
                              aria-pressed={focusedID === item.id}
                              aria-label={boxLabel}
                              data-testid={`asset-board-compare-box-${item.id}`}
                              data-state={item.state}
                              data-focused={focusedID === item.id ? 'true' : undefined}
                              className={cn(
                                'flex size-full min-h-11 min-w-11 items-start justify-start rounded-sm border-2 p-0.5 text-left text-[11px] font-medium transition-shadow focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring',
                                stateTone(item.state),
                                focusedID === item.id && 'ring-2 ring-ring ring-offset-1 ring-offset-background',
                              )}
                            >
                              <span className="rounded-sm bg-background/90 px-1 tabular-nums text-foreground">{index}</span>
                            </button>
                          </HintTooltip>
                        );
                      })}
                    </span>
                  ) : (
                    <p className="text-xs text-muted-foreground" data-testid={`asset-board-compare-source-missing-${group.sourceAssetID}`}>
                      {text('ui.asset_list.board.compare_source_missing')}
                    </p>
                  )}
                </div>

                <div className="flex min-w-0 flex-col gap-2 lg:flex-1">
                  <h3 className="text-xs font-semibold">{text('ui.asset_list.board.compare_results')}</h3>
                  <ul className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-2" data-testid={`asset-board-compare-results-${group.sourceAssetID}`}>
                    {group.items.map((item) => {
                      const taskID = boardTaskID(item);
                      const coverURL = previewAssetUrl(taskAssetPath(schema, taskID, coverAsset(item)));
                      const index = boardItemIndex(items, item.id);
                      const label = stateLabel(item.state);
                      const focused = focusedID === item.id;
                      return (
                        <li key={item.id}>
                          <button
                            type="button"
                            ref={(element) => { cellRefs.current.set(item.id, element); }}
                            onClick={() => onFocus(item.id)}
                            aria-pressed={focused}
                            aria-label={formatSchemaText(text('ui.asset_list.board.compare_cell'), {
                              index, name: item.name,
                            })}
                            data-testid={`asset-board-compare-cell-${item.id}`}
                            data-state={item.state}
                            data-focused={focused ? 'true' : undefined}
                            className={cn(
                              'flex min-h-11 w-full flex-col gap-2 rounded-lg border bg-panel-raised p-2 text-left transition-shadow focus-visible:outline-2 focus-visible:outline-ring',
                              focused ? 'border-primary ring-2 ring-primary' : 'border-border',
                            )}
                          >
                            <span className="relative flex aspect-square w-full items-center justify-center overflow-hidden rounded-md bg-muted">
                              {coverURL ? (
                                <img src={coverURL} alt={''} loading="lazy" decoding="async" className="size-full object-cover" />
                              ) : (
                                <ImageIcon aria-hidden className="size-6 text-muted-foreground" />
                              )}
                              <span className="absolute left-1 top-1 rounded-sm bg-background/90 px-1 text-[11px] tabular-nums text-foreground">{index}</span>
                            </span>
                            <span className="flex flex-wrap items-baseline gap-1 text-[12px]">
                              <span className="min-w-0 flex-1 truncate font-medium">{item.name}</span>
                              <span className="shrink-0 text-[10px] text-muted-foreground">{label}</span>
                            </span>
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                </div>
              </section>
            );
          })}
        </div>
      </div>
    </div>
  );
}