'use client';

import { memo, useCallback, useEffect, useMemo, useRef } from 'react';
import { Popover } from '@base-ui/react/popover';
import { AlertTriangle, Box, Brush, Camera, Check, ChevronUp, Columns2, Crop, Eraser, Grid2x2, Link2Off, Maximize2, Merge, Paintbrush, Paperclip, Wand2, type LucideIcon } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { KindBadge } from '@/components/studio/kind-badge';
import { ObjectThumbImage } from '@/components/studio/object-thumb';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { anchoredPositionMethod, useAnchoredPositioner } from '@/components/ui/overlay';
import { CardDownload, ResultContextMenu, openResultDetail, useCanvasDragSource } from '@/components/result/result-card-parts';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useSessionMessages } from '@/hooks/use-picture-handles';
import { useSessionPick } from '@/hooks/use-session-pick';
import { useStageViewer } from '@/hooks/use-stage-viewer';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { compareFromGroup, slotKey, type CompareSlot } from '@/lib/workspace/compare';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { groupProgress } from '@/lib/workspace/three-view';
import { imageResultSlots, orderedImageResults, type ImageResultSlot } from '@/lib/workspace/image-result-group';
import { handleLabel, handleOfPicture } from '@/lib/workspace/object-handle';
import { targetOfTile } from '@/lib/workspace/result-target';
import { objectOfTile, type TilePick } from '@/lib/workspace/version-object';
import { versionCaption, versionLabel, versionTitle } from '@/lib/workspace/version-label';
import { branchOf, foldColumn, layoutColumns, shownVersionTiles, versionStatusLabel, type LinkClass, type VersionColumn, type VersionParent, type VersionTile } from '@/lib/workspace/versions';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { keepUnchanged } from '@/lib/core/keep-unchanged';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

/**
 * The strip is exactly 144px (h-36). A regular card with its 「详情」 button row is 112px (#593) and the list pads it
 * by 12px above and below (#546); with the 1px top border and a thin horizontal scrollbar (11px in Chromium) the
 * scrollbar only eats into the bottom padding, never the detail button, and the strip does not change height when one
 * appears (#51).
 * One column may hold two compact tiles.
 */
const LINK_ICONS: Record<LinkClass, LucideIcon> = {
  to_model: Box,
  capture: Camera,
  process: Wand2,
  edit: Paintbrush,
  inpaint: Brush,
  remove_background: Eraser,
  reference: Paperclip,
  merge: Merge,
  crop: Crop,
  unknown: Link2Off,
};

const RING_RADIUS = 15;
const RING_LENGTH = 2 * Math.PI * RING_RADIUS;

/** 进行中的版本：缩略图上一圈进度；排队中（还没有进度）画成缓慢旋转的虚线圈。 */
function ProgressRing({ progress, pending, label }: { progress: number; pending: boolean; label: string }) {
  return (
    <span role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pending ? undefined : Math.round(progress * 100)} className="absolute inset-0 flex items-center justify-center bg-panel/70">
      <svg viewBox="0 0 36 36" className={cn('size-8 -rotate-90', pending && 'animate-spin [animation-duration:3s]')} aria-hidden>
        <circle cx="18" cy="18" r={RING_RADIUS} fill="none" strokeWidth="3" className="stroke-border" />
        <circle
          cx="18" cy="18" r={RING_RADIUS} fill="none" strokeWidth="3" strokeLinecap="round"
          className="stroke-primary"
          strokeDasharray={pending ? `${RING_LENGTH / 8} ${RING_LENGTH / 8}` : RING_LENGTH}
          strokeDashoffset={pending ? 0 : RING_LENGTH * (1 - Math.min(Math.max(progress, 0), 1))}
        />
      </svg>
    </span>
  );
}

const VIEW_PIP: Record<string, string> = {
  succeeded: 'bg-success',
  running: 'bg-primary animate-pulse',
  starting: 'bg-primary animate-pulse',
  awaiting_approval: 'bg-warning',
  failed: 'bg-destructive',
  denied: 'bg-destructive',
  canceled: 'bg-destructive',
};

/**
 * #87: the chat handle of a tile that shows one picture (never a handle the messages do not give; a group or
 * candidate grid shows several pictures, so none). #617: a compact tile, the one a 「更多」 group keeps outside,
 * has it too.
 */
function useTileHandle(tile: VersionTile): string {
  const messages = useSessionMessages(useChatStore((state) => state.activeSessionId));
  const single = tile.domain === 'image' && tile.status === 'ready' && tile.slots.length === 0 && tile.views.length === 0
    && tile.assets.length === 1 && Boolean(tile.assets[0].url);
  return single ? handleOfPicture(messages, { url: tile.assets[0].url }) : '';
}

/** The handle badge: over the thumbnail of a regular tile, beside the version label of a compact one. */
function HandleBadge({ handle, className }: { handle: string; className?: string }) {
  const { schema } = useProductSchema();
  return (
    <span
      aria-label={formatSchemaText(schemaText(schema, 'ui.studio.handle_badge.label'), { handle })}
      className={cn('pointer-events-none rounded-sm bg-black/60 px-0.5 font-mono text-[9px] leading-3 text-white', className)}
      data-testid="version-handle-badge"
    >{handleLabel(handle)}</span>
  );
}

function Thumb({ tile, size, handle }: { tile: VersionTile; size: 'regular' | 'compact'; handle: string }) {
  const { schema } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  // #290: a tile is 28–56 px; it shows the downscaled preview, not the 2K–4K original.
  const resolve = (url: string | undefined) => (url ? previewAssetUrl(url) : '');
  const imageUrls = orderedImageResults(tile.assets, imageResultSlots(schema, tile.capability)).map((asset) => asset.url);
  const frontAsset = tile.slots.length > 0
    ? tile.assets.find((asset) => asset.id === tile.slots.find((slot) => slot.taskId === tile.taskId)?.assetId)
    : undefined;
  // A candidate group (several pictures of one result) reads as a 2x2 grid; a three-view group shows its front view.
  const grid = tile.domain === 'image' && tile.slots.length === 0 && tile.views.length === 0 && imageUrls.length > 1
    ? imageUrls.slice(0, 4).map(resolve)
    : null;
  const source = tile.domain === 'model'
    ? tile.thumbnailUrl
    : (frontAsset?.url ?? (tile.slots.length > 0 ? '' : tile.assets[0]?.url)) ?? '';
  const src = resolve(source);
  const text = (key: string) => schemaText(schema, `ui.studio.strip.${key}`);
  const dimension = size === 'regular' ? 'size-14' : 'size-7';
  // #633: the three-view card's count — the right view joins it once asked for, so a three-view group reads 3/3.
  const progress = groupProgress(tile.views);
  return (
    <span className={cn('relative flex shrink-0 items-center justify-center overflow-hidden rounded-md bg-muted', dimension)} data-testid="version-thumb" data-grid={grid ? grid.length : undefined}>
      {grid ? (
        <span className="grid size-full grid-cols-2 grid-rows-2 gap-px bg-background">
          {[0, 1, 2, 3].map((index) => (
            grid[index]
              ? <img key={index} src={grid[index]} alt={''} loading="lazy" decoding="async" className="size-full object-cover" />
              : <span key={index} className="bg-muted" />
          ))}
        </span>
      ) : src || tile.domain === 'model'
        // #624: a 3D version without a preview (or whose preview does not load) shows the 3D placeholder.
        ? <ObjectThumbImage kind={tile.domain} src={src} />
        : null}
      {tile.status === 'pending' || tile.status === 'running'
        ? <ProgressRing progress={tile.progress} pending={tile.status === 'pending'} label={text(tile.status === 'pending' ? 'pending' : 'running')} />
        : null}
      {tile.status === 'failed' ? (
        <span className="absolute inset-0 flex items-center justify-center bg-destructive/15" data-testid="version-failed-badge">
          <AlertTriangle aria-hidden className="size-4 text-destructive" />
        </span>
      ) : null}
      {tile.views.length > 1 && size === 'regular' ? (
        <span className="absolute inset-x-0 bottom-0 flex items-center justify-between gap-1 bg-panel/85 px-1 py-0.5" data-testid="version-view-pips">
          <span className="flex items-center gap-0.5" aria-hidden>
            {tile.views.map((view) => <span key={view.key} className={cn('size-1.5 rounded-full', VIEW_PIP[view.status] ?? 'bg-muted-foreground/40')} />)}
          </span>
          <span className="text-[9px] font-semibold leading-3" data-testid="version-group-count">
            {formatSchemaText(text('group_progress'), progress)}
          </span>
        </span>
      ) : size === 'regular' && tile.slots.length > 1 ? (
        <span className="absolute bottom-0.5 right-0.5 rounded-sm bg-panel/90 px-1 text-[9px] font-semibold leading-3">{tile.slots.length}</span>
      ) : size === 'regular' && tile.domain === 'image' && tile.assets.length > 1 ? (
        <span className="absolute bottom-0.5 right-0.5 rounded-sm bg-panel/90 px-1 text-[9px] font-semibold leading-3" data-testid="version-candidate-count">{tile.assets.length}</span>
      ) : null}
      {/* #87: a regular tile of one picture carries that picture's chat handle in its top-right corner. */}
      {handle && size === 'regular' ? <HandleBadge handle={handle} className="absolute right-0.5 top-0.5" /> : null}
    </span>
  );
}

interface Candidate {
  key: string;
  taskId: string;
  assetId: string;
  label: string;
  url: string;
  variant?: string;
}

function candidatesOf(tile: VersionTile, indexLabel: (index: number) => string, slots: readonly ImageResultSlot[] = []): Candidate[] {
  if (tile.domain !== 'image') return [];
  if (tile.slots.length > 1) {
    return tile.slots.flatMap((slot) => {
      const asset = tile.assets.find((entry) => entry.id === slot.assetId);
      return asset ? [{ key: slot.taskId, taskId: slot.taskId, assetId: asset.id, label: slot.label || slot.slot, url: asset.url }] : [];
    });
  }
  const images = orderedImageResults(tile.assets, slots);
  if (images.length < 2) return [];
  return images.map((asset, index) => ({ key: asset.id, taskId: tile.taskId, assetId: asset.id, variant: asset.variant, label: slots.find((slot) => slot.variant === asset.variant)?.label ?? indexLabel(index + 1), url: asset.url }));
}

/**
 * 一格版本里的候选 / 分组成员：展开后可以逐个查看，也可以改选用哪一张。
 * #290: the strip has a popover per version, so only the trigger lives here; what the open popup needs (the
 * session's versions, its pick, the board's layers) is read by `CandidateList`, mounted only while it is open.
 * Reading them in every tile re-derived every version once per tile on each task or board update.
 */
function CandidatePopover({ tile, sessionId, current }: { tile: VersionTile; sessionId: string; current: boolean }) {
  const { schema } = useProductSchema();
  const layer = useAnchoredPositioner();
  if (candidatesOf(tile, String, imageResultSlots(schema, tile.capability)).length === 0) return null;
  return (
    <Popover.Root>
      <HintTooltip name={schemaText(schema, 'ui.studio.strip.expand')} className="absolute -right-1 -top-1 z-sticky">
        <Popover.Trigger
          aria-label={schemaText(schema, 'ui.studio.strip.expand')}
          data-testid="version-expand"
          className="flex size-4 items-center justify-center rounded-full border border-border bg-panel-raised text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:border-primary data-popup-open:text-primary"
        >
          <ChevronUp aria-hidden className="size-2.5" />
        </Popover.Trigger>
      </HintTooltip>
      <Popover.Portal>
        <Popover.Positioner positionMethod={anchoredPositionMethod} side="top" align="center" sideOffset={8} collisionPadding={12} className={layer}>
          <Popover.Popup className="flex max-w-(--available-width) flex-col gap-1.5 rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10" data-testid="version-candidates">
            <CandidateList tile={tile} sessionId={sessionId} current={current} />
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

function CandidateList({ tile, sessionId, current }: { tile: VersionTile; sessionId: string; current: boolean }) {
  const { schema } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const { openOnStage, viewOnStage } = useObjectActions();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick, busy, choose } = useSessionPick(chat, sessionId);
  const shownAssetId = useWorkspaceStore((state) => state.currentObject?.assetId);
  const { slotOf } = useStageViewer();
  const candidates = candidatesOf(tile, (index) => formatSchemaText(schemaText(schema, 'ui.studio.strip.candidate_n'), { index }), imageResultSlots(schema, tile.capability));
  const groupDeclaration = schema.capabilities.find((entry) => entry.id === tile.capability)?.image_result_group;
  const groupSource = groupDeclaration?.source_selectable && tile.slots.length === 0 && groupDeclaration.slots.length === candidates.length &&
    groupDeclaration.slots.every((slot) => candidates.some((candidate) => candidate.variant === slot.variant))
    ? candidates.find((candidate) => candidate.variant === groupDeclaration.slots[0].variant) : undefined;
  if (candidates.length === 0) return null;
  const candidateSlots = candidates.flatMap((candidate) => {
    const slot = slotOf(tile, candidate);
    return slot ? [slot] : [];
  });
  const enlarge = (index: number) => {
    useStudioStore.getState().openLightbox(
      candidateSlots.map((slot) => ({ key: slot.key, url: slot.url, label: slot.label, downloadUrl: slot.url, ...(slot.assetId ? { pick: { taskId: slot.taskId, assetId: slot.assetId } } : {}) })),
      index,
    );
  };
  return (
    <>
      <div className="flex items-center gap-2">
        <Popover.Title className="min-w-0 flex-1 truncate text-xs font-semibold">{tile.groupLabel || schemaText(schema, 'ui.studio.strip.candidates')}</Popover.Title>
        <HintTooltip name={schemaText(schema, 'ui.studio.strip.compare_group')}
          reason={candidateSlots.length < 2 ? schemaText(schema, 'ui.unavailable.reason.stage.compare_needs_two') : undefined}>
          <Action
            type="button"
            size="xs"
            variant="outline"
            data-testid="version-compare-group"
            disabled={candidateSlots.length < 2}
            onClick={() => useStudioStore.getState().startCompare(compareFromGroup(candidateSlots))}
          >
            <Columns2 />{schemaText(schema, 'ui.studio.strip.compare_group')}
          </Action>
        </HintTooltip>
        {groupSource ? <Action type="button" size="xs" variant="outline" disabled={busy}
          data-testid="version-pick-image-group" onClick={() => {
            void choose({ kind: 'image', taskId: groupSource.taskId, assetId: groupSource.assetId }).then((ok) => {
              const object = ok ? objectOfTile(tile, sessionId, { taskId: groupSource.taskId, assetId: groupSource.assetId }) : null;
              if (object) openOnStage(object);
            });
          }}>{schemaText(schema, 'ui.studio.strip.pick_image_group')}</Action> : null}
      </div>
      <ul className="flex gap-2">
        {candidates.map((candidate) => {
          const picked = pick?.kind === 'image' && pick.taskId === candidate.taskId && pick.assetId === candidate.assetId;
          const shown = current && shownAssetId === candidate.assetId;
          return (
            <li key={candidate.key} className="flex w-20 flex-col gap-1">
              <button
                type="button"
                onClick={() => {
                  // Looking at a candidate only shows it (#559); 选用 below does not put it on the board either (#681).
                  const object = objectOfTile(tile, sessionId, { taskId: candidate.taskId, assetId: candidate.assetId });
                  if (object) viewOnStage(object);
                }}
                aria-label={[schemaText(schema, 'ui.studio.strip.view'), candidate.label].join(' ')}
                className={cn('relative h-20 w-20 overflow-hidden rounded-md border bg-muted focus-visible:outline-2 focus-visible:outline-ring', shown ? 'border-primary' : 'border-border')}
              >
                <img src={previewAssetUrl(candidate.url)} alt={''} decoding="async" className="size-full object-cover" />
              </button>
              <span className="truncate text-center text-[11px] text-muted-foreground">{candidate.label}</span>
              <HintTooltip name={schemaText(schema, 'ui.studio.strip.enlarge')}
                reason={candidateSlots.some((slot) => slot.key === slotKey(candidate.taskId, candidate.assetId)) ? undefined : schemaText(schema, 'ui.unavailable.reason.stage.no_output')}>
                <Action
                  type="button"
                  size="xs"
                  variant="ghost"
                  data-testid="version-enlarge"
                  disabled={!candidateSlots.some((slot) => slot.key === slotKey(candidate.taskId, candidate.assetId))}
                  onClick={() => enlarge(Math.max(0, candidateSlots.findIndex((slot) => slot.key === slotKey(candidate.taskId, candidate.assetId))))}
                >
                  <Maximize2 />{schemaText(schema, 'ui.studio.strip.enlarge')}
                </Action>
              </HintTooltip>
              <Action
                type="button"
                size="xs"
                variant={picked ? 'secondary' : 'outline'}
                disabled={busy || picked}
                aria-pressed={picked}
                data-testid="version-repick"
                onClick={() => {
                  // Choosing a candidate tells the Agent which one (the session's pick) and puts it on the stage, so the
                  // next request starts from it; it never puts it on the board (#681: openOnStage only views).
                  void choose({ kind: 'image', taskId: candidate.taskId, assetId: candidate.assetId }).then((ok) => {
                    const object = ok ? objectOfTile(tile, sessionId, { taskId: candidate.taskId, assetId: candidate.assetId }) : null;
                    if (object) openOnStage(object);
                  });
                }}
              >
                {picked ? <Check /> : null}{picked ? schemaText(schema, 'ui.chat.image.picked') : schemaText(schema, 'ui.chat.image.pick')}
              </Action>
            </li>
          );
        })}
      </ul>
    </>
  );
}

/** A detail without a Task needs a file to show. */
function canDisplayTarget(target: { url: string; stageObjectId?: string }): boolean {
  return Boolean(target.url && target.stageObjectId);
}

/** A locally viewed candidate takes precedence over the persisted pick only within its exact tile. */
function selectedTilePick(tile: VersionTile, object: CurrentObject | null, pick: TilePick = {}): TilePick {
  const belongs = (taskId: string | undefined, assetId: string | undefined) => Boolean(
    taskId && assetId && tile.taskIds.includes(taskId)
    && tile.assets.some((asset) => asset.id === assetId)
    && (tile.slots.length === 0 || tile.slots.some((slot) => slot.taskId === taskId && slot.assetId === assetId)),
  );
  if (object && belongs(object.taskId, object.assetId)) return { taskId: object.taskId, assetId: object.assetId };
  return belongs(pick.taskId, pick.assetId) ? pick : {};
}

interface TileButtonProps {
  tile: VersionTile;
  size: 'regular' | 'compact';
  current: boolean;
  inBranch: boolean;
  /** Part of the running comparison. */
  inCompare: boolean;
  sessionId: string;
  /** What the version is beside its number ("三视图", "手绘", "3D v1 截图"). */
  caption: string;
  /** A click views the version; a double click (`insert`) puts a 2D picture on the board (#593). */
  onOpen: (tile: VersionTile, mode?: OpenMode) => void;
}

/** #559/#593: how a strip tile is opened. `view` only looks at it; `insert` is 「插入到画布」. */
type OpenMode = 'view' | 'insert';

// Memoized (#291): the strip redraws on every task update; a tile whose own props did not change is skipped.
const TileButton = memo(function TileButton({ tile, size, current, inBranch, inCompare, sessionId, caption, onOpen }: TileButtonProps) {
  const { schema } = useProductSchema();
  const label = caption ? [versionLabel(schema, tile, tile.domain), caption].join(' · ') : versionLabel(schema, tile, tile.domain);
  const status = tile.status === 'ready' ? '' : versionStatusLabel(schema, tile);
  const ref = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (current) ref.current?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, [current]);
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick } = useSessionPick(chat, sessionId);
  const displayed = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  const selected = selectedTilePick(tile, displayed, pick?.kind === 'image' ? pick : {});
  const rawTarget = targetOfTile(tile, sessionId, label, selected, '', schema);
  // Uploads, canvas composites and captures have object IDs or source-model IDs, not their own public request.
  const taskBacked = tile.origin === 'task' && Boolean(rawTarget.taskId);
  const target = taskBacked ? rawTarget : { ...rawTarget, taskId: undefined };
  // An upload or an Agent crop (#587) has no Task, but its detail still shows it and offers 「插入到画布」.
  const detailed = taskBacked || (tile.origin === 'upload' && tile.status === 'ready' && canDisplayTarget(target));
  const canDisplay = objectOfTile(tile, sessionId, selected) !== null;
  const dragged = useRef(false);
  const dragSource = useCanvasDragSource(target);
  const handle = useTileHandle(tile);
  return (
    <ResultContextMenu
      target={target}
      render={<li ref={ref} className={cn('group/card relative', size === 'regular' ? 'w-24' : 'w-36')} data-testid="version-tile" data-key={tile.key} data-status={tile.status} data-current={current} />}
    >
      {/* The card shows a short caption; the hover hint carries the whole label and, for a failed one, why it failed.
          The wrapper is block-level and full width, so every tile of a column stays as wide as the column (its
          ring and hover background included), as the bare button was. */}
      <HintTooltip name={label} hint={tile.status === 'failed' && tile.failure ? tile.failure : undefined}
        reason={tile.status === 'ready' && !canDisplay ? schemaText(schema, 'ui.unavailable.reason.stage.no_output') : undefined} className="flex w-full">
        <button
          type="button"
          disabled={!canDisplay}
          onClick={(event) => {
            if (dragged.current && event.detail !== 0) { dragged.current = false; return; }
            // #593: the second click of a double click repeats nothing; the double click itself inserts.
            if (event.detail > 1) return;
            onOpen(tile);
          }}
          onDoubleClick={() => {
            if (dragged.current) return;
            onOpen(tile, 'insert');
          }}
          {...dragSource}
          onPointerDown={() => { dragged.current = false; }}
          onDragStart={(event) => { dragged.current = true; dragSource.onDragStart?.(event); }}
          aria-current={current ? 'true' : undefined}
          aria-pressed={inCompare || undefined}
          data-shortcut-arrows
          aria-label={[schemaText(schema, 'ui.studio.strip.view'), label, status || (!canDisplay ? schemaText(schema, 'ui.studio.strip.unavailable') : ''), inCompare ? schemaText(schema, 'ui.studio.strip.in_compare') : ''].filter(Boolean).join(' · ')}
          className={cn(
            'flex w-full items-center gap-2 rounded-lg p-1 text-left transition-colors enabled:hover:bg-panel-raised disabled:cursor-default focus-visible:outline-2 focus-visible:outline-ring',
            size === 'regular' ? 'flex-col gap-0.5 p-0.5' : 'h-[34px] flex-row gap-1.5 p-0.5 pr-2',
            current && 'bg-primary/10 ring-2 ring-primary',
            !inBranch && !current && 'opacity-60',
          )}
        >
          <Thumb tile={tile} size={size} handle={handle} />
          {inCompare ? (
            <span className="absolute left-1 top-1 rounded-sm bg-primary px-1 text-[9px] font-semibold leading-3 text-primary-foreground" data-testid="version-in-compare">{schemaText(schema, 'ui.studio.strip.in_compare')}</span>
          ) : null}
          <span className={cn('flex min-w-0 flex-col overflow-hidden', size === 'regular' ? 'items-center' : 'items-start')}>
            <span className="flex h-[18px] min-w-0 max-w-full items-center gap-1">
              <KindBadge kind={tile.domain} />
              <span className={cn('truncate font-medium leading-4', size === 'regular' ? 'text-xs' : 'text-[11px]')}>{versionLabel(schema, tile, tile.domain)}</span>
              {/* #617: a 28px compact thumbnail has no room for the badge; it sits after the version label. */}
              {handle && size === 'compact' ? <HandleBadge handle={handle} className="shrink-0" /> : null}
            </span>
            <span className={cn('h-3 max-w-full truncate text-[10px] leading-3', tile.status === 'failed' ? 'text-destructive' : 'text-muted-foreground')} data-testid="version-caption">
              {status || caption || ' '}
            </span>
          </span>
        </button>
      </HintTooltip>
      {/* #593: 「详情」 is a button as wide as the tile, under it; a tile without details keeps the row empty. */}
      <div className="flex h-5 items-stretch pt-0.5" data-testid="version-detail-row">
        {detailed ? <Action
          type="button"
          variant="outline"
          size="xs"
          data-testid="version-detail"
          aria-label={[schemaText(schema, 'ui.studio.strip.detail'), label].join(' · ')}
          onClick={(event) => {
            event.stopPropagation();
            // Safari does not focus buttons on pointer activation; the shared dialog restores its opening focus.
            event.currentTarget.focus();
            openResultDetail(target);
          }}
          className="h-full w-full justify-center px-1 text-[11px] leading-none text-muted-foreground"
        >{schemaText(schema, 'ui.studio.strip.detail')}</Action> : null}
      </div>
      {/* A compact row always shows its download: revealing it on hover left one row with the icon (the one
          focused) and the next without, which read as a version that cannot be downloaded. */}
      <CardDownload target={target} className="absolute right-1 top-1 z-10" buttonClassName={cn('size-5', size === 'compact' && 'opacity-100')} />
      {tile.status === 'ready' ? <CandidatePopover tile={tile} sessionId={sessionId} current={current} /> : null}
    </ResultContextMenu>
  );
});

/** At most this many source names show under a multi-source link; the rest read "+N" (the hover hint names them all). */
const SOURCE_NAMES_SHOWN = 3;

/** How the strip names a source version: the chat handle of a single picture (@img2), else its version label (2D v2). */
function useSourceName() {
  const { schema } = useProductSchema();
  const messages = useSessionMessages(useChatStore((state) => state.activeSessionId));
  return useCallback((tile: VersionTile) => {
    const single = tile.domain === 'image' && tile.slots.length === 0 && tile.views.length === 0 && tile.assets.length === 1;
    const handle = single ? handleOfPicture(messages, { url: tile.assets[0].url }) : '';
    return handle || versionLabel(schema, tile, tile.domain);
  }, [messages, schema]);
}

/**
 * A result made from several versions (a multi-view 3D from four pictures): one link that names every source, in
 * the order the task used them. None of them is "the" source, so none is singled out (#558).
 */
const MultiSourceConnector = memo(function MultiSourceConnector({ column, sourceTiles }: { column: VersionColumn; sourceTiles: readonly VersionTile[] }) {
  const { schema } = useProductSchema();
  const nameOf = useSourceName();
  const names = column.sources.map((source) => {
    const tile = sourceTiles.find((entry) => entry.key === source.key);
    return { link: source.link, name: tile ? nameOf(tile) : '' };
  }).filter((entry) => entry.name);
  const classes = [...new Set(names.map((entry) => entry.link))];
  // One sentence per kind of link ("由 @img2 @img3 转成 3D"), so a mixed set (an edit with a reference) says both.
  const title = classes.map((link) => formatSchemaText(schemaText(schema, `ui.studio.strip.link.${link}`), {
    parent: names.filter((entry) => entry.link === link).map((entry) => entry.name).join(' '),
  })).join(' · ');
  const hidden = names.length - SOURCE_NAMES_SHOWN;
  return (
    <li className="flex w-12 shrink-0 self-stretch flex-col items-center justify-center gap-0.5" data-testid="version-sources" data-count={names.length}>
      <HintTooltip name={title} className="w-full">
        <div className="flex w-full flex-col items-center gap-0.5" data-testid="version-link" data-link={classes.join(' ')} data-known="true" data-full-label={title}>
          <span className="flex items-center gap-0.5">
            {classes.map((link) => {
              const Icon = LINK_ICONS[link];
              return <Icon key={link} aria-hidden className="size-3 text-muted-foreground" />;
            })}
          </span>
          <span className="h-0 w-full border-t-2 border-muted-foreground/60" />
          <span className="sr-only">{title}</span>
        </div>
      </HintTooltip>
      {names.slice(0, hidden > 0 ? SOURCE_NAMES_SHOWN - 1 : SOURCE_NAMES_SHOWN).map((entry, index) => (
        <span key={index} className="max-w-12 truncate text-[9px] leading-3 text-muted-foreground" data-testid="version-source-name">{entry.name}</span>
      ))}
      {hidden > 0 ? <span className="text-[9px] leading-3 text-muted-foreground">+{hidden + 1}</span> : null}
    </li>
  );
});

const Connector = memo(function Connector({ column, previous, parentTile }: { column: VersionColumn; previous: VersionColumn | null; parentTile: VersionTile | undefined }) {
  const { schema } = useProductSchema();
  const parent = column.parent;
  if (!parent) return <li aria-hidden className="w-3 shrink-0" />;
  const adjacent = Boolean(parentTile && previous?.tiles.some((tile) => tile.key === parentTile.key));
  const parentLabel = parentTile ? versionLabel(schema, parentTile, parentTile.domain) : '';
  // Results of one source share this connector, with one mark for each of them: a plain edit and a
  // masked repaint are different, and a new branch is a new mark. A processing link names its step.
  const sentence = (link: VersionParent) => {
    // #670: a parent outside the loaded versions has no name to put in the sentence, so it reads as unknown.
    if (!link.known || !parentLabel) return schemaText(schema, 'ui.studio.strip.link.unknown');
    const stepKey = link.link === 'process' && link.step ? `ui.studio.strip.link.step.${link.step}` : '';
    const linkKey = stepKey && schemaText(schema, stepKey) ? stepKey : `ui.studio.strip.link.${link.link}`;
    return formatSchemaText(schemaText(schema, linkKey), { parent: parentLabel });
  };
  const marks = column.links.length > 0 ? column.links : [parent];
  return (
    <li className="flex w-9 shrink-0 self-stretch flex-col items-center justify-center gap-1">
      {marks.map((link, index) => {
        const Icon = LINK_ICONS[link.link];
        const title = sentence(link);
        return (
          <HintTooltip key={index} name={title} className="w-full">
            {/* data-full-label carries the same sentence as the hover hint, for tests and scripts that read it. */}
            <div className="flex w-full flex-col items-center gap-0.5" data-testid="version-link" data-link={link.link} data-known={link.known} data-full-label={title}>
              <Icon aria-hidden className={cn('size-3', link.known ? 'text-muted-foreground' : 'text-muted-foreground/70')} />
              <span className={cn('h-0 w-full border-t-2', link.known ? 'border-muted-foreground/60' : 'border-dashed border-muted-foreground/50')} />
              <span className="sr-only">{title}</span>
            </div>
          </HintTooltip>
        );
      })}
      {parent.known && !adjacent && parentLabel ? <span className="max-w-9 truncate text-[9px] leading-3 text-muted-foreground" data-testid="version-link-source">{parentLabel}</span> : null}
    </li>
  );
});

/**
 * 底部的版本条：本会话每个 2D / 3D 版本一格，严格按创建时间排开（最新的在最右，#558）；格与格之间
 * 用连接线说明"由谁而来"（转 3D、截图、处理、编辑），来源不明的用虚线。来源不是紧挨着的前一格时，
 * 连接下写出来源名；用多张图做成的结果（多视图 3D）列出全部来源，不挑其中一张。点一格就是把它放上
 * 舞台、成为当前对象，输入框上方的引用随之换成它（#559：不放上画布；放上画布在详情里点「插入到画布」，
 * 或双击 2D 那一格，#593）；时间上相邻的同源结果在一列里叠起来，多了就留第一个、其余收进「更多」，
 * 点哪一个都不改变各格的位置和这一组留在外面的那一个（#596）。
 */
/** 切换会话、版本链还没到时的占位：几块与缩略图同尺寸的骨架。 */
function VersionStripSkeleton({ label }: { label: string }) {
  return (
    <div role="status" aria-busy="true" aria-label={label} data-testid="version-strip-skeleton" className="flex h-full items-center gap-2 px-3">
      {[0, 1, 2, 3].map((index) => <Skeleton key={index} className="h-20 w-20 shrink-0" />)}
    </div>
  );
}

export function VersionStrip() {
  const { schema } = useProductSchema();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const object = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  const versions = useSessionVersions();
  const scrollRef = useRef<HTMLDivElement>(null);
  const loadEarlier = async () => {
    const scroll = scrollRef.current;
    const width = scroll?.scrollWidth ?? 0;
    const left = scroll?.scrollLeft ?? 0;
    try {
      await versions.loadEarlier();
      requestAnimationFrame(() => { if (scroll) scroll.scrollLeft = left + scroll.scrollWidth - width; });
    } catch { toast.error(schemaText(schema, 'ui.studio.strip.earlier_failed')); }
  };
  const setAllOutputsOpen = useStudioStore((state) => state.setAllOutputsOpen);
  const currentTile = tileOfObject(versions, object);
  const { toggleTileInCompare, slotOf } = useStageViewer();
  const compare = useStudioStore((state) => state.compare);
  const compareKeys = useMemo(() => new Set((compare?.slots ?? []).map((slot: CompareSlot) => slot.key)), [compare]);
  const inCompareTile = (tile: VersionTile) => compareKeys.size > 0 && (
    candidatesOf(tile, String, imageResultSlots(schema, tile.capability)).some((candidate) => compareKeys.has(slotKey(candidate.taskId, candidate.assetId)))
    || compareKeys.has(slotOf(tile)?.key ?? '')
  );
  const tiles = useMemo(() => shownVersionTiles(versions.tiles), [versions.tiles]);
  const branch = useMemo(() => branchOf(tiles, currentTile?.key ?? null), [tiles, currentTile?.key]);
  // Columns that did not change keep their objects, so their memoized connectors are not redrawn (#291).
  const previousColumns = useRef<VersionColumn[] | undefined>(undefined);
  const columns = useMemo(() => {
    const next = keepUnchanged(previousColumns.current, layoutColumns(tiles), (column) => column.tiles[0].key);
    previousColumns.current = next;
    return next;
  }, [tiles]);
  const captionOf = useCallback((tile: VersionTile) => versionCaption(schema, tile, tiles), [schema, tiles]);
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick } = useSessionPick(chat, sessionId);
  const { viewOnStage, insertOnCanvas } = useObjectActions();

  // The same callback for every tile and render (it reads the latest state through a ref), so memoized tiles stay put.
  const openLatest = useRef<(tile: VersionTile, mode?: OpenMode) => void>(() => undefined);
  openLatest.current = (tile: VersionTile, mode: OpenMode = 'view') => {
    if (!sessionId) return;
    // #593: a double click inserts a 2D picture (an upload or Agent crop too); on a model or while comparing,
    // its clicks already did what a click does.
    if (mode === 'insert' && (compare || tile.domain !== 'image')) return;
    if (compare) {
      // While comparing, a click puts the version into or out of a pane instead of changing the stage.
      if (tile.status === 'ready' && !toggleTileInCompare(tile)) toast.error(schemaText(schema, 'ui.studio.compare.unavailable'));
      return;
    }
    // Keep the currently viewed candidate when revisiting its tile, then fall back to the persisted pick.
    const picked = selectedTilePick(tile, object, pick?.kind === 'image' ? pick : {});
    const selected = objectOfTile(tile, sessionId, picked);
    // #559: a click looks at the version; it does not put it on the board again. 「插入到画布」 in its detail, or a
    // double click on the tile (#593), does: the same insertOnCanvas, receipt and all.
    if (!selected) return;
    if (mode === 'insert') insertOnCanvas(selected);
    else viewOnStage(selected);
  };
  const open = useCallback((tile: VersionTile, mode?: OpenMode) => openLatest.current(tile, mode), []);

  return (
    <section aria-label={schemaText(schema, 'ui.studio.strip.title')} data-testid="version-strip" className="flex h-36 shrink-0 items-stretch border-t border-border bg-panel">
      <div ref={scrollRef} className="min-w-0 flex-1 overflow-x-auto overflow-y-hidden">
        {columns.length === 0 && versions.isLoading ? (
          <VersionStripSkeleton label={schemaText(schema, 'ui.studio.strip.loading')} />
        ) : columns.length === 0 ? (
          <p className="flex h-full items-center px-4 text-sm text-muted-foreground" data-testid="version-strip-empty">{schemaText(schema, 'ui.studio.strip.empty')}</p>
        ) : (
          // #546: 12px above and below the cards, so the 2px ring of the selected card clears the strip's top edge.
          <ol className="flex h-full w-max items-start px-3 py-3">
            {columns.map((column, index) => (
              <ColumnView
                key={column.tiles[0].key}
                column={column}
                previous={index > 0 ? columns[index - 1] : null}
                tiles={tiles}
                currentKey={currentTile?.key ?? null}
                branch={branch}
                inCompareTile={inCompareTile}
                captionOf={captionOf}
                sessionId={sessionId ?? ''}
                onOpen={open}
              />
            ))}
            {versions.truncated ? <li className="px-3 text-xs text-muted-foreground">{schemaText(schema, 'ui.studio.strip.truncated')}</li> : null}
          </ol>
        )}
      </div>
      <div className="flex shrink-0 items-center border-l border-border px-3">
        {versions.hasEarlier ? <Action type="button" variant="ghost" size="sm" loading={versions.loadingEarlier} onClick={() => void loadEarlier()} data-testid="versions-load-earlier">
          {schemaText(schema, versions.loadingEarlier ? 'ui.studio.strip.loading_earlier' : 'ui.studio.strip.load_earlier')}
        </Action> : null}
        <HintTooltip name={schemaText(schema, 'ui.studio.strip.all_outputs')} reason={tiles.length === 0 ? schemaText(schema, 'ui.unavailable.reason.stage.no_output') : undefined}>
          <Action type="button" variant="outline" size="sm" onClick={() => setAllOutputsOpen(true)} disabled={tiles.length === 0} data-testid="all-outputs-button">
            <Grid2x2 />{schemaText(schema, 'ui.studio.strip.all_outputs')}
          </Action>
        </HintTooltip>
      </div>
    </section>
  );
}

function ColumnView({ column, previous, tiles, currentKey, branch, inCompareTile, captionOf, sessionId, onOpen }: {
  column: VersionColumn;
  previous: VersionColumn | null;
  tiles: readonly VersionTile[];
  currentKey: string | null;
  branch: Set<string>;
  inCompareTile: (tile: VersionTile) => boolean;
  captionOf: (tile: VersionTile) => string;
  sessionId: string;
  onOpen: (tile: VersionTile, mode?: OpenMode) => void;
}) {
  const stacked = column.tiles.length > 1;
  // #596: what stays out is fixed by the order the results were made; the selection never changes it.
  const folded = useMemo(() => foldColumn(column.tiles, 2), [column.tiles]);
  const sourceTiles = useMemo(
    () => (column.sources.length > 1 ? tiles.filter((tile) => column.sources.some((source) => source.key === tile.key)) : []),
    [column.sources, tiles],
  );
  return (
    <>
      {previous && column.sources.length > 1 ? <MultiSourceConnector column={column} sourceTiles={sourceTiles} />
        : previous ? <Connector column={column} previous={previous} parentTile={column.parent?.known ? tiles.find((tile) => tile.key === column.parent?.key) : undefined} /> : null}
      <li className="shrink-0" data-testid="version-column">
        <ol className={cn('flex flex-col', stacked ? 'gap-0.5 border-l-2 border-border pl-1' : 'gap-1')} data-testid={stacked ? 'version-stack' : undefined} data-count={stacked ? column.tiles.length : undefined}>
          {folded.visible.map((tile) => (
            <TileButton
              key={tile.key}
              tile={tile}
              size={stacked ? 'compact' : 'regular'}
              current={tile.key === currentKey}
              inBranch={currentKey === null || branch.has(tile.key)}
              inCompare={inCompareTile(tile)}
              caption={captionOf(tile)}
              sessionId={sessionId}
              onOpen={onOpen}
            />
          ))}
          {folded.hidden.length > 0 ? (
            <FoldedTiles
              tiles={column.tiles}
              currentHidden={folded.hidden.some((tile) => tile.key === currentKey)}
              currentKey={currentKey}
              branch={branch}
              inCompareTile={inCompareTile}
              captionOf={captionOf}
              sessionId={sessionId}
              onOpen={onOpen}
            />
          ) : null}
        </ol>
      </li>
    </>
  );
}

/**
 * 一组里放不下的结果收在「更多」后面（#596）：弹层列出这一步生成的全部内容（外面那一格也在内），按生成先后排好。
 * 每一项和版本条上的一格一样：单击引用、双击插入画布、「详情」看详情；点哪一项都不改变顺序，也不换外面那一格。
 * 当前对象在收起的部分时，「更多」按钮高亮。
 */
function FoldedTiles({ tiles, currentHidden, currentKey, branch, inCompareTile, captionOf, sessionId, onOpen }: {
  /** Every result of the column, in the order they were made. */
  tiles: VersionTile[];
  /** The current object is one of the results only listed here. */
  currentHidden: boolean;
  currentKey: string | null;
  branch: Set<string>;
  inCompareTile: (tile: VersionTile) => boolean;
  captionOf: (tile: VersionTile) => string;
  sessionId: string;
  onOpen: (tile: VersionTile, mode?: OpenMode) => void;
}) {
  const { schema } = useProductSchema();
  const layer = useAnchoredPositioner();
  const title = schemaText(schema, 'ui.studio.strip.group_all_title');
  return (
    <li data-testid="version-fold">
      <Popover.Root>
        <HintTooltip name={title} className="flex w-full">
          <Popover.Trigger
            aria-label={title}
            aria-current={currentHidden ? 'true' : undefined}
            data-testid="version-fold-trigger"
            className={cn(
              'flex h-8 w-full items-center justify-center rounded-md border border-dashed border-border px-2 text-[11px] font-medium text-muted-foreground hover:bg-panel-raised hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:border-primary data-popup-open:text-primary',
              currentHidden && 'border-solid border-primary bg-primary/10 text-primary',
            )}
          >
            {schemaText(schema, 'ui.studio.strip.group_all')}
          </Popover.Trigger>
        </HintTooltip>
        <Popover.Portal>
          <Popover.Positioner positionMethod={anchoredPositionMethod} side="top" align="start" sideOffset={8} collisionPadding={12} className={layer}>
            <Popover.Popup className="flex max-h-96 max-w-(--available-width) flex-col gap-1 overflow-y-auto rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10" data-testid="version-fold-list">
              <Popover.Title className="text-xs font-semibold">{title}</Popover.Title>
              <ol className="grid grid-cols-4 gap-1 p-0.5">
                {tiles.map((tile) => (
                  <TileButton
                    key={tile.key}
                    tile={tile}
                    size="regular"
                    current={tile.key === currentKey}
                    inBranch={currentKey === null || branch.has(tile.key)}
                    inCompare={inCompareTile(tile)}
                    caption={captionOf(tile)}
                    sessionId={sessionId}
                    onOpen={onOpen}
                  />
                ))}
              </ol>
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
    </li>
  );
}
