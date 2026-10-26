'use client';

import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import type { ModelViewerElement } from '@google/model-viewer';
import { Maximize, Maximize2, X } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { COMPARE_MODES, compareCapacity, isModelSlot, sliderSlots, type CompareMode, type CompareSlot } from '@/lib/workspace/compare';
import { clampPan, zoomAt, ZOOM_MINIMUM, ZOOM_STEP, type View } from '@/lib/workspace/lightbox';
import { cn } from '@/lib/utils';
import { useStudioStore } from '@/store/studio-store';
import { HintTooltip } from '@/components/ui/hint-tooltip';

const SLIDER_STEP = 5;
const FIT: View = { zoom: ZOOM_MINIMUM, x: 0, y: 0 };

/**
 * The compared models share one orbit: turning or zooming one of them turns the others to
 * the same side, at the same relative distance (each model keeps its own framing, so a big
 * and a small model still fill their panes alike). Only a person's own interaction is copied
 * on; the copy itself reports another source and is not copied back. Panning stays per pane,
 * since the models' centres do not line up.
 */
function useSyncedCameras() {
  const viewers = useRef(new Map<string, { element: ModelViewerElement; framed: number; unfollow: () => void }>());
  const follow = useCallback((key: string) => {
    const source = viewers.current.get(key);
    if (!source) return;
    const orbit = source.element.getCameraOrbit();
    const fieldOfView = source.element.getFieldOfView();
    const distance = source.framed > 0 ? orbit.radius / source.framed : null;
    for (const [otherKey, other] of viewers.current) {
      if (otherKey === key) continue;
      const radius = distance !== null && other.framed > 0 ? `${distance * other.framed}m` : 'auto';
      other.element.cameraOrbit = `${orbit.theta}rad ${orbit.phi}rad ${radius}`;
      other.element.fieldOfView = `${fieldOfView}deg`;
      other.element.jumpCameraToGoal();
    }
  }, []);
  const register = useCallback((key: string, element: ModelViewerElement | null) => {
    viewers.current.get(key)?.unfollow();
    viewers.current.delete(key);
    if (!element) return;
    const entry = { element, framed: 0, unfollow: () => {} };
    // The framed distance is where the viewer put the camera once the model was in.
    const onLoad = () => { entry.framed = element.getCameraOrbit().radius; };
    const onChange = (event: Event) => {
      const detail = (event as CustomEvent<{ source?: string }>).detail;
      if (detail?.source === 'user-interaction') follow(key);
    };
    element.addEventListener('load', onLoad);
    element.addEventListener('camera-change', onChange);
    if (element.loaded) onLoad();
    entry.unfollow = () => {
      element.removeEventListener('load', onLoad);
      element.removeEventListener('camera-change', onChange);
    };
    viewers.current.set(key, entry);
  }, [follow]);
  useEffect(() => () => {
    for (const entry of viewers.current.values()) entry.unfollow();
    viewers.current.clear();
  }, []);
  return register;
}

/** A model pane: the model turns here, together with the other compared models. */
function ModelPane({ slot, onRemove, onElement }: {
  slot: CompareSlot;
  onRemove: (key: string) => void;
  onElement: (key: string, element: ModelViewerElement | null) => void;
}) {
  const { schema } = useProductSchema();
  return (
    <figure className="relative flex min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-panel" data-testid="compare-pane" data-kind="model">
      <div className="min-h-0 flex-1 p-2">
        <ModelViewer3D
          key={slot.key}
          src={safeAssetURL(slot.url)}
          alt={slot.label}
          taskId={slot.taskId}
          autoRotate={false}
          className="aspect-auto h-full max-w-none"
          onElement={(element) => onElement(slot.key, element)}
        />
      </div>
      <figcaption className="flex h-8 shrink-0 items-center gap-1 border-t border-border px-2 text-xs">
        <span className="min-w-0 flex-1 truncate font-medium">{slot.label}</span>
        <HintTooltip name={schemaText(schema, 'ui.studio.compare.remove')}>
          <Action type="button" variant="ghost" size="icon-xs" aria-label={schemaText(schema, 'ui.studio.compare.remove')} onClick={() => onRemove(slot.key)} data-testid="compare-remove"><X /></Action>
        </HintTooltip>
      </figcaption>
    </figure>
  );
}

/**
 * One pane. The zoom and pan are the comparison's, not the pane's: the wheel or a drag in
 * any pane moves them all, so the same spot of every picture stays side by side.
 */
function Pane({ slot, view, onView, onRemove, onEnlarge }: {
  slot: CompareSlot | undefined;
  view: View;
  onView: (update: (current: View) => View) => void;
  onRemove: (key: string) => void;
  onEnlarge: (slot: CompareSlot) => void;
}) {
  const { schema } = useProductSchema();
  const box = useRef<HTMLDivElement>(null);
  const drag = useRef<{ x: number; y: number; view: View } | null>(null);
  const hasPicture = slot !== undefined;

  // The wheel must be a non-passive listener to stop the page behind from scrolling.
  useEffect(() => {
    const element = box.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const rect = element.getBoundingClientRect();
      const pointer = { x: event.clientX - rect.left - rect.width / 2, y: event.clientY - rect.top - rect.height / 2 };
      const factor = event.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP;
      onView((current) => clampPan(zoomAt(current, factor, pointer), { width: rect.width, height: rect.height }));
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => element.removeEventListener('wheel', onWheel);
  }, [hasPicture, onView]);

  if (!slot) {
    return (
      <div className="flex min-h-0 items-center justify-center rounded-lg border border-dashed border-border p-4 text-center text-xs text-muted-foreground" data-testid="compare-empty-pane">
        {schemaText(schema, 'ui.studio.compare.empty_slot')}
      </div>
    );
  }
  const panning = view.zoom > ZOOM_MINIMUM;
  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || !panning) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    drag.current = { x: event.clientX, y: event.clientY, view };
  };
  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const start = drag.current;
    const rect = box.current?.getBoundingClientRect();
    if (!start || !rect) return;
    const next = { ...start.view, x: start.view.x + event.clientX - start.x, y: start.view.y + event.clientY - start.y };
    onView(() => clampPan(next, { width: rect.width, height: rect.height }));
  };
  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    drag.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  };
  return (
    <figure className="relative flex min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-panel" data-testid="compare-pane">
      <div
        ref={box}
        className={cn('flex min-h-0 flex-1 items-center justify-center overflow-hidden p-2', panning ? 'cursor-grab active:cursor-grabbing' : 'cursor-default')}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={() => { drag.current = null; }}
      >
        <img
          src={safeAssetURL(slot.url)}
          alt={slot.label}
          className="max-h-full max-w-full select-none object-contain"
          style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.zoom})` }}
          draggable={false}
          onDoubleClick={() => onEnlarge(slot)}
        />
      </div>
      <figcaption className="flex h-8 shrink-0 items-center gap-1 border-t border-border px-2 text-xs">
        <span className="min-w-0 flex-1 truncate font-medium">{slot.label}</span>
        <HintTooltip name={schemaText(schema, 'ui.studio.compare.enlarge')}>
          <Action type="button" variant="ghost" size="icon-xs" aria-label={schemaText(schema, 'ui.studio.compare.enlarge')} onClick={() => onEnlarge(slot)}><Maximize2 /></Action>
        </HintTooltip>
        <HintTooltip name={schemaText(schema, 'ui.studio.compare.remove')}>
          <Action type="button" variant="ghost" size="icon-xs" aria-label={schemaText(schema, 'ui.studio.compare.remove')} onClick={() => onRemove(slot.key)} data-testid="compare-remove"><X /></Action>
        </HintTooltip>
      </figcaption>
    </figure>
  );
}

function SliderCompare({ slots: picked }: { slots: readonly CompareSlot[] }) {
  const { schema } = useProductSchema();
  const [position, setPosition] = useState(50);
  const frame = useRef<HTMLDivElement>(null);
  const dragging = useRef(false);
  // A model cannot be sliced by the slider: the pictures are kept, and the view says what to do with the models.
  const slots = sliderSlots(picked);
  const modelsLeftOut = slots.length < picked.length;
  const [before, after] = slots;

  const update = (clientX: number) => {
    const rect = frame.current?.getBoundingClientRect();
    if (!rect || rect.width === 0) return;
    setPosition(Math.min(100, Math.max(0, ((clientX - rect.left) / rect.width) * 100)));
  };
  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    dragging.current = true;
    event.currentTarget.setPointerCapture(event.pointerId);
    update(event.clientX);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'ArrowLeft') { event.preventDefault(); setPosition((value) => Math.max(0, value - SLIDER_STEP)); }
    if (event.key === 'ArrowRight') { event.preventDefault(); setPosition((value) => Math.min(100, value + SLIDER_STEP)); }
    if (event.key === 'Home') { event.preventDefault(); setPosition(0); }
    if (event.key === 'End') { event.preventDefault(); setPosition(100); }
  };

  if (!before || !after) {
    return (
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground" data-testid="compare-slider-need">
        {before ? <img src={safeAssetURL(before.url)} alt={before.label} className="max-h-[60%] max-w-[60%] object-contain" /> : null}
        <p>{schemaText(schema, modelsLeftOut ? 'ui.studio.compare.slider_images_only' : before ? 'ui.studio.compare.slider_need' : 'ui.studio.compare.empty_slot')}</p>
      </div>
    );
  }
  return (
    <div
      ref={frame}
      className="relative min-h-0 flex-1 touch-none select-none overflow-hidden rounded-lg border border-border bg-panel"
      onPointerDown={onPointerDown}
      onPointerMove={(event) => { if (dragging.current) update(event.clientX); }}
      onPointerUp={(event) => { dragging.current = false; if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); }}
      data-testid="compare-slider"
    >
      <img src={safeAssetURL(after.url)} alt={after.label} draggable={false} className="absolute inset-0 size-full object-contain" />
      <img src={safeAssetURL(before.url)} alt={before.label} draggable={false} className="absolute inset-0 size-full object-contain" style={{ clipPath: `inset(0 ${100 - position}% 0 0)` }} />
      <span className="pointer-events-none absolute left-2 top-2 max-w-[40%] truncate rounded-sm bg-panel/90 px-1.5 py-0.5 text-[11px]">{before.label}</span>
      <span className="pointer-events-none absolute right-2 top-2 max-w-[40%] truncate rounded-sm bg-panel/90 px-1.5 py-0.5 text-[11px]">{after.label}</span>
      <div
        role="slider"
        tabIndex={0}
        aria-label={schemaText(schema, 'ui.studio.compare.slider_label')}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(position)}
        onKeyDown={onKeyDown}
        data-testid="compare-slider-handle"
        className="absolute inset-y-0 z-sticky flex w-5 -translate-x-1/2 cursor-ew-resize items-center justify-center outline-none focus-visible:[&>span]:bg-primary"
        style={{ left: `${position}%` }}
      >
        <span className="h-full w-0.5 bg-foreground/80" />
        <span className="absolute flex size-7 items-center justify-center rounded-full border border-border bg-panel-raised text-[10px] text-muted-foreground shadow-lg">
          {schemaText(schema, 'ui.studio.compare.left')}|{schemaText(schema, 'ui.studio.compare.right')}
        </span>
      </div>
    </div>
  );
}

/** 比较：盖在舞台上，2 联、4 联或滑块；版本从下面的版本条里选。 */
export function CompareView({ onEnlarge }: { onEnlarge: (slots: readonly CompareSlot[], index: number) => void }) {
  const { schema } = useProductSchema();
  const compare = useStudioStore((state) => state.compare);
  const setMode = useStudioStore((state) => state.setCompareMode);
  const removeSlot = useStudioStore((state) => state.removeCompareSlot);
  const end = useStudioStore((state) => state.endCompare);
  const notice = useStudioStore((state) => state.compareNotice);
  const [view, setView] = useState<View>(FIT);
  const registerViewer = useSyncedCameras();
  const mode = compare?.mode;
  // A new way of laying the panes out starts from the whole picture again.
  useEffect(() => { setView(FIT); }, [mode]);
  if (!compare) return null;
  const { slots } = compare;
  const modeLabel = (value: CompareMode) => schemaText(schema, `ui.studio.compare.mode_${value}`);
  const panes = compareCapacity(compare.mode);
  const hasModel = slots.some(isModelSlot);
  const enlarge = (slot: CompareSlot) => onEnlarge(slots, Math.max(0, slots.findIndex((item) => item.key === slot.key)));
  const gestureHint = compare.mode === 'slider' ? '' : ` · ${schemaText(schema, hasModel ? 'ui.studio.compare.sync_hint' : 'ui.studio.compare.zoom_hint')}`;

  return (
    <div className="absolute inset-0 z-float flex min-h-0 flex-col gap-2 bg-canvas p-3" data-testid="compare-view" data-mode={compare.mode}>
      <div className="flex shrink-0 items-center gap-3">
        <h3 className="text-sm font-semibold">{schemaText(schema, 'ui.studio.compare.title')}</h3>
        <div role="radiogroup" aria-label={schemaText(schema, 'ui.studio.compare.mode')} className="flex items-center rounded-lg border border-border bg-panel p-0.5">
          {COMPARE_MODES.map((value) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={compare.mode === value}
              onClick={() => setMode(value)}
              data-testid={`compare-mode-${value}`}
              className={cn('h-6 rounded-md px-2.5 text-xs transition-colors focus-visible:outline-2 focus-visible:outline-ring', compare.mode === value ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:text-foreground')}
            >
              {modeLabel(value)}
            </button>
          ))}
        </div>
        <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{schemaCountMessage(schema, 'ui.studio.compare.hint', panes, { count: panes })}{gestureHint}</span>
        {compare.mode !== 'slider' && view.zoom > ZOOM_MINIMUM ? (
          <HintTooltip name={schemaText(schema, 'ui.studio.compare.zoom_reset')}>
            <Action type="button" variant="ghost" size="sm" onClick={() => setView(FIT)} data-testid="compare-zoom-reset">
              <Maximize /><span className="tabular-nums" data-testid="compare-zoom">{Math.round(view.zoom * 100)}%</span>
            </Action>
          </HintTooltip>
        ) : null}
        <Action type="button" variant="outline" size="sm" onClick={end} data-testid="compare-exit"><X />{schemaText(schema, 'ui.studio.compare.exit')}</Action>
      </div>
      {notice ? (
        <p role="status" className="shrink-0 rounded-md border border-warning/40 bg-warning/10 px-3 py-1.5 text-xs" data-testid="compare-notice">
          {schemaText(schema, 'ui.studio.compare.unavailable')}
        </p>
      ) : null}
      {compare.mode === 'slider' ? (
        <SliderCompare slots={slots} />
      ) : (
        <div className={cn('grid min-h-0 flex-1 gap-2', compare.mode === 'four' ? 'grid-cols-2 grid-rows-2' : 'grid-cols-2 grid-rows-1')} data-testid="compare-grid">
          {Array.from({ length: panes }, (_, index) => {
            const slot = slots[index];
            if (slot && isModelSlot(slot)) return <ModelPane key={slot.key} slot={slot} onRemove={removeSlot} onElement={registerViewer} />;
            return <Pane key={slot?.key ?? `empty-${index}`} slot={slot} view={view} onView={setView} onRemove={removeSlot} onEnlarge={enlarge} />;
          })}
        </div>
      )}
    </div>
  );
}
