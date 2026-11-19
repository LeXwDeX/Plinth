'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Box, Download, Image as ImageIcon, ListChecks, X } from 'lucide-react';

import { AssetBoardCompare } from '@/components/studio/asset-board-compare';
import { AssetBoardDetail } from '@/components/studio/asset-board-detail';
import { AssetBoardReturnDialog } from '@/components/studio/asset-board-return-dialog';
import { Action } from '@/components/ui/button';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import {
  ASSET_LIST_BOARD_FILTERS,
  acceptedBoardItems,
  boardCheckCounts,
  boardCropItems,
  boardDownloadScope,
  boardFilterCount,
  boardItems,
  boardPollingActive,
  boardSlotKey,
  boardSteps,
  boardTaskID,
  coverAsset,
  filterBoardItems,
  model3DAsset,
  returnReasonsForKind,
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
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import { isEditableTarget } from '@/lib/workspace/stage-keys';
import { saveArchive } from '@/lib/workspace/batch-export';
import { useInlineModelPreviewStore } from '@/store/inline-model-preview-store';
import { useStudioStore, type AssetBoardTarget } from '@/store/studio-store';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';

/** #329d: the board re-reads while the run produces; five seconds keeps the grid current. */
const DEFAULT_POLL_INTERVAL_MS = 5000;

export interface AssetListBoardProps {
  target: AssetBoardTarget;
  schema: ProductSchema;
  /** The client the stage built from the Schema, or a test double. */
  client?: AssetListClient;
  /** Test seam; polling defaults to 5s. */
  pollIntervalMs?: number;
}

interface BoardActionError {
  title: string;
  details: string[];
}

function documentVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

function failureText(failure: unknown): string {
  if (failure instanceof AssetListError) return failure.detail || failure.message;
  return failure instanceof Error ? failure.message : '';
}

/**
 * #329: the acceptance board covers the stage like 全部产出 does, because the
 * right drawer is too narrow for a whole list. The header carries the list's
 * title, state, count, the six filters and the board actions (#329d): accept
 * the selected items, return one item with its reasons, download the accepted
 * batch, or compare the results with the original picture. The grid shows
 * every item from one aggregate read with its cover, name, state, self-check
 * summary and steps. A 3D item can be rotated in place through the global
 * single viewer slot, so only one cell turns at a time. Every cell is a button
 * that opens the detail drawer; there is no hover-only action. While the list
 * is producing the board polls, pausedly while the tab is hidden.
 */
export function AssetListBoard({
  target, schema, client, pollIntervalMs = DEFAULT_POLL_INTERVAL_MS,
}: AssetListBoardProps) {
  const source = useMemo(() => client ?? assetListClient(schema), [client, schema]);
  const close = useStudioStore((state) => state.closeAssetBoard);
  const text = (key: string) => schemaText(schema, key);
  const [board, setBoard] = useState<AssetListBoard | null>(null);
  const [loading, setLoading] = useState(true);
  const [errorDetail, setErrorDetail] = useState('');
  const [refreshError, setRefreshError] = useState('');
  const [filter, setFilter] = useState<AssetListBoardFilter>('all');
  const [detailID, setDetailID] = useState<string | null>(null);
  const [view, setView] = useState<'grid' | 'compare'>('grid');
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set());
  const [focusedID, setFocusedID] = useState<string | null>(null);
  const [accepting, setAccepting] = useState(false);
  const [actionError, setActionError] = useState<BoardActionError | null>(null);
  const [returningID, setReturningID] = useState<string | null>(null);
  const [returnBusy, setReturnBusy] = useState(false);
  const [returnError, setReturnError] = useState('');
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState('');
  const [downloadTooLarge, setDownloadTooLarge] = useState(false);
  const [visible, setVisible] = useState(documentVisible);
  // The latest read wins: a slow first answer must not replace a newer read
  // (or land after the board is gone).
  const requestSeq = useRef(0);

  const load = useCallback(async (silent = false) => {
    const seq = requestSeq.current + 1;
    requestSeq.current = seq;
    if (!silent) {
      setLoading(true);
      setErrorDetail('');
    }
    setRefreshError('');
    try {
      const next = await source.getBoard(target.projectId, target.listId);
      if (requestSeq.current === seq) setBoard(next);
    } catch (failure) {
      if (requestSeq.current !== seq) return;
      const message = failureText(failure);
      if (silent) setRefreshError(message);
      else {
        setBoard(null);
        setErrorDetail(message);
      }
    } finally {
      if (!silent && requestSeq.current === seq) setLoading(false);
    }
  }, [source, target.projectId, target.listId]);

  useEffect(() => {
    void load();
    return () => {
      // Invalidate an in-flight read when the board or the list changes.
      requestSeq.current += 1;
    };
  }, [load]);

  // Poll only while the workspace is visible; a hidden tab stops the interval
  // and a visible one starts it again. The poll is silent: a transient read
  // failure never blanks a board that is already on screen.
  useEffect(() => {
    if (typeof document === 'undefined') return;
    const sync = () => setVisible(documentVisible());
    sync();
    document.addEventListener('visibilitychange', sync);
    return () => document.removeEventListener('visibilitychange', sync);
  }, []);
  const polling = boardPollingActive(board);
  useEffect(() => {
    if (!polling || !visible) return;
    const timer = window.setInterval(() => { void load(true); }, pollIntervalMs);
    return () => window.clearInterval(timer);
  }, [polling, visible, load, pollIntervalMs]);

  // Drop selected ids the newest read no longer contains.
  useEffect(() => {
    if (!board) return;
    const present = new Set(board.items.map((item) => item.id));
    setSelected((previous) => {
      const next = new Set([...previous].filter((id) => present.has(id)));
      return next.size === previous.size ? previous : next;
    });
  }, [board]);

  // Escape leaves the detail first, then the board; a prevented key (a popup
  // inside the drawer, or the return dialog) is left to that popup.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || isEditableTarget(event.target)) return;
      if (returningID) return;
      event.preventDefault();
      if (detailID) setDetailID(null);
      else close();
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [detailID, returningID, close]);

  const items = boardItems(board);
  const shown = filterBoardItems(items, filter);
  const cropItems = boardCropItems(items);
  const accepted = acceptedBoardItems(items);
  const compareView = view === 'compare' && cropItems.length > 0;
  const detailItem = detailID ? items.find((item) => item.id === detailID) ?? null : null;
  const returningItem = returningID ? items.find((item) => item.id === returningID) ?? null : null;
  const selectedReviewIDs = items
    .filter((item) => item.state === 'review' && selected.has(item.id))
    .map((item) => item.id);
  const downloadScope = boardDownloadScope(items, filter, selected);
  const canDownloadBatch = downloadTooLarge
    && downloadScope.length > 0
    && downloadScope.length < accepted.length;
  const title = board?.list.title || target.title;
  const listVersion = board?.list.version ?? 0;
  const stateLabel = board
    ? schema.asset_list?.states.find((state) => state.id === board.list.effective_status)?.label
      ?? board.list.effective_status
    : '';
  const loadFailedText = text(errorDetail) || errorDetail || text('ui.asset_list.board.load_failed');

  const reloadForStale = useCallback(async (failure: unknown) => {
    await load(true);
    const detail = failureText(failure);
    setActionError({
      title: text('ui.asset_list.board.stale'),
      details: detail ? [detail] : [],
    });
  }, [load, schema]);

  const acceptSelected = async () => {
    if (!board || selectedReviewIDs.length === 0 || accepting) return;
    setAccepting(true);
    setActionError(null);
    try {
      const result = await source.review(target.projectId, target.listId, {
        decision: 'accept',
        item_ids: selectedReviewIDs,
        expected_version: listVersion,
      });
      const refused = result.items.filter((entry) => entry.error);
      const names = new Map(items.map((item) => [item.id, item.name]));
      setSelected(new Set(refused.map((entry) => entry.item_id)));
      await load(true);
      if (refused.length > 0) {
        setActionError({
          title: text('ui.asset_list.board.accept_partial'),
          details: refused.map((entry) => {
            const name = names.get(entry.item_id) || entry.item_id;
            return formatSchemaText(text('ui.asset_list.board.item_error'), {
              name, detail: entry.error ?? '',
            });
          }),
        });
      }
    } catch (failure) {
      if (failure instanceof AssetListError && failure.status === 409
        && failure.code !== 'asset_list_rework_limit') {
        await reloadForStale(failure);
      } else {
        setActionError({
          title: text('ui.asset_list.decide_failed'),
          details: [failureText(failure)].filter(Boolean),
        });
      }
    } finally {
      setAccepting(false);
    }
  };

  const submitReturn = async (reasonIDs: string[], note: string) => {
    if (!board || !returningItem || returnBusy) return;
    setReturnBusy(true);
    setReturnError('');
    try {
      const result = await source.review(target.projectId, target.listId, {
        decision: 'return',
        item_ids: [returningItem.id],
        reason_ids: reasonIDs,
        ...(note ? { note } : {}),
        expected_version: listVersion,
      });
      const refused = result.items.find((entry) => entry.error);
      await load(true);
      if (refused?.error) {
        setReturnError(refused.error);
        return;
      }
      setReturningID(null);
      setSelected((previous) => {
        const next = new Set(previous);
        next.delete(returningItem.id);
        return next;
      });
    } catch (failure) {
      const status = failure instanceof AssetListError ? failure.status : 0;
      const code = failure instanceof AssetListError ? failure.code : '';
      if (status === 409 && code !== 'asset_list_rework_limit') {
        // The board moved under the dialog: close it and read the newest state.
        setReturningID(null);
        await reloadForStale(failure);
      } else {
        setReturnError(failureText(failure) || text('ui.asset_list.decide_failed'));
        if (status === 409) await load(true);
      }
    } finally {
      setReturnBusy(false);
    }
  };

  const download = async (itemIDs: readonly string[]) => {
    if (downloading) return;
    setDownloading(true);
    setDownloadError('');
    try {
      const archive = await source.downloadArchive(target.projectId, target.listId, itemIDs);
      saveArchive(archive.blob, archive.filename);
      setDownloadTooLarge(false);
    } catch (failure) {
      setDownloadError(failureText(failure) || text('ui.asset_list.board.download_failed'));
      setDownloadTooLarge(failure instanceof AssetListError && failure.status === 413);
    } finally {
      setDownloading(false);
    }
  };

  const toggleSelected = (itemID: string) => {
    setSelected((previous) => {
      const next = new Set(previous);
      if (next.has(itemID)) next.delete(itemID);
      else next.add(itemID);
      return next;
    });
  };

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

      {board !== null ? (
        <div
          role="group"
          aria-label={text('ui.asset_list.board.actions')}
          className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2"
          data-testid="asset-board-actions"
        >
          <Action
            type="button"
            size="sm"
            className="min-h-11 px-4"
            disabled={selectedReviewIDs.length === 0 || accepting}
            loading={accepting}
            onClick={() => void acceptSelected()}
            data-testid="asset-board-accept-selected"
          >
            {accepting
              ? text('ui.asset_list.board.accepting')
              : schemaCountMessage(schema, 'ui.asset_list.board.accept_selected', selectedReviewIDs.length, {
                count: selectedReviewIDs.length,
              })}
          </Action>
          <span className="tabular-nums text-[11px] text-muted-foreground" data-testid="asset-board-selected-count">
            {schemaCountMessage(schema, 'ui.asset_list.board.selected_count', selected.size, { count: selected.size })}
          </span>
          <span className="ml-auto flex flex-wrap items-center gap-2">
            {cropItems.length > 0 ? (
              <Action
                type="button"
                variant="outline"
                size="sm"
                className="min-h-11 px-4"
                aria-pressed={compareView}
                onClick={() => setView(compareView ? 'grid' : 'compare')}
                data-testid="asset-board-compare-toggle"
              >
                {text(compareView ? 'ui.asset_list.board.compare_close' : 'ui.asset_list.board.compare_open')}
              </Action>
            ) : null}
            {accepted.length > 0 ? (
              <Action
                type="button"
                variant="outline"
                size="sm"
                className="min-h-11 px-4"
                disabled={downloading}
                loading={downloading}
                onClick={() => void download([])}
                data-testid="asset-board-download"
              >
                {!downloading ? <Download aria-hidden /> : null}
                {downloading ? text('ui.asset_list.board.downloading') : text('ui.asset_list.board.download_all')}
              </Action>
            ) : null}
          </span>
          {downloadError ? (
            <div role="alert" className="flex basis-full flex-wrap items-center gap-2 text-xs text-destructive" data-testid="asset-board-download-error">
              <span className="min-w-0 [overflow-wrap:anywhere]">{downloadError}</span>
              {canDownloadBatch ? (
                <Action
                  type="button"
                  variant="outline"
                  size="sm"
                  className="min-h-11 px-4"
                  disabled={downloading}
                  onClick={() => void download(downloadScope)}
                  data-testid="asset-board-download-batch"
                >
                  {text('ui.asset_list.board.download_batch')}
                </Action>
              ) : downloadTooLarge ? (
                <span className="min-w-0 text-muted-foreground [overflow-wrap:anywhere]">
                  {text('ui.asset_list.board.download_scope_hint')}
                </span>
              ) : null}
            </div>
          ) : null}
          {actionError ? (
            <div role="alert" className="flex basis-full flex-col gap-1 text-xs text-destructive" data-testid="asset-board-action-error">
              <span className="font-medium">{actionError.title}</span>
              {actionError.details.map((detail, index) => (
                <span key={`${detail}-${index}`} className="[overflow-wrap:anywhere]">{detail}</span>
              ))}
              <Action
                type="button"
                variant="ghost"
                size="sm"
                className="min-h-11 self-start px-4"
                onClick={() => setActionError(null)}
                data-testid="asset-board-action-error-close"
              >
                {text('ui.common.close')}
              </Action>
            </div>
          ) : null}
          {refreshError ? (
            <div role="alert" className="flex basis-full flex-wrap items-center gap-2 text-xs text-destructive" data-testid="asset-board-refresh-error">
              <span className="min-w-0 [overflow-wrap:anywhere]">{refreshError}</span>
              <span className="text-muted-foreground">{text('ui.asset_list.board.refresh_failed')}</span>
              <Action
                type="button"
                variant="outline"
                size="sm"
                className="min-h-11 px-4"
                onClick={() => void load(true)}
                data-testid="asset-board-refresh-now"
              >
                {text('ui.asset_list.board.refresh_now')}
              </Action>
            </div>
          ) : null}
        </div>
      ) : null}

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
      ) : compareView ? (
        <AssetBoardCompare
          schema={schema}
          items={items}
          shown={shown}
          focusedID={focusedID}
          onFocus={setFocusedID}
        />
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
                selected={selected.has(item.id)}
                busy={accepting || returnBusy}
                detailOpen={detailItem?.id === item.id}
                onToggleSelect={() => toggleSelected(item.id)}
                onReturn={() => {
                  setReturnError('');
                  setReturningID(item.id);
                }}
                onOpen={() => setDetailID(item.id)}
              />
            ))}
          </ul>
        </div>
      )}

      {detailItem ? (
        <AssetBoardDetail item={detailItem} schema={schema} onClose={() => setDetailID(null)} />
      ) : null}

      {returningItem ? (
        <AssetBoardReturnDialog
          key={returningItem.id}
          item={returningItem}
          schema={schema}
          reasons={returnReasonsForKind(schema, returningItem.kind)}
          busy={returnBusy}
          error={returnError}
          onSubmit={(reasonIDs, note) => void submitReturn(reasonIDs, note)}
          onClose={() => {
            if (returnBusy) return;
            setReturningID(null);
            setReturnError('');
          }}
        />
      ) : null}
    </div>
  );
}

interface BoardCellProps {
  item: AssetListBoardItem;
  schema: ProductSchema;
  selected: boolean;
  /** A board decision is in flight; the cell's own actions wait. */
  busy: boolean;
  /** The drawer shows this item's viewer instead of the cell. */
  detailOpen: boolean;
  onToggleSelect: () => void;
  onReturn: () => void;
  onOpen: () => void;
}

/** One grid cell: cover and text open the details, the 3D button rotates the model in place. */
function BoardCell({
  item, schema, selected, busy, detailOpen, onToggleSelect, onReturn, onOpen,
}: BoardCellProps) {
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
      <label className="mb-2 flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-1 text-[11px] text-muted-foreground hover:bg-muted">
        <input
          type="checkbox"
          className="size-4 shrink-0"
          checked={selected}
          disabled={busy}
          onChange={onToggleSelect}
          aria-label={formatSchemaText(text('ui.asset_list.board.select_item'), { name: item.name })}
          data-testid={`asset-board-select-${item.id}`}
        />
        <span>{text('ui.asset_list.board.select_short')}</span>
      </label>

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
              <img src={coverURL} alt={''} loading="lazy" decoding="async" className="size-full object-cover" />
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

      {(modelURL || item.state === 'review') ? (
        <div className="mt-2 flex flex-wrap items-center justify-end gap-2">
          {item.state === 'review' ? (
            <Action
              type="button"
              variant="outline"
              size="sm"
              className="min-h-11 px-4"
              disabled={busy}
              onClick={onReturn}
              aria-label={formatSchemaText(text('ui.asset_list.board.return_item_label'), { name: item.name })}
              data-testid={`asset-board-return-${item.id}`}
            >
              {text('ui.asset_list.board.return_item')}
            </Action>
          ) : null}
          {modelURL ? (
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
          ) : null}
        </div>
      ) : null}
    </li>
  );
}