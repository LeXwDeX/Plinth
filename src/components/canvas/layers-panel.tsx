'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import {
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  Folder,
  GripVertical,
  Image as ImageIcon,
  Lock,
  LockOpen,
  Pencil,
  Scan,
  Square,
  Trash2,
  Type,
  X,
} from 'lucide-react';

import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Surface } from '@/components/ui/surface';
import { useProductSchema } from '@/components/product-schema-provider';
import { artboardBackdrop, checkerTile } from '@/lib/canvas/artboard-backdrop';
import { canvasArtboard } from '@/lib/canvas/artboard';
import { arrangeLayers, layerRows, layerRowKey, type DropPosition, type DropTarget } from '@/lib/canvas/layer-tree';
import { inpaintMaskURL, isInpaintSource, type InpaintSource } from '@/lib/canvas/inpaint-source';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { renderSelectionPreview } from '@/lib/canvas/selection-preview';
import { layerName } from '@/lib/canvas/layer-name';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { useMaskStore, type MaskDraft } from '@/store/mask-store';

const EMPTY_NODES: CanvasNode[] = [];

function typeIcon(node: CanvasNode) {
  switch (node.type) {
    case 'image':
      return <ImageIcon className="size-3.5" />;
    case 'text':
      return <Type className="size-3.5" />;
    case 'pencil':
      return <Pencil className="size-3.5" />;
    default:
      return <Square className="size-3.5" />;
  }
}

interface DragState {
  /** Layer ids being dragged: one layer, or every member of a group row. */
  ids: string[];
}

interface DropHint {
  key: string;
  position: DropPosition;
}

export interface InpaintMaskLabels {
  title: string;
  note: string;
  loading: string;
  unavailable: string;
  size: string;
  close: string;
}

/** Owner-scoped view of the exact mask an inpaint patch used; an unavailable mask never breaks the list. */
export function InpaintMaskDialog({
  assetUrl,
  width,
  height,
  labels,
  onClose,
}: {
  assetUrl: string;
  width: number;
  height: number;
  labels: InpaintMaskLabels;
  onClose: () => void;
}) {
  const { schema } = useProductSchema();
  const [state, setState] = useState<{ kind: 'loading' } | { kind: 'ready'; url: string } | { kind: 'unavailable' }>({ kind: 'loading' });
  const backdrop = useMemo(() => artboardBackdrop(schema.canvas), [schema.canvas]);
  const checker = useMemo(() => (backdrop.kind === 'checker' ? checkerTile(backdrop) : null), [backdrop]);
  const checkerURL = useMemo(() => checker?.toDataURL('image/png') ?? null, [checker]);
  useEffect(() => {
    let active = true;
    let objectUrl: string | null = null;
    const controller = new AbortController();
    const release = () => {
      if (objectUrl) URL.revokeObjectURL(objectUrl);
      objectUrl = null;
    };
    setState({ kind: 'loading' });
    void (async () => {
      try {
        const url = safeAssetURL(assetUrl);
        if (!url || !Number.isSafeInteger(width) || width <= 0 || !Number.isSafeInteger(height) || height <= 0) {
          throw new Error('mask source invalid');
        }
        const response = await fetch(url, { credentials: 'same-origin', signal: controller.signal });
        if (!response.ok) throw new Error('mask unavailable');
        if (!active) return;
        const blob = await response.blob();
        if (!active) return;
        if (blob.type && !blob.type.startsWith('image/')) throw new Error('mask is not an image');
        objectUrl = URL.createObjectURL(blob);
        const image = new Image();
        await new Promise<void>((resolve, reject) => {
          image.onload = () => resolve();
          image.onerror = () => reject(new Error('mask decode failed'));
          image.src = objectUrl!;
        });
        if (!active) { release(); return; }
        if (image.naturalWidth !== width || image.naturalHeight !== height) {
          release();
          throw new Error('mask size mismatch');
        }
        setState({ kind: 'ready', url: objectUrl! });
      } catch {
        release();
        if (active) setState({ kind: 'unavailable' });
      }
    })();
    return () => { active = false; controller.abort(); release(); };
  }, [assetUrl, width, height]);
  return (
    <Dialog open onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <DialogContent closeLabel={labels.close} className="flex flex-col gap-0 p-5">
        <DialogTitle className="mb-3 text-sm font-semibold">{labels.title}</DialogTitle>
        <p className="mb-3 text-xs leading-5 text-muted-foreground">{labels.note}</p>
        <div className="relative overflow-hidden rounded-md border border-border p-2" data-testid="inpaint-mask-checker">
          {backdrop.kind === 'checker' && checkerURL ? (
            <div
              aria-hidden
              className="absolute inset-0"
              style={{
                backgroundImage: `url("${checkerURL}")`,
                backgroundRepeat: 'repeat',
                backgroundSize: `${checker!.width}px ${checker!.height}px`,
                opacity: backdrop.opacity,
              }}
              data-testid="inpaint-mask-checker-tile"
            />
          ) : backdrop.kind === 'fill' ? (
            <div aria-hidden className="absolute inset-0" style={{ backgroundColor: backdrop.color }} />
          ) : null}
          {state.kind === 'ready' ? (
            <img src={state.url} alt={labels.note} className="relative max-h-[46vh] w-full object-contain" data-testid="inpaint-mask-image" />
          ) : null}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">{formatSchemaText(labels.size, { size: `${width}×${height}` })}</p>
        {state.kind === 'loading' ? <p className="mt-2 text-xs text-muted-foreground" data-testid="inpaint-mask-loading">{labels.loading}</p> : null}
        {state.kind === 'unavailable' ? <p role="alert" className="mt-2 text-xs text-warning" data-testid="inpaint-mask-unavailable">{labels.unavailable}</p> : null}
      </DialogContent>
    </Dialog>
  );
}

export function MaskDraftBanner({ draft, artboard, labels, selection }: {
  draft: MaskDraft | null;
  artboard: { x: number; y: number; width: number; height: number } | null;
  labels: { title: string; count: (count: number) => string; empty: string; scope: string; previewAlt: string };
  selection: { tint_color: string; tint_opacity: number; preview_max_edge: number };
}) {
  const preview = useMemo(() => {
    if (!draft || !artboard || draft.strokes.length === 0) return null;
    return renderSelectionPreview(draft.strokes, artboard, {
      tintColor: selection.tint_color,
      tintOpacity: selection.tint_opacity,
      maxEdge: selection.preview_max_edge,
    })?.toDataURL('image/png') ?? null;
  }, [draft, artboard, selection.preview_max_edge, selection.tint_color, selection.tint_opacity]);
  return (
    <div className="border-b border-border px-2 py-2" data-testid="canvas-draft-mask">
      <p className="text-xs font-medium">{labels.title}</p>
      <p className="mt-0.5 text-[11px] text-muted-foreground">
        {draft?.strokes.length ? labels.count(draft.strokes.length) : labels.empty}
      </p>
      <p className="mt-0.5 text-[11px] text-muted-foreground">{labels.scope}</p>
      {preview ? <img src={preview} alt={labels.previewAlt} className="mt-1 h-12 w-20 object-contain" data-testid="canvas-draft-mask-preview" /> : null}
    </div>
  );
}

/**
 * The layer list: top layer first, groups as folders (collapsible, with their own eye and lock), and rows
 * that can be dragged above / below another row or into a group. A drop is one undo step (arrangeLayers).
 */
export function CanvasLayersPanel({
  sessionId,
  onClose,
}: {
  sessionId: string;
  /** Floating panel with a close button; without it the list is embedded (the right panel's 图层 tab). */
  onClose?: () => void;
}) {
  const { schema, text } = useProductSchema();
  const nodes = useCanvasStore((state) => state.nodesBySession[sessionId] ?? EMPTY_NODES);
  const selectedIds = useCanvasStore((state) => state.selectedNodeIds);
  const selectNodes = useCanvasStore((state) => state.selectNodes);
  const toggleVisibility = useCanvasStore((state) => state.toggleNodeVisibility);
  const toggleLock = useCanvasStore((state) => state.toggleNodeLock);
  const toggleGroupVisibility = useCanvasStore((state) => state.toggleGroupVisibility);
  const toggleGroupLock = useCanvasStore((state) => state.toggleGroupLock);
  const arrange = useCanvasStore((state) => state.arrangeLayers);
  const removeNode = useCanvasStore((state) => state.removeNode);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [hint, setHint] = useState<DropHint | null>(null);
  const [maskView, setMaskView] = useState<{ source: InpaintSource; sessionId: string } | null>(null);
  const drag = useRef<DragState | null>(null);
  const rows = useMemo(() => layerRows(nodes, collapsed), [nodes, collapsed]);
  // The draft mask belongs to the canvas, not to a layer: show its presence and real shape separately.
  const maskDraft = useMaskStore((state) => state.draft);
  const currentDraft = maskDraft?.sessionId === sessionId ? maskDraft : null;
  const canvasSize = useCanvasStore((state) => state.canvasSizeBySession[sessionId]);
  const draftArtboard = useMemo(() => {
    if (currentDraft?.frame) return currentDraft.frame;
    if (!canvasSize) return null;
    try { return canvasArtboard(schema.canvas.frontend.artboard, canvasSize); } catch { return null; }
  }, [canvasSize, currentDraft, schema.canvas.frontend.artboard]);
  useEffect(() => { setMaskView(null); }, [sessionId]);
  const draftBanner = (
    <MaskDraftBanner
      draft={currentDraft}
      artboard={draftArtboard}
      selection={schema.canvas.frontend.selection}
      labels={{
        title: text('ui.canvas.layers.draft_mask_title'),
        count: (count) => schemaCountMessage(schema, 'ui.canvas.layers.draft_mask_count', count, { count }),
        empty: text('ui.canvas.layers.draft_mask_empty'),
        scope: text('ui.canvas.layers.draft_mask_scope'),
        previewAlt: text('ui.canvas.layers.draft_mask_preview_alt'),
      }}
    />
  );
  const visibleMask = maskView?.sessionId === sessionId ? maskView.source : null;
  const maskViewUrl = visibleMask ? inpaintMaskURL(schema, visibleMask) : '';
  const maskDialog = visibleMask && maskViewUrl ? (
    <InpaintMaskDialog
      assetUrl={maskViewUrl}
      width={visibleMask.width}
      height={visibleMask.height}
      onClose={() => setMaskView(null)}
      labels={{
        title: text('ui.canvas.layers.inpaint_mask_title'),
        note: text('ui.canvas.layers.inpaint_mask_note'),
        loading: text('ui.canvas.layers.inpaint_mask_loading'),
        unavailable: text('ui.canvas.layers.inpaint_mask_unavailable'),
        size: text('ui.canvas.layers.inpaint_mask_size'),
        close: text('ui.dialog.close'),
      }}
    />
  ) : null;

  const toggleCollapsed = (groupId: string) => setCollapsed((current) => {
    const next = new Set(current);
    if (next.has(groupId)) next.delete(groupId);
    else next.add(groupId);
    return next;
  });

  const positionOf = (event: React.DragEvent, isGroup: boolean): DropPosition => {
    const rect = event.currentTarget.getBoundingClientRect();
    const ratio = rect.height > 0 ? (event.clientY - rect.top) / rect.height : 0.5;
    if (isGroup) return ratio < 0.35 ? 'above' : 'into';
    return ratio < 0.5 ? 'above' : 'below';
  };
  const dragProps = (key: string, ids: string[], target: DropTarget) => ({
    draggable: true,
    onDragStart: (event: React.DragEvent) => {
      drag.current = { ids };
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', key);
    },
    onDragEnd: () => { drag.current = null; setHint(null); },
    onDragOver: (event: React.DragEvent) => {
      if (!drag.current) return;
      const position = positionOf(event, target.groupId !== undefined);
      if (arrangeLayers(nodes, drag.current.ids, { ...target, position }) === null) {
        setHint(null);
        return;
      }
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      setHint((current) => (current?.key === key && current.position === position ? current : { key, position }));
    },
    onDrop: (event: React.DragEvent) => {
      const moving = drag.current;
      drag.current = null;
      setHint(null);
      if (!moving) return;
      event.preventDefault();
      const arrangement = arrangeLayers(nodes, moving.ids, { ...target, position: positionOf(event, target.groupId !== undefined) });
      if (arrangement) arrange(sessionId, arrangement.order, arrangement.groupIds);
    },
  });
  const hintClass = (key: string) => {
    if (hint?.key !== key) return '';
    if (hint.position === 'above') return 'shadow-[inset_0_2px_0_0_var(--color-primary)]';
    if (hint.position === 'below') return 'shadow-[inset_0_-2px_0_0_var(--color-primary)]';
    return 'ring-1 ring-primary';
  };
  const iconButton = 'opacity-70 group-hover:opacity-100 focus-visible:opacity-100';

  const list = (
    <ul className={cn('min-h-0 flex-1 overflow-y-auto', onClose ? 'p-1.5' : 'px-2 py-1.5')} data-testid="canvas-layers-list">
      {rows.length === 0 ? (
        <li className="px-2 py-6 text-center text-xs text-muted-foreground">{text('ui.canvas.layers.empty')}</li>
      ) : (
        rows.map((row) => {
          if (row.kind === 'group') {
            const allSelected = row.memberIds.every((id) => selectedIds.includes(id));
            const label = schemaCountMessage(schema, 'ui.canvas.layers.group', row.memberIds.length, { count: row.memberIds.length });
            return (
              <li key={row.key} data-testid="layer-group-row" data-group={row.groupId}>
                <div
                  {...dragProps(row.key, row.memberIds, { groupId: row.groupId, position: 'into' })}
                  className={cn(
                    'group flex items-center gap-1 rounded-lg px-1 py-1.5 text-xs',
                    allSelected ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
                    !row.visible && 'opacity-50',
                    hintClass(row.key),
                  )}
                >
                  <GripVertical className="size-3 shrink-0 cursor-grab opacity-50" aria-hidden />
                  <HintTooltip name={text(row.collapsed ? 'ui.canvas.layers.expand_group' : 'ui.canvas.layers.collapse_group')} className="shrink-0">
                    <Action variant="ghost" size="icon-xs" type="button"
                      aria-expanded={!row.collapsed}
                      aria-label={text(row.collapsed ? 'ui.canvas.layers.expand_group' : 'ui.canvas.layers.collapse_group')}
                      onClick={() => toggleCollapsed(row.groupId)}
                    >
                      {row.collapsed ? <ChevronRight className="size-3.5" /> : <ChevronDown className="size-3.5" />}
                    </Action>
                  </HintTooltip>
                  <Action variant="ghost" size="xs" type="button" className="min-w-0 flex-1 justify-start" aria-pressed={allSelected}
                    onClick={() => selectNodes(row.memberIds)}>
                    <Folder className="size-3.5" />
                    <span className="truncate">{label}</span>
                  </Action>
                  <HintTooltip name={row.visible ? text('ui.canvas.layers.visibility_hide') : text('ui.canvas.layers.visibility_show')} className="shrink-0">
                    <Action variant="ghost" size="icon-xs" type="button"
                      aria-label={row.visible ? text('ui.canvas.layers.visibility_hide') : text('ui.canvas.layers.visibility_show')}
                      onClick={() => toggleGroupVisibility(sessionId, row.groupId)} className={iconButton}>
                      {row.visible ? <Eye className="size-3.5" /> : <EyeOff className="size-3.5" />}
                    </Action>
                  </HintTooltip>
                  <HintTooltip name={row.locked ? text('ui.canvas.layers.unlock') : text('ui.canvas.layers.lock')} className="shrink-0">
                    <Action variant="ghost" size="icon-xs" type="button"
                      aria-label={row.locked ? text('ui.canvas.layers.unlock') : text('ui.canvas.layers.lock')}
                      onClick={() => toggleGroupLock(sessionId, row.groupId)} className={iconButton}>
                      {row.locked ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
                    </Action>
                  </HintTooltip>
                </div>
              </li>
            );
          }
          const node = row.node;
          const hidden = node.visible === false;
          const label = layerName(node, text, schema.localization?.persisted_default_texts);
          const inpaint = node.type === 'image' && isInpaintSource(node.inpaint) ? node.inpaint : null;
          return (
            <li key={row.key} data-testid="layer-row" data-node={node.id}>
              <div
                {...dragProps(layerRowKey(node.id), [node.id], { nodeId: node.id, position: 'above' })}
                className={cn(
                  'group flex items-center gap-1 rounded-lg py-1.5 pr-1 text-xs',
                  row.depth === 1 ? 'pl-6' : 'pl-1',
                  selectedIds.includes(node.id) ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
                  hidden && 'opacity-50',
                  hintClass(layerRowKey(node.id)),
                )}
              >
                <GripVertical className="size-3 shrink-0 cursor-grab opacity-50" aria-hidden />
                <HintTooltip name={label} className="min-w-0 flex-1">
                  <Action variant="ghost" size="xs" type="button" className="w-full justify-start" aria-pressed={selectedIds.includes(node.id)}
                    onClick={() => selectNodes([node.id])}>
                    {typeIcon(node)}
                    <span className="truncate">{label}</span>
                  </Action>
                </HintTooltip>
                {inpaint ? (
                  <HintTooltip name={text('ui.canvas.layers.inpaint_mask_view')} className="shrink-0">
                    <Action variant="ghost" size="icon-xs" type="button"
                      aria-label={text('ui.canvas.layers.inpaint_mask_view')}
                      onClick={() => setMaskView({ source: inpaint, sessionId })} className={iconButton}>
                      <Scan className="size-3.5" />
                    </Action>
                  </HintTooltip>
                ) : null}
                <HintTooltip name={hidden ? text('ui.canvas.layers.visibility_show') : text('ui.canvas.layers.visibility_hide')} className="shrink-0">
                  <Action variant="ghost" size="icon-xs" type="button"
                    aria-label={hidden ? text('ui.canvas.layers.visibility_show') : text('ui.canvas.layers.visibility_hide')}
                    onClick={() => toggleVisibility(sessionId, node.id)} className={iconButton}>
                    {hidden ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                  </Action>
                </HintTooltip>
                <HintTooltip name={node.locked ? text('ui.canvas.layers.unlock') : text('ui.canvas.layers.lock')} className="shrink-0">
                  <Action variant="ghost" size="icon-xs" type="button"
                    aria-label={node.locked ? text('ui.canvas.layers.unlock') : text('ui.canvas.layers.lock')}
                    onClick={() => toggleLock(sessionId, node.id)} className={iconButton}>
                    {node.locked ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
                  </Action>
                </HintTooltip>
                <HintTooltip name={text('ui.canvas.action.delete')} className="shrink-0">
                  <Action variant="destructive" size="icon-xs" type="button"
                    aria-label={text('ui.canvas.action.delete')}
                    onClick={() => removeNode(sessionId, node.id)} className={iconButton}>
                    <Trash2 className="size-3.5" />
                  </Action>
                </HintTooltip>
              </div>
            </li>
          );
        })
      )}
    </ul>
  );
  if (!onClose) {
    return <div className="flex min-h-0 flex-1 flex-col">{draftBanner}{list}{maskDialog}</div>;
  }
  return (
    <>
      <Surface as="aside" variant="frosted" radius="xl" elevation="xl" className="absolute right-3 top-3 z-float flex max-h-[calc(100%-96px)] w-56 flex-col">
        <header className="flex items-center justify-between border-b border-border px-3 py-2">
          <h3 className="text-xs font-semibold">{text('ui.canvas.layers.title')}</h3>
          <Action variant="ghost" size="icon-xs" type="button" onClick={onClose} aria-label={schemaText(schema, 'ui.canvas.layers.close')}>
            <X className="size-3.5" />
          </Action>
        </header>
        {draftBanner}
        {list}
      </Surface>
      {maskDialog}
    </>
  );
}

