'use client';

import { useCanvasStore } from '@/store/canvas-store';
import { memo, useCallback, useEffect, useMemo, useRef } from 'react';
import { Popover } from '@base-ui/react/popover';
import { AlertTriangle, Box, Brush, Camera, Check, ChevronUp, Columns2, Eraser, Grid2x2, Link2Off, Maximize2, Merge, Paintbrush, Paperclip, Wand2, type LucideIcon } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { KindBadge } from '@/components/studio/kind-badge';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAnchoredPositioner } from '@/components/ui/overlay';
import { CardDownload, ResultContextMenu, openResultDetail, useCanvasDragSource } from '@/components/result/result-card-parts';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useSessionPick } from '@/hooks/use-session-pick';
import { useStageViewer } from '@/hooks/use-stage-viewer';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { compareFromGroup, slotKey, type CompareSlot } from '@/lib/workspace/compare';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { imageResultSlots, orderedImageResults, type ImageResultSlot } from '@/lib/workspace/image-result-group';
import { targetOfTile } from '@/lib/workspace/result-target';
import { objectOfTile, type TilePick } from '@/lib/workspace/version-object';
import { versionCaption, versionLabel, versionTitle } from '@/lib/workspace/version-label';
import { branchOf, foldColumn, layoutColumns, versionStatusLabel, type LinkClass, type VersionColumn, type VersionParent, type VersionTile } from '@/lib/workspace/versions';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { keepUnchanged } from '@/lib/core/keep-unchanged';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

/** The strip is exactly this tall (h-28 = 7rem = 112px); one column may hold two compact tiles. */
const LINK_ICONS: Record<LinkClass, LucideIcon> = {
  to_model: Box,
  capture: Camera,
  process: Wand2,
  edit: Paintbrush,
  inpaint: Brush,
  remove_background: Eraser,
  reference: Paperclip,
  merge: Merge,
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

function Thumb({ tile, size }: { tile: VersionTile; size: 'regular' | 'compact' }) {
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
  const finished = tile.views.filter((view) => view.status === 'succeeded').length;
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
      ) : src
        ? <img src={src} alt={''} loading="lazy" decoding="async" className="size-full object-cover" />
        : tile.domain === 'model' ? <Box aria-hidden className="size-5 text-muted-foreground" /> : null}
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
            {formatSchemaText(text('group_progress'), { done: finished, total: tile.views.length })}
          </span>
        </span>
      ) : size === 'regular' && tile.slots.length > 1 ? (
        <span className="absolute bottom-0.5 right-0.5 rounded-sm bg-panel/90 px-1 text-[9px] font-semibold leading-3">{tile.slots.length}</span>
      ) : size === 'regular' && tile.domain === 'image' && tile.assets.length > 1 ? (
        <span className="absolute bottom-0.5 right-0.5 rounded-sm bg-panel/90 px-1 text-[9px] font-semibold leading-3" data-testid="version-candidate-count">{tile.assets.length}</span>
      ) : null}
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
        <Popover.Positioner side="top" align="center" sideOffset={8} collisionPadding={12} className={layer}>
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
  const { openOnStage } = useObjectActions();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick, busy, choose } = useSessionPick(chat, sessionId);
  const boardNodes = useCanvasStore((state) => state.nodesBySession[sessionId]);
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
          // Picked on the server but its picture was removed from the board: say so, the pick itself is untouched.
          const offCanvas = picked && boardNodes !== undefined && !boardNodes.some((node) => node.type === 'image' && node.taskId === candidate.taskId);
          const shown = current && shownAssetId === candidate.assetId;
          return (
            <li key={candidate.key} className="flex w-20 flex-col gap-1">
              <button
                type="button"
                onClick={() => {
                  const object = objectOfTile(tile, sessionId, { taskId: candidate.taskId, assetId: candidate.assetId });
                  if (object) openOnStage(object);
                }}
                aria-label={[schemaText(schema, 'ui.studio.strip.view'), candidate.label].join(' ')}
                className={cn('relative h-20 w-20 overflow-hidden rounded-md border bg-muted focus-visible:outline-2 focus-visible:outline-ring', shown ? 'border-primary' : 'border-border')}
              >
                <img src={previewAssetUrl(candidate.url)} alt={''} decoding="async" className="size-full object-cover" />
              </button>
              <span className="truncate text-center text-[11px] text-muted-foreground">{candidate.label}</span>
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
              <Action
                type="button"
                size="xs"
                variant={picked ? 'secondary' : 'outline'}
                disabled={busy || picked}
                aria-pressed={picked}
                data-testid="version-repick"
                onClick={() => {
                  // Choosing a candidate also puts it on the stage, so the next request starts from the one picked.
                  void choose({ kind: 'image', taskId: candidate.taskId, assetId: candidate.assetId }).then((ok) => {
                    const object = ok ? objectOfTile(tile, sessionId, { taskId: candidate.taskId, assetId: candidate.assetId }) : null;
                    if (object) openOnStage(object);
                  });
                }}
              >
                {picked ? <Check /> : null}{picked ? schemaText(schema, offCanvas ? 'ui.chat.image.picked_off_canvas' : 'ui.chat.image.picked') : schemaText(schema, 'ui.chat.image.pick')}
              </Action>
            </li>
          );
        })}
      </ul>
    </>
  );
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
  onOpen: (tile: VersionTile) => void;
}

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
  const canDisplay = objectOfTile(tile, sessionId, selected) !== null;
  const dragged = useRef(false);
  const dragSource = useCanvasDragSource(target);
  return (
    <ResultContextMenu
      target={target}
      render={<li ref={ref} className={cn('group/card relative', size === 'regular' ? 'w-24' : 'w-36')} data-testid="version-tile" data-key={tile.key} data-status={tile.status} data-current={current} />}
    >
      {/* The card shows a short caption; the hover hint carries the whole label and, for a failed one, why it failed.
          The wrapper is block-level and full width, so every tile of a column stays as wide as the column (its
          ring and hover background included), as the bare button was. */}
      <HintTooltip name={label} hint={tile.status === 'failed' && tile.failure ? tile.failure : undefined} className="flex w-full">
        <button
          type="button"
          disabled={!canDisplay}
          onClick={(event) => {
            if (dragged.current && event.detail !== 0) { dragged.current = false; return; }
            onOpen(tile);
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
          <Thumb tile={tile} size={size} />
          {inCompare ? (
            <span className="absolute left-1 top-1 rounded-sm bg-primary px-1 text-[9px] font-semibold leading-3 text-primary-foreground" data-testid="version-in-compare">{schemaText(schema, 'ui.studio.strip.in_compare')}</span>
          ) : null}
          <span className={cn('flex min-w-0 flex-col overflow-hidden', size === 'regular' ? 'items-center' : 'items-start')}>
            <span className="flex h-[18px] min-w-0 max-w-full items-center gap-1">
              <KindBadge kind={tile.domain} />
              <span className={cn('truncate font-medium leading-4', size === 'regular' ? 'text-xs' : 'text-[11px]')}>{versionLabel(schema, tile, tile.domain)}</span>
            </span>
            <span className={cn('h-3 max-w-full truncate text-[10px] leading-3', tile.status === 'failed' ? 'text-destructive' : 'text-muted-foreground')} data-testid="version-caption">
              {status || caption || ' '}
            </span>
          </span>
        </button>
      </HintTooltip>
      <div className="flex h-4 items-center justify-center" data-testid="version-detail-row">
        {taskBacked ? <button
          type="button"
          data-testid="version-detail"
          aria-label={[schemaText(schema, 'ui.studio.strip.detail'), label].join(' · ')}
          onClick={(event) => {
            event.stopPropagation();
            // Safari does not focus buttons on pointer activation; the shared dialog restores its opening focus.
            event.currentTarget.focus();
            openResultDetail(target);
          }}
          className="rounded-sm px-1 text-[10px] leading-4 text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        >{schemaText(schema, 'ui.studio.strip.detail')}</button> : null}
      </div>
      <CardDownload target={target} className="absolute right-1 top-1 z-10" buttonClassName="size-5" />
      {tile.status === 'ready' ? <CandidatePopover tile={tile} sessionId={sessionId} current={current} /> : null}
    </ResultContextMenu>
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
    if (!link.known) return schemaText(schema, 'ui.studio.strip.link.unknown');
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
      {parent.known && !adjacent ? <span className="max-w-9 truncate text-[9px] leading-3 text-muted-foreground">{parentLabel}</span> : null}
    </li>
  );
});

/**
 * 底部 112px 的版本条：本会话每个 2D / 3D 版本一格，按先后排开；格与格之间用连接线
 * 说明"由谁而来"（转 3D、截图、处理、编辑），来源不明的用虚线。点一格就是把它放上舞台；
 * 同一来源的多个结果在一列里叠起来。
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
  const branch = useMemo(() => branchOf(versions.tiles, currentTile?.key ?? null), [versions.tiles, currentTile?.key]);
  // Columns that did not change keep their objects, so their memoized connectors are not redrawn (#291).
  const previousColumns = useRef<VersionColumn[] | undefined>(undefined);
  const columns = useMemo(() => {
    const next = keepUnchanged(previousColumns.current, layoutColumns(versions.tiles), (column) => column.tiles[0].key);
    previousColumns.current = next;
    return next;
  }, [versions.tiles]);
  const captionOf = useCallback((tile: VersionTile) => versionCaption(schema, tile, versions.tiles), [schema, versions.tiles]);
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick } = useSessionPick(chat, sessionId);
  const { openOnStage } = useObjectActions();

  // The same callback for every tile and render (it reads the latest state through a ref), so memoized tiles stay put.
  const openLatest = useRef<(tile: VersionTile) => void>(() => undefined);
  openLatest.current = (tile: VersionTile) => {
    if (!sessionId) return;
    if (compare) {
      // While comparing, a click puts the version into or out of a pane instead of changing the stage.
      if (tile.status === 'ready' && !toggleTileInCompare(tile)) toast.error(schemaText(schema, 'ui.studio.compare.unavailable'));
      return;
    }
    // Keep the currently viewed candidate when revisiting its tile, then fall back to the persisted pick.
    const picked = selectedTilePick(tile, object, pick?.kind === 'image' ? pick : {});
    const selected = objectOfTile(tile, sessionId, picked);
    if (selected) openOnStage(selected);
  };
  const open = useCallback((tile: VersionTile) => openLatest.current(tile), []);

  return (
    <section aria-label={schemaText(schema, 'ui.studio.strip.title')} data-testid="version-strip" className="flex h-28 shrink-0 items-stretch border-t border-border bg-panel">
      <div ref={scrollRef} className="min-w-0 flex-1 overflow-x-auto overflow-y-hidden">
        {columns.length === 0 && versions.isLoading ? (
          <VersionStripSkeleton label={schemaText(schema, 'ui.studio.strip.loading')} />
        ) : columns.length === 0 ? (
          <p className="flex h-full items-center px-4 text-sm text-muted-foreground" data-testid="version-strip-empty">{schemaText(schema, 'ui.studio.strip.empty')}</p>
        ) : (
          <ol className="flex h-full w-max items-start px-3 py-0.5">
            {columns.map((column, index) => (
              <ColumnView
                key={column.tiles[0].key}
                column={column}
                previous={index > 0 ? columns[index - 1] : null}
                tiles={versions.tiles}
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
        <Action type="button" variant="outline" size="sm" onClick={() => setAllOutputsOpen(true)} disabled={versions.tiles.length === 0} data-testid="all-outputs-button">
          <Grid2x2 />{schemaText(schema, 'ui.studio.strip.all_outputs')}
        </Action>
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
  onOpen: (tile: VersionTile) => void;
}) {
  const stacked = column.tiles.length > 1;
  const folded = useMemo(() => foldColumn(column.tiles, branch, currentKey, 2), [column.tiles, branch, currentKey]);
  return (
    <>
      {previous ? <Connector column={column} previous={previous} parentTile={column.parent?.known ? tiles.find((tile) => tile.key === column.parent?.key) : undefined} /> : null}
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
              tiles={folded.hidden}
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

/** 超过三个同源结果时折起来的那部分："+N" 打开一个列表，点哪个就把哪个放上舞台。 */
function FoldedTiles({ tiles, currentKey, branch, inCompareTile, captionOf, sessionId, onOpen }: {
  tiles: VersionTile[];
  currentKey: string | null;
  branch: Set<string>;
  inCompareTile: (tile: VersionTile) => boolean;
  captionOf: (tile: VersionTile) => string;
  sessionId: string;
  onOpen: (tile: VersionTile) => void;
}) {
  const { schema } = useProductSchema();
  const layer = useAnchoredPositioner();
  const label = schemaCountMessage(schema, 'ui.studio.strip.more', tiles.length, { count: tiles.length });
  return (
    <li data-testid="version-fold">
      <Popover.Root>
        <HintTooltip name={schemaText(schema, 'ui.studio.strip.more_title')} hint={label} className="flex w-full">
          <Popover.Trigger
            aria-label={label}
            data-testid="version-fold-trigger"
            className="flex h-8 w-full items-center justify-center rounded-md border border-dashed border-border px-2 text-[11px] font-medium text-muted-foreground hover:bg-panel-raised hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:border-primary data-popup-open:text-primary"
          >
            +{tiles.length}
          </Popover.Trigger>
        </HintTooltip>
        <Popover.Portal>
          <Popover.Positioner side="top" align="start" sideOffset={8} collisionPadding={12} className={layer}>
            <Popover.Popup className="flex max-h-72 max-w-(--available-width) flex-col gap-1 overflow-y-auto rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10" data-testid="version-fold-list">
              <Popover.Title className="text-xs font-semibold">{schemaText(schema, 'ui.studio.strip.more_title')}</Popover.Title>
              <ol className="flex flex-col gap-0.5">
                {tiles.map((tile) => (
                  <TileButton
                    key={tile.key}
                    tile={tile}
                    size="compact"
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
