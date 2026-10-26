'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Download, Image as ImageIcon, LayoutGrid, Package, X } from 'lucide-react';
import { toast } from 'sonner';

import { CardDownload, ResultContextMenu, openResultDetail, useCanvasDragSource } from '@/components/result/result-card-parts';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Checkbox } from '@/components/ui/checkbox';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useSessionPick } from '@/hooks/use-session-pick';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { isEditableTarget } from '@/lib/workspace/stage-keys';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import {
  buildArchive,
  canSaveArchiveToDisk,
  planBatchExport,
  saveArchive,
  saveArchiveToDisk,
  type ArchiveProgress,
  type ArchiveSummary,
} from '@/lib/workspace/batch-export';
import { clearSelection, escapeClearsSelection, groupByDay, selectionAfterClick } from '@/lib/workspace/output-groups';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { targetOfTile } from '@/lib/workspace/result-target';
import { versionCaption, versionLabel } from '@/lib/workspace/version-label';
import { versionStatusLabel, type VersionTile } from '@/lib/workspace/versions';
import { cn } from '@/lib/utils';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

type Filter = 'all' | 'image' | 'model';

/**
 * 全部产出：盖住舞台的网格，列出本会话所有 2D / 3D 版本，可以按 2D、3D 过滤。
 * 单击一格打开共用的任务详情；多选只用格子左上角的勾选框（Shift 连选），选中后顶部出现批量栏（下载、取消选择）。
 * 悬停显示下载；右键菜单有详情、在画布中打开、下载；已完成的图片可拖到画布上。进行中和失败的版本也在这里，可看详情，不能选中。
 */
export function AllOutputs() {
  const { schema } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const object = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  const versions = useSessionVersions();
  const setAllOutputsOpen = useStudioStore((state) => state.setAllOutputsOpen);
  const [filter, setFilter] = useState<Filter>('all');
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [packing, setPacking] = useState(false);
  const [packed, setPacked] = useState<ArchiveProgress | null>(null);
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick } = useSessionPick(chat, sessionId);
  // The checkbox last clicked: a Shift range runs from it to the next one.
  const anchor = useRef<string | null>(null);
  const currentKey = tileOfObject(versions, object)?.key ?? null;
  const counts = useMemo(() => ({
    all: versions.tiles.length,
    image: versions.tiles.filter((tile) => tile.domain === 'image').length,
    model: versions.tiles.filter((tile) => tile.domain === 'model').length,
  }), [versions.tiles]);
  const shown = versions.tiles.filter((tile) => filter === 'all' || tile.domain === filter);
  const filters: Array<[Filter, string]> = [['all', schemaText(schema, 'ui.studio.outputs.filter_all')], ['image', schemaText(schema, 'ui.studio.outputs.filter_2d')], ['model', schemaText(schema, 'ui.studio.outputs.filter_3d')]];

  // Only finished versions have files to take; the selection follows what is still in the list.
  const selectable = shown.filter((tile) => tile.status === 'ready');
  const chosen = selectable.filter((tile) => selected.has(tile.key));
  const allChosen = selectable.length > 0 && chosen.length === selectable.length;
  const groups = useMemo(() => groupByDay(shown, (tile) => tile.createdAt), [shown]);
  // The order the tiles are on the page: what a Shift range runs over.
  const pageOrder = groups.flatMap((group) => group.items.filter((tile) => tile.status === 'ready').map((tile) => tile.key));
  const toggle = (key: string, shift: boolean) => {
    setSelected((current) => selectionAfterClick(pageOrder, current, anchor.current, key, shift));
    anchor.current = key;
  };
  // Esc drops the selection first; it runs in the capture phase so the stage's own Esc (leave focus mode) does not also fire.
  const chosenCount = chosen.length;
  useEffect(() => {
    if (chosenCount === 0) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return;
      const blockers = {
        lightbox: Boolean(useStudioStore.getState().lightbox),
        popupOpen: document.querySelector('[data-base-ui-portal] [role=dialog][data-open], [data-base-ui-portal] [role=menu][data-open], [data-base-ui-portal] [role=listbox][data-open], [role=alertdialog]') !== null,
        editable: isEditableTarget(event.target),
      };
      if (!escapeClearsSelection(chosenCount, blockers)) return;
      event.stopPropagation();
      setSelected(clearSelection());
      anchor.current = null;
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [chosenCount]);
  const toggleAll = () => setSelected((current) => {
    const next = new Set(current);
    for (const tile of selectable) { if (allChosen) next.delete(tile.key); else next.add(tile.key); }
    return next;
  });

  // One file per version: the candidate that was picked there, or the one the stage would show.
  const chosenAssetId = (tile: VersionTile): string | undefined => {
    if (pick?.kind === 'image' && pick.assetId && tile.taskIds.includes(pick.taskId ?? '')) return pick.assetId;
    return tile.key === currentKey ? object?.assetId : undefined;
  };
  const zipName = (count: number) => `${schemaText(schema, 'ui.studio.outputs.zip_name')}-${count}.zip`;
  const pack = async (entries: ReturnType<typeof planBatchExport>) => {
    if (entries.length === 0 || packing) return;
    setPacking(true);
    setPacked({ done: 0, total: entries.length, bytes: 0 });
    const progress = (next: ArchiveProgress) => setPacked(next);
    try {
      // Where the browser can write straight to a file, one entry is held in
      // memory at a time. Otherwise the archive is built as a browser-managed
      // Blob (still one pass, no JSZip copy) and handed to the download.
      let summary: ArchiveSummary | null = null;
      if (canSaveArchiveToDisk()) summary = await saveArchiveToDisk(entries, zipName(entries.length), progress);
      if (!summary) {
        const archive = await buildArchive(entries, undefined, 4, progress);
        if (!archive) { toast.error(schemaText(schema, 'ui.studio.outputs.zip_failed')); return; }
        saveArchive(archive.blob, zipName(archive.added));
        summary = archive;
      }
      if (summary.failed > 0) toast.warning(schemaCountMessage(schema, 'ui.studio.outputs.zip_partial', summary.failed, { count: summary.failed }));
    } catch (cause) {
      // Dismissing the save dialog is not a packaging failure.
      if (!(cause instanceof DOMException && cause.name === 'AbortError')) toast.error(schemaText(schema, 'ui.studio.outputs.zip_failed'));
    } finally {
      setPacking(false);
      setPacked(null);
    }
  };
  const labelOf = (tile: VersionTile) => versionLabel(schema, tile, tile.domain);
  const downloadSelected = () => pack(planBatchExport(versions.tiles, new Set(chosen.map((tile) => tile.key)), labelOf, { scope: 'chosen', chosenAssetId }));
  // A version that came out as a group (candidates, three views) can also be taken whole.
  const downloadGroup = (tile: VersionTile) => pack(planBatchExport(versions.tiles, new Set([tile.key]), labelOf));

  const detailOf = (tile: VersionTile) => {
    if (!sessionId) return;
    // A candidate group shows the candidate that was picked there.
    const picked = pick?.kind === 'image' && pick.taskId && tile.taskIds.includes(pick.taskId) ? { taskId: pick.taskId, assetId: pick.assetId } : {};
    openResultDetail(targetOfTile(tile, sessionId, labelOf(tile), picked, '', schema));
  };

  return (
    <div role="region" aria-label={schemaText(schema, 'ui.studio.outputs.title')} data-testid="all-outputs" className="absolute inset-0 z-float flex flex-col bg-canvas">
      <header className="flex h-11 shrink-0 items-center gap-3 border-b border-border px-3">
        <LayoutGrid aria-hidden className="size-4 text-primary" />
        <h2 className="text-sm font-semibold">{schemaText(schema, 'ui.studio.outputs.title')}</h2>
        <div role="group" aria-label={schemaText(schema, 'ui.studio.outputs.filter')} className="flex items-center gap-1">
          {filters.map(([value, label]) => (
            <button
              key={value}
              type="button"
              aria-pressed={filter === value}
              onClick={() => setFilter(value)}
              data-testid={`all-outputs-filter-${value}`}
              className={cn(
                'h-7 rounded-md px-2.5 text-xs transition-colors focus-visible:outline-2 focus-visible:outline-ring',
                filter === value ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              )}
            >
              {label}<span className="ml-1 tabular-nums">{counts[value]}</span>
            </button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-2">
          {selectable.length > 0 ? (
            <label className="flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground">
              <Checkbox checked={allChosen} indeterminate={chosen.length > 0 && !allChosen} onCheckedChange={toggleAll} data-testid="all-outputs-select-all" />
              {schemaText(schema, allChosen ? 'ui.studio.outputs.select_none' : 'ui.studio.outputs.select_all')}
            </label>
          ) : null}
        </div>
        <HintTooltip name={schemaText(schema, 'ui.studio.outputs.close')}>
          <Action type="button" variant="ghost" size="icon-sm" className="text-muted-foreground" onClick={() => setAllOutputsOpen(false)} aria-label={schemaText(schema, 'ui.studio.outputs.close')}>
            <X />
          </Action>
        </HintTooltip>
      </header>
      {chosen.length > 0 ? (
        <div className="flex h-10 shrink-0 items-center gap-3 border-b border-border bg-primary/10 px-3 text-xs" data-testid="all-outputs-bulk-bar">
          <span className="font-medium tabular-nums" role="status">{schemaCountMessage(schema, 'ui.studio.outputs.selected_count', chosen.length, { count: chosen.length })}</span>
          <Action type="button" variant="outline" size="sm" icon={<Download />} loading={packing} onClick={() => void downloadSelected()} data-testid="all-outputs-download">
            {packing && packed
              ? formatSchemaText(schemaText(schema, 'ui.studio.outputs.zip_progress'), { done: packed.done, total: packed.total })
              : schemaText(schema, 'ui.studio.outputs.download')}
          </Action>
          <Action type="button" variant="ghost" size="sm" onClick={() => { setSelected(clearSelection()); anchor.current = null; }} data-testid="all-outputs-clear">
            {schemaText(schema, 'ui.studio.outputs.clear_selection')}
          </Action>
        </div>
      ) : null}
      {shown.length === 0 ? (
        <p className="p-6 text-sm text-muted-foreground">{schemaText(schema, 'ui.studio.outputs.empty')}</p>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-3" data-testid="all-outputs-scroll">
          {groups.map((group) => {
            const heading = group.kind === 'date'
              ? formatSchemaText(schemaText(schema, 'ui.studio.outputs.group_date'), { year: group.year, month: group.month, day: group.dayOfMonth })
              : schemaText(schema, group.kind === 'today' ? 'ui.studio.outputs.group_today' : 'ui.studio.outputs.group_yesterday');
            return (
              <section key={group.day} aria-label={heading} data-testid="all-outputs-day" data-day={group.day}>
                <h3 className="sticky top-0 z-10 mb-2 flex items-baseline gap-2 bg-canvas/95 py-1 text-xs font-semibold" data-testid="all-outputs-day-title">
                  {heading}
                  <span className="font-normal tabular-nums text-muted-foreground">{group.items.length}</span>
                </h3>
                <ul className="grid grid-cols-2 content-start gap-3 @2xl:grid-cols-4 @5xl:grid-cols-6">
                  {group.items.map((tile) => {
                    const first = tile.domain === 'model' ? tile.thumbnailUrl : tile.assets[0]?.url ?? '';
                    // #290: cards show the downscaled preview of a task picture.
                    const src = first ? previewAssetUrl(first) : '';
                    const label = labelOf(tile);
                    const caption = versionCaption(schema, tile, versions.tiles);
                    const whole = tile.status === 'ready' && tile.domain === 'image' && tile.assets.filter((asset) => asset.kind === 'image').length > 1;
                    return (
                      <OutputCard
                        key={tile.key}
                        tile={tile}
                        sessionId={sessionId ?? ''}
                        label={label}
                        selected={selected.has(tile.key)}
                        current={tile.key === currentKey}
                        schemaCaption={caption}
                        src={src}
                        whole={whole}
                        packing={packing}
                        onCheck={(shift) => toggle(tile.key, shift)}
                        onDetail={() => detailOf(tile)}
                        onGroupDownload={() => void downloadGroup(tile)}
                      />
                    );
                  })}
                </ul>
              </section>
            );
          })}
        </div>
      )}
      {versions.hasEarlier ? <div className="shrink-0 border-t border-border p-3">
        <Action type="button" variant="outline" size="sm" loading={versions.loadingEarlier}
          onClick={() => void versions.loadEarlier().catch(() => toast.error(schemaText(schema, 'ui.studio.strip.earlier_failed')))} data-testid="outputs-load-earlier">
          {schemaText(schema, versions.loadingEarlier ? 'ui.studio.strip.loading_earlier' : 'ui.studio.strip.load_earlier')}
        </Action>
      </div> : null}
    </div>
  );
}

interface OutputCardProps {
  tile: VersionTile;
  sessionId: string;
  label: string;
  selected: boolean;
  current: boolean;
  schemaCaption: string;
  src: string;
  whole: boolean;
  packing: boolean;
  onCheck: (shift: boolean) => void;
  onDetail: () => void;
  onGroupDownload: () => void;
}

/** One card: click for details, the checkbox to select, hover for download, right-click for the menu, drag onto the canvas. */
function OutputCard({ tile, sessionId, label, selected, current, schemaCaption, src, whole, packing, onCheck, onDetail, onGroupDownload }: OutputCardProps) {
  const { schema } = useProductSchema();
  const shiftHeld = useRef(false);
  const target = targetOfTile(tile, sessionId, label, {}, '', schema);
  const dragSource = useCanvasDragSource(target);
  return (
    <ResultContextMenu target={target} render={<li className="group/card relative" data-testid="all-outputs-card" />}>
      {tile.status === 'ready' ? (
        <HintTooltip
          name={formatSchemaText(schemaText(schema, 'ui.studio.outputs.select_tile'), { label })}
          hint={schemaText(schema, 'ui.studio.outputs.range_hint')}
          className="absolute left-3 top-3 z-10"
        >
          <Checkbox
            checked={selected}
            onClickCapture={(event) => { shiftHeld.current = event.shiftKey; }}
            onCheckedChange={() => { const shift = shiftHeld.current; shiftHeld.current = false; onCheck(shift); }}
            aria-label={formatSchemaText(schemaText(schema, 'ui.studio.outputs.select_tile'), { label })}
            data-testid="all-outputs-check"
            className="bg-panel/90 shadow-sm"
          />
        </HintTooltip>
      ) : null}
      <CardDownload target={target} className="absolute right-3 top-3 z-10" />
      {whole ? (
        <HintTooltip name={formatSchemaText(schemaText(schema, 'ui.studio.outputs.download_group'), { label })} className="absolute right-11 top-3 z-10">
          <Action
            type="button"
            variant="media"
            size="icon-xs"
            disabled={packing}
            onClick={onGroupDownload}
            aria-label={formatSchemaText(schemaText(schema, 'ui.studio.outputs.download_group'), { label })}
            data-testid="all-outputs-download-group"
            className="opacity-0 transition-opacity group-focus-within/card:opacity-100 group-hover/card:opacity-100"
          >
            <Package />
          </Action>
        </HintTooltip>
      ) : null}
      <button
        type="button"
        onClick={onDetail}
        {...dragSource}
        data-testid="all-outputs-tile"
        data-kind={tile.domain}
        data-selected={selected ? 'true' : undefined}
        aria-current={current ? 'true' : undefined}
        className={cn(
          'group flex w-full flex-col gap-1.5 rounded-lg p-1.5 text-left transition-colors hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring',
          current && 'bg-primary/10 ring-2 ring-primary',
          selected && 'bg-primary/15 ring-2 ring-primary',
        )}
      >
        <span className="relative flex aspect-square w-full items-center justify-center overflow-hidden rounded-md bg-muted">
          {src ? <img src={src} alt={''} loading="lazy" decoding="async" draggable={false} className="size-full object-cover" /> : tile.domain === 'model' ? <Box aria-hidden className="size-8 text-muted-foreground" /> : <ImageIcon aria-hidden className="size-8 text-muted-foreground" />}
          {tile.status !== 'ready' ? (
            <span className="absolute inset-x-0 bottom-0 bg-panel/85 px-2 py-1 text-xs">{versionStatusLabel(schema, tile)}</span>
          ) : null}
        </span>
        <span className="flex items-center gap-1.5 text-xs">
          <span className="font-medium">{label}</span>
          {schemaCaption ? <span className="truncate text-muted-foreground">{schemaCaption}</span> : null}
        </span>
      </button>
    </ResultContextMenu>
  );
}
