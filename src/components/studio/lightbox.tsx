'use client';

import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { Check, ChevronLeft, ChevronRight, Download, Maximize, Minus, Plus, ScanSearch, X } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action, buttonVariants } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useSessionPick } from '@/hooks/use-session-pick';
import { useSessionVersions } from '@/hooks/use-session-versions';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { objectOfTile } from '@/lib/workspace/version-object';
import { useChatStore } from '@/store/chat-store';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { clampPan, clampZoom, zoomAt, ZOOM_MINIMUM, ZOOM_STEP, type View } from '@/lib/workspace/lightbox';
import { cn } from '@/lib/utils';
import { useStudioStore } from '@/store/studio-store';

const DOUBLE_CLICK_ZOOM = 2.5;
const FIT: View = { zoom: ZOOM_MINIMUM, x: 0, y: 0 };

/**
 * 全窗口大图：滚轮或按钮缩放、拖动平移、双击在适合窗口与放大之间切换，左右切换版本。
 * 键盘（空格、Esc、←/→）由工作台的快捷键统一处理，输入框里不会触发。
 */
export function Lightbox() {
  const lightbox = useStudioStore((state) => state.lightbox);
  if (!lightbox) return null;
  return <LightboxView />;
}

function LightboxView() {
  const { schema } = useProductSchema();
  const lightbox = useStudioStore((state) => state.lightbox);
  const close = useStudioStore((state) => state.closeLightbox);
  const step = useStudioStore((state) => state.stepLightbox);
  const [view, setView] = useState<View>(FIT);
  const [broken, setBroken] = useState(false);
  const frame = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const image = useRef<HTMLImageElement>(null);
  const drag = useRef<{ x: number; y: number; view: View; moved: boolean } | null>(null);
  const item = lightbox ? lightbox.items[lightbox.index] : undefined;
  const itemKey = item?.key;
  const sessionId = useChatStore((state) => state.activeSessionId);
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { pick, busy: pickBusy, choose } = useSessionPick(chat, sessionId);
  const versions = useSessionVersions();
  const { openOnStage } = useObjectActions();
  const picked = Boolean(item?.pick && pick?.kind === 'image' && pick.taskId === item.pick.taskId && pick.assetId === item.pick.assetId);
  // Choosing a candidate also puts it on the stage, so the next request starts from the one picked.
  const pickCurrent = () => {
    const target = item?.pick;
    if (!target || !sessionId) return;
    void choose({ kind: 'image', taskId: target.taskId, assetId: target.assetId }).then((ok) => {
      if (!ok) { toast.error(schemaText(schema, 'ui.studio.lightbox.pick_failed')); return; }
      const tile = versions.tiles.find((entry) => entry.key === versions.byTask.get(target.taskId));
      const object = tile ? objectOfTile(tile, sessionId, target) : null;
      if (object) openOnStage(object);
    });
  };

  useEffect(() => {
    setView(FIT);
    setBroken(false);
  }, [itemKey]);

  // The wheel must be a non-passive listener to stop the page behind from scrolling.
  const boxSize = useCallback(() => {
    const element = box.current;
    return { width: element?.clientWidth ?? 0, height: element?.clientHeight ?? 0 };
  }, []);
  useEffect(() => {
    const element = box.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const rect = element.getBoundingClientRect();
      const pointer = { x: event.clientX - rect.left - rect.width / 2, y: event.clientY - rect.top - rect.height / 2 };
      const factor = event.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP;
      setView((current) => clampPan(zoomAt(current, factor, pointer), boxSize()));
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => element.removeEventListener('wheel', onWheel);
  }, [boxSize, itemKey]);

  const text = (key: string) => schemaText(schema, `ui.studio.lightbox.${key}`);
  const zoomBy = (factor: number) => setView((current) => clampPan(zoomAt(current, factor, { x: 0, y: 0 }), boxSize()));
  const actualSize = () => {
    const element = image.current;
    if (!element || element.clientWidth === 0) return;
    const zoom = clampZoom(element.naturalWidth / element.clientWidth);
    setView(zoom <= ZOOM_MINIMUM ? FIT : { zoom, x: 0, y: 0 });
  };
  const percent = (() => {
    const element = image.current;
    if (!element || element.naturalWidth === 0 || element.clientWidth === 0) return Math.round(view.zoom * 100);
    return Math.round((element.clientWidth * view.zoom / element.naturalWidth) * 100);
  })();

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    drag.current = { x: event.clientX, y: event.clientY, view, moved: false };
  };
  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const start = drag.current;
    if (!start) return;
    const dx = event.clientX - start.x;
    const dy = event.clientY - start.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) start.moved = true;
    if (start.view.zoom > ZOOM_MINIMUM) setView(clampPan({ ...start.view, x: start.view.x + dx, y: start.view.y + dy }, boxSize()));
  };
  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const start = drag.current;
    drag.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    // A plain click on the empty backdrop closes; a click on the picture does not.
    if (start && !start.moved && event.target === event.currentTarget) close();
  };

  if (!lightbox || !item) return null;
  const many = lightbox.items.length > 1;
  const panning = view.zoom > ZOOM_MINIMUM;

  return (
    <Dialog
      open
      // Esc is the stage's own key (it leaves the large view, then compare, then focus), so the dialog does not take it.
      onOpenChange={(open, details) => {
        if (open) return;
        if (details.reason === 'escape-key') { details.cancel(); return; }
        close();
      }}
    >
    <DialogContent
      ref={frame}
      // Focus rests on the frame, not a button, so Space stays the toggle and not a zoom click.
      initialFocus={frame}
      showCloseButton={false}
      aria-label={text('title')}
      data-testid="studio-lightbox"
      data-shortcut-passthrough
      data-shortcut-arrows
      overlayClassName="bg-black/90 backdrop-blur-none"
      className="top-0 left-0 flex h-dvh w-screen max-w-none translate-x-0 translate-y-0 flex-col gap-0 bg-transparent p-0 text-foreground ring-0 sm:max-w-none"
    >
      <header className="flex h-12 shrink-0 items-center gap-3 px-4">
        <h2 className="min-w-0 truncate text-sm font-medium" data-testid="lightbox-label">{item.label}</h2>
        {many ? (
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground" data-testid="lightbox-counter">
            {formatSchemaText(text('counter'), { current: lightbox.index + 1, total: lightbox.items.length })}
          </span>
        ) : null}
        <span className="flex-1" />
        <div className="flex items-center gap-0.5 rounded-lg bg-panel/80 p-0.5" role="group" aria-label={text('zoom')}>
          <HintTooltip name={text('zoom_out')}>
            <Action type="button" variant="ghost" size="icon-sm" onClick={() => zoomBy(1 / ZOOM_STEP)} disabled={view.zoom <= ZOOM_MINIMUM} aria-label={text('zoom_out')}><Minus /></Action>
          </HintTooltip>
          <output className="w-12 text-center text-xs tabular-nums" data-testid="lightbox-zoom">{percent}%</output>
          <HintTooltip name={text('zoom_in')}>
            <Action type="button" variant="ghost" size="icon-sm" onClick={() => zoomBy(ZOOM_STEP)} aria-label={text('zoom_in')}><Plus /></Action>
          </HintTooltip>
          <HintTooltip name={text('fit')}>
            <Action type="button" variant="ghost" size="icon-sm" onClick={() => setView(FIT)} aria-label={text('fit')}><Maximize /></Action>
          </HintTooltip>
          <HintTooltip name={text('actual')}>
            <Action type="button" variant="ghost" size="icon-sm" onClick={actualSize} aria-label={text('actual')}><ScanSearch /></Action>
          </HintTooltip>
        </div>
        {item.pick ? (
          <Action
            type="button"
            size="sm"
            variant={picked ? 'secondary' : 'default'}
            disabled={pickBusy || picked || !sessionId}
            aria-pressed={picked}
            onClick={pickCurrent}
            onPointerDown={(event) => event.stopPropagation()}
            data-testid="lightbox-pick"
            data-picked={picked}
          >
            {picked ? <Check /> : null}{schemaText(schema, picked ? 'ui.studio.lightbox.picked' : 'ui.studio.lightbox.pick')}
          </Action>
        ) : null}
        {safeAssetURL(item.downloadUrl) ? (
          <HintTooltip name={text('download')}>
            <a href={safeAssetURL(item.downloadUrl)} download aria-label={text('download')} className={cn(buttonVariants({ variant: 'ghost', size: 'icon-sm' }))}>
              <Download />
            </a>
          </HintTooltip>
        ) : null}
        <HintTooltip name={text('close')}>
          <Action type="button" variant="ghost" size="icon-sm" onClick={close} aria-label={text('close')} data-testid="lightbox-close"><X /></Action>
        </HintTooltip>
      </header>
      <div
        ref={box}
        className={cn('relative flex min-h-0 flex-1 items-center justify-center overflow-hidden', panning ? 'cursor-grab active:cursor-grabbing' : 'cursor-default')}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={() => { drag.current = null; }}
        onDoubleClick={() => setView((current) => (current.zoom > ZOOM_MINIMUM ? FIT : { zoom: DOUBLE_CLICK_ZOOM, x: 0, y: 0 }))}
      >
        {broken ? (
          <p role="status" className="text-sm text-muted-foreground">{text('missing')}</p>
        ) : (
          <img
            ref={image}
            src={safeAssetURL(item.url)}
            alt={item.label}
            draggable={false}
            onLoad={() => setView((current) => ({ ...current }))}
            onError={() => setBroken(true)}
            style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.zoom})` }}
            className="max-h-full max-w-full select-none object-contain"
            data-testid="lightbox-image"
          />
        )}
        {many ? (
          <>
            <HintTooltip name={text('previous')} className="absolute left-4 top-1/2 -translate-y-1/2">
              <Action type="button" variant="media" size="icon-lg" onClick={() => step(-1)} onPointerDown={(event) => event.stopPropagation()} className="rounded-full" aria-label={text('previous')} data-testid="lightbox-previous"><ChevronLeft /></Action>
            </HintTooltip>
            <HintTooltip name={text('next')} className="absolute right-4 top-1/2 -translate-y-1/2">
              <Action type="button" variant="media" size="icon-lg" onClick={() => step(1)} onPointerDown={(event) => event.stopPropagation()} className="rounded-full" aria-label={text('next')} data-testid="lightbox-next"><ChevronRight /></Action>
            </HintTooltip>
          </>
        ) : null}
      </div>
      <footer className="flex h-9 shrink-0 items-center justify-center text-[11px] text-muted-foreground">{text('hint')}</footer>
    </DialogContent>
    </Dialog>
  );
}
