'use client';

import type { DragEvent, ReactElement, ReactNode } from 'react';
import { Download, Eye, Info } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { buttonVariants } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from '@/components/ui/context-menu';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { canvasDragItem, startCanvasDrag } from '@/lib/workspace/canvas-drag';
import { formatSchemaText } from '@/lib/product-schema';
import { objectOfTarget, type ResultTarget } from '@/lib/workspace/result-target';
import { cn } from '@/lib/utils';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';

/** Opens the shared 任务详情 on a result. Every card calls this; none keeps its own copy of the dialog. */
export function openResultDetail(target: ResultTarget): void {
  useStudioStore.getState().openResultDetail(target);
}

/** A finished picture a card can be dragged onto the canvas with; null for anything else (generating, failed, 3D, no file). */
export function dragItemOf(target: ResultTarget, proxy: (url: string, taskId?: string) => string, size?: { width: number; height: number } | null) {
  if (target.kind !== 'image' || target.status === 'pending' || target.status === 'running' || target.status === 'failed') return null;
  return canvasDragItem({
    taskId: target.taskId, assetId: target.assetId, url: target.url ? proxy(target.url, target.taskId) : '',
    ...(size ?? {}), label: target.title,
  });
}

/** `draggable` and `onDragStart` for a ready picture card; spread onto the element that is the card. */
export function useCanvasDragSource(target: ResultTarget) {
  const { proxyAssetUrl } = useAssetURLRewriter();
  const item = dragItemOf(target, proxyAssetUrl);
  if (!item) return {};
  return {
    draggable: true,
    onDragStart: (event: DragEvent<HTMLElement>) => {
      const thumbnail = event.currentTarget.querySelector('img');
      startCanvasDrag(event.dataTransfer, item, thumbnail ?? event.currentTarget);
    },
  };
}

/**
 * Download as a link on the card (shown on hover or keyboard focus). Not offered while the version has no file.
 * `className` places the hover-hint wrapper on the card (`absolute ...`); `buttonClassName` sizes the link itself.
 */
export function CardDownload({ target, className, buttonClassName }: { target: ResultTarget; className?: string; buttonClassName?: string }) {
  const { text } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  if (target.status !== 'ready' || !target.url) return null;
  const label = formatSchemaText(text('ui.result.card.download'), { label: target.title });
  return (
    <HintTooltip name={label} className={className}>
      <a
        href={proxyAssetUrl(target.url, target.taskId)}
        download
        onClick={(event) => event.stopPropagation()}
        aria-label={label}
        data-testid="card-download"
        className={cn(buttonVariants({ variant: 'media', size: 'icon-xs' }), 'opacity-0 transition-opacity group-focus-within/card:opacity-100 group-hover/card:opacity-100 focus-visible:opacity-100', buttonClassName)}
      >
        <Download />
      </a>
    </HintTooltip>
  );
}

/**
 * Right-click menu for a card: details, open on the canvas, download. `render` is the card's own
 * element (a li) so the menu adds no wrapper to the layout.
 */
export function ResultContextMenu({ target, render, children }: { target: ResultTarget; render: ReactElement<Record<string, unknown>>; children: ReactNode }) {
  const { text } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { openOnStage } = useObjectActions();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const object = objectOfTarget(target);
  const canOpen = target.status === 'ready' && object !== null && target.sessionId === activeSessionId;
  const href = target.status === 'ready' && target.url ? proxyAssetUrl(target.url, target.taskId) : '';
  return (
    <ContextMenu>
      <ContextMenuTrigger render={render}>{children}</ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onClick={() => openResultDetail(target)} data-testid="card-menu-detail"><Info />{text('ui.result.card.detail')}</ContextMenuItem>
        {canOpen && object ? <ContextMenuItem onClick={() => { openOnStage(object); }} data-testid="card-menu-open-canvas"><Eye />{text('ui.result.detail.open_canvas')}</ContextMenuItem> : null}
        {href ? (
          <ContextMenuItem render={<a href={href} download />} data-testid="card-menu-download"><Download />{text('ui.account.download')}</ContextMenuItem>
        ) : null}
      </ContextMenuContent>
    </ContextMenu>
  );
}
