'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Image as ImageIcon, ListChecks, X } from 'lucide-react';

import { AssetBoardDetail } from '@/components/studio/asset-board-detail';
import { Action } from '@/components/ui/button';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import {
  ASSET_LIST_BOARD_FILTERS,
  boardCheckCounts,
  boardFilterCount,
  boardItems,
  boardSlotKey,
  boardSteps,
  boardTaskID,
  coverAsset,
  filterBoardItems,
  model3DAsset,
  taskAssetPath,
  type AssetListBoardFilter,
} from '@/lib/asset-lists/board';
import {
  AssetListError,
  assetListClient,
  type AssetListBoard,
  type AssetListBoardItem,
  type AssetListClient,
} from '@/lib/asset-lists/client';
import { isEditableTarget } from '@/lib/workspace/stage-keys';
import { cn } from '@/lib/utils';
import {
  formatSchemaText,
  schemaCountMessage,
  schemaText,
  type ProductSchema,
} from '@/lib/product-schema';
import { useInlineModelPreviewStore } from '@/store/inline-model-preview-store';
import { useStudioStore, type AssetBoardTarget } from '@/store/studio-store';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';

export interface AssetListBoardProps {
  target: AssetBoardTarget;
  schema: ProductSchema;
  /** The client the stage built from the Schema, or a test double. */
  client?: AssetListClient;
}

/**
 * #329: the acceptance board covers the stage like 全部产出 does, because the
 * right drawer is too narrow for a whole list. The header carries the list's
 * title, state, count and the six filters; the grid shows every item from one
 * aggregate read with its cover, name, state, self-check summary and steps.
 * A 3D item can be rotated in place through the global single viewer slot, so
 * only one cell turns at a time. Every cell is a button that opens the detail
 * drawer; there is no hover-only action.
 */
export function AssetListBoard({ target, schema, client }: AssetListBoardProps) {
  const source = useMemo(() => client ?? assetListClient(schema), [client, schema]);
  const close = useStudioStore((state) => state.closeAssetBoard);
  const text = (key: string) => schemaText(schema, key);
  const [board, setBoard] = useState<AssetListBoard | null>(null);
  const [loading, setLoading] = useState(true);
  const [errorDetail, setErrorDetail] = useState('');
  const [filter, setFilter] = useState<AssetListBoardFilter>('all');
  const [detailID, setDetailID] = useState<string | null>(null);
  // The latest read wins: a slow first answer must not replace a newer read
  // (or land after the board is gone).
  const requestSeq = useRef(0);

  const load = useCallback(async () => {
    const seq = requestSeq.current + 1;
    requestSeq.current = seq;
    setLoading(true);
    setErrorDetail('');
    try {
      const next = await source.getBoard(target.projectId, target.listId);
      if (requestSeq.current === seq) setBoard(next);
    } catch (failure) {
      if (requestSeq.current !== seq) return;
      setBoard(null);
      setErrorDetail(failure instanceof AssetListError
        ? failure.detail || failure.message
        : failure instanceof Error ? failure.message : '');
    } finally {
      if (requestSeq.current === seq) setLoading(false);
    }
  }, [source, target.projectId, target.listId]);

  useEffect(() => {
    void load();
    return () => {
      // Invalidate an in-flight read when the board or the list changes.
      requestSeq.current += 1;
    };
  }, [load]);

  // Escape leaves the detail first, then the board; a prevented key (a popup
  // inside the drawer) is left to that popup.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || isEditableTarget(event.target)) return;
      event.preventDefault();
      if (detailID) setDetailID(null);
      else close();
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [detailID, close]);

  const items = boardItems(board);
  const shown = filterBoardItems(items, filter);
  const detailItem = detailID ? items.find((item) => item.id === detailID) ?? null : null;
  const title = board?.list.title || target.title;
  const stateLabel = board
    ? schema.asset_list?.states.find((state) => state.id === board.list.effective_status)?.label
      ?? board.list.effective_status
    : '';
  const loadFailedText = text(errorDetail) || errorDetail || text('ui.asset_list.board.load_failed');

  return (
    <div
      role="region"
      aria-label={text('ui.asset_list.board.title')}
      data-testid="asset-list-board"
      className="absolute inset-0 z-float flex flex-col bg-canvas"
    >
      <header className="flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <ListChecks aria-hidden className="size-4 shrink-0 text-primary" />
        <h2 className="min-w-0 max-w-[24rem] truncate text-sm font-semibold" data-testid="asset-board-title">{title}</h2>
        {stateLabel ? (
          <span className="shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground" data-testid="asset-board-status">
            {stateLabel}
          </span>
        ) : null}
        <span className="shrink-0 tabular-nums text-xs text-muted-foreground" data-testid="asset-board-count">
          {schemaCountMessage(schema, 'ui.asset_list.board.items_count', items.length, { count: items.length })}
        </span>
        <div role="group" aria-label={text('ui.asset_list.board.filter')} className="flex min-w-0 flex-wrap items-center gap-1">
          {ASSET_LIST_BOARD_FILTERS.map((option) => (
            <button
              key={option.id}
              type="button"
              aria-pressed={filter === option.id}
              onClick={() => setFilter(option.id)}
              data-testid={`asset-board-filter-${option.id}`}
              className={cn(
                'flex min-h-11 items-center gap-1 rounded-md px-4 text-xs transition-colors focus-visible:outline-2 focus-visible:outline-ring',
                filter === option.id ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              )}
            >
              {text(option.labelKey)}
              <span className="tabular-nums">{boardFilterCount(items, option.id)}</span>
            </button>
          ))}
        </div>
        <Action
          type="button"
          variant="ghost"
          size="icon-lg"
          className="ml-auto size-11 shrink-0 text-muted-foreground"
          onClick={close}
          aria-label={text('ui.asset_list.board.close')}
          data-testid="asset-board-close"
        >
          <X />
        </Action>
      </header>

      {loading ? (
        <p role="status" className="p-6 text-sm text-muted-foreground" data-testid="asset-board-loading">
          {text('ui.asset_list.board.loading')}
        </p>
      ) : board === null ? (
        <div role="alert" className="m-4 flex flex-wrap items-center gap-3 rounded-lg border border-destructive/40 p-4 text-sm" data-testid="asset-board-error">
          <span className="min-w-0 [overflow-wrap:anywhere]">{loadFailedText}</span>
          <Action type="button" variant="outline" size="sm" className="min-h-11" onClick={() => void load()} data-testid="asset-board-retry">
            {text('ui.asset_list.board.retry')}
          </Action>
        </div>
      ) : items.length === 0 ? (
        <p className="p-6 text-sm text-muted-foreground" data-testid="asset-board-empty">
          {text('ui.asset_list.board.empty')}
        </p>
      ) : shown.length === 0 ? (
        <p className="p-6 text-sm text-muted-foreground" data-testid="asset-board-empty-filter">
          {text('ui.asset_list.board.empty_filter')}
        </p>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto p-4" data-testid="asset-board-scroll">
          <ul className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] content-start gap-4" data-testid="asset-board-grid">
            {shown.map((item) => (
              <BoardCell
                key={item.id}
                item={item}
                schema={schema}
                detailOpen={detailItem?.id === item.id}
                onOpen={() => setDetailID(item.id)}
              />
            ))}
          </ul>
        </div>
      )}

      {detailItem ? (
        <AssetBoardDetail item={detailItem} schema={schema} onClose={() => setDetailID(null)} />
      ) : null}
    </div>
  );
}

interface BoardCellProps {
  item: AssetListBoardItem;
  schema: ProductSchema;
  /** The drawer shows this item's viewer instead of the cell. */
  detailOpen: boolean;
  onOpen: () => void;
}

/** One grid cell: cover and text open the details, the 3D button rotates the model in place. */
function BoardCell({ item, schema, detailOpen, onOpen }: BoardCellProps) {
  const { previewAssetUrl } = useAssetURLRewriter();
  const text = (key: string) => schemaText(schema, key);
  const slot = boardSlotKey(item.id);
  const live = useInlineModelPreviewStore((state) => state.taskId === slot);
  // A cell that leaves the grid (filter change, board close) gives the single
  // viewer up; `close` keeps it when another cell already took it.
  useEffect(() => () => useInlineModelPreviewStore.getState().close(slot), [slot]);
  const taskID = boardTaskID(item);
  const model = model3DAsset(item);
  const modelURL = taskAssetPath(schema, taskID, model);
  const coverURL = previewAssetUrl(taskAssetPath(schema, taskID, coverAsset(item)));
  const showViewer = live && !detailOpen && Boolean(modelURL);
  const counts = boardCheckCounts(item);
  const steps = boardSteps(item);
  const stateLabel = schema.asset_list?.item_states.find((state) => state.id === item.state)?.label || item.state;
  const selfCheck = item.review?.self_check;
  const selfCheckLabel = selfCheck?.status
    ? schema.chat.self_check?.statuses?.find((status) => status.role === selfCheck.status)?.label || selfCheck.status
    : '';
  const summary = counts.total === 0
    ? text('ui.asset_list.board.checks_none')
    : counts.fail > 0
      ? formatSchemaText(text('ui.asset_list.board.checks_summary_fail'), { fail: counts.fail })
      : counts.unknown > 0
        ? formatSchemaText(text('ui.asset_list.board.checks_summary'), {
          pass: counts.pass, fail: counts.fail, unknown: counts.unknown,
        })
        : text('ui.asset_list.board.checks_summary_pass');
  const stepsText = steps
    ? steps.cap && steps.cap > 0
      ? formatSchemaText(text('ui.asset_list.board.steps_capped'), { used: steps.used, cap: steps.cap })
      : formatSchemaText(text('ui.asset_list.board.steps'), { steps: steps.used })
    : text('ui.asset_list.board.steps_unknown');
  const openLabel = formatSchemaText(text('ui.asset_list.board.cell_open'), { name: item.name });

  return (
    <li
      className="flex flex-col rounded-lg border border-border bg-panel-raised p-4"
      data-testid={`asset-board-item-${item.id}`}
      data-kind={item.kind}
      data-state={item.state}
    >
      {showViewer ? (
        <div className="aspect-square w-full overflow-hidden rounded-md border border-border" data-testid={`asset-board-preview-${item.id}`}>
          <ModelViewer3D src={modelURL} alt={item.name} autoRotate className="h-full w-full" />
        </div>
      ) : (
        <button
          type="button"
          onClick={onOpen}
          aria-label={openLabel}
          className="block w-full rounded-md focus-visible:outline-2 focus-visible:outline-ring"
          data-testid={`asset-board-cover-${item.id}`}
        >
          <span className="flex aspect-square w-full flex-col items-center justify-center gap-2 overflow-hidden rounded-md bg-muted">
            {coverURL ? (
              <img src={coverURL} alt="" loading="lazy" decoding="async" className="size-full object-cover" />
            ) : (
              <>
                {model ? <Box aria-hidden className="size-8 text-muted-foreground" /> : <ImageIcon aria-hidden className="size-8 text-muted-foreground" />}
                <span className="text-[11px] text-muted-foreground">{text('ui.asset_list.board.no_cover')}</span>
              </>
            )}
          </span>
        </button>
      )}

      <button
        type="button"
        onClick={onOpen}
        className="mt-2 flex w-full flex-col gap-1 rounded-md text-left focus-visible:outline-2 focus-visible:outline-ring"
        data-testid={`asset-board-cell-${item.id}`}
      >
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{item.name}</span>
          <span className="shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground" data-testid={`asset-board-state-${item.id}`}>
            {stateLabel}
          </span>
        </span>
        <span className="flex flex-wrap items-baseline gap-x-2 text-[11px] leading-4 text-muted-foreground">
          <span>{formatSchemaText(text('ui.asset_list.board.round'), { round: item.round })}</span>
          <span
            className={counts.fail > 0 ? 'text-destructive' : ''}
            data-testid={`asset-board-checks-${item.id}`}
          >
            {summary}
          </span>
        </span>
        {selfCheckLabel || selfCheck?.summary ? (
          <span className="flex min-w-0 items-baseline gap-1 text-[11px] leading-4 text-muted-foreground" data-testid={`asset-board-self-check-${item.id}`}>
            {selfCheckLabel ? <span className="shrink-0 text-foreground/70">{selfCheckLabel}</span> : null}
            {selfCheck?.summary ? <span className="min-w-0 truncate">{selfCheck.summary}</span> : null}
          </span>
        ) : null}
        <span className="flex flex-wrap items-baseline gap-x-2 tabular-nums text-[11px] leading-4 text-muted-foreground">
          {steps && !(steps.cap && steps.cap > 0) && steps.model3d > 0 ? (
            <span>{formatSchemaText(text('ui.asset_list.board.steps_3d'), { steps: steps.model3d })}</span>
          ) : null}
          <span className="ml-auto" data-testid={`asset-board-steps-${item.id}`}>{stepsText}</span>
        </span>
      </button>

      {modelURL ? (
        <div className="mt-2 flex justify-end">
          <Action
            type="button"
            variant="outline"
            size="sm"
            className="min-h-11 px-4"
            aria-pressed={live}
            onClick={() => {
              const previews = useInlineModelPreviewStore.getState();
              if (live) previews.close(slot);
              else previews.open(slot);
            }}
            data-testid={`asset-board-3d-${item.id}`}
          >
            {text(live ? 'ui.asset_list.board.preview_close' : 'ui.asset_list.board.preview_3d')}
          </Action>
        </div>
      ) : null}
    </li>
  );
}