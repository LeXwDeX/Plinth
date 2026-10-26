'use client';

import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { Check, LocateFixed, Maximize, Minus, Plus } from 'lucide-react';
import { toast } from 'sonner';

import { RailButton } from '@/components/canvas/canvas-tool-rail';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Surface } from '@/components/ui/surface';
import { createCanvasBrowserClient } from '@/lib/canvas/browser-client';
import { cn } from '@/lib/utils';
import { canvasDefaultViewport, useCanvasStore } from '@/store/canvas-store';
import { fitArtboardViewport, zoomAroundCenter, zoomStepViewport } from '@/lib/canvas/viewport';
import { descriptorCaps } from '@/lib/workspace/key-dispatch';
import { shortcutCaps } from '@/lib/workspace/stage-actions';
import { stageFitInsets } from '@/lib/workspace/tool-rail-layout';
import { useStudioStore } from '@/store/studio-store';

/**
 * 2D 舞台右下角的缩放：缩小、当前比例（点击回到 100%）、放大、适应画面，
 * 以及画布底色。适应画面用"最大化"图标，不再用减号和方块。
 */
export function CanvasZoomControls({ sessionId, containerRef, artboard = null, onRecenter, spaceTapTaken }: { sessionId: string; containerRef: RefObject<HTMLDivElement | null>; artboard?: { x: number; y: number; width: number; height: number } | null; onRecenter?: () => void; spaceTapTaken?: () => boolean }) {
  const { schema, text } = useProductSchema();
  const canvasSchema = schema.canvas;
  const viewportSchema = canvasSchema.frontend.viewport;
  const viewport = useCanvasStore((state) => state.viewportBySession[sessionId]) ?? canvasDefaultViewport(viewportSchema);
  const setViewport = useCanvasStore((state) => state.setViewport);
  const fitToScreen = useCanvasStore((state) => state.fitToScreen);
  const keyboard = canvasSchema.frontend.keyboard;
  const railExpanded = useStudioStore((state) => state.railExpanded);
  // Buttons and shortcuts share one rule: a step or a jump around the middle of the drawing area, fit = the frame fills it.
  const drawingSize = () => ({ width: containerRef.current?.clientWidth ?? 0, height: containerRef.current?.clientHeight ?? 0 });
  const stepZoom = (direction: 1 | -1) => setViewport(sessionId, zoomStepViewport(viewport, drawingSize(), direction, viewportSchema));
  const canvasBg = useCanvasStore((state) => state.bgStyleBySession[sessionId]) ?? canvasSchema.defaults.bg_style;
  const setBgStyle = useCanvasStore((state) => state.setBgStyleForSession);
  const [bgOpen, setBgOpen] = useState(false);
  const bgRef = useRef<HTMLDivElement>(null);
  const client = useMemo(() => createCanvasBrowserClient({
    schema: canvasSchema,
    routes: {
      projects: schema.runtime.http.legacy_routes.canvas_projects,
      project: schema.runtime.http.legacy_routes.canvas_project,
      nodes: schema.runtime.http.legacy_routes.canvas_nodes,
      merge: schema.runtime.http.legacy_routes.canvas_merge,
    },
  }), [canvasSchema, schema.runtime.http.legacy_routes]);
  // The background is the canvas project's: it is saved with the project, so a reload or another session
  // switch shows the one picked here instead of the project's earlier value (#277).
  const chooseBackground = async (value: string) => {
    setBgOpen(false);
    if (value === canvasBg) return;
    setBgStyle(sessionId, value);
    const projectId = useCanvasStore.getState().projectBySession[sessionId];
    if (!projectId) return;
    try {
      await client.updateProject(projectId, { bgStyle: value });
    } catch {
      toast.error(text('ui.canvas.background_save_failed'));
    }
  };

  useEffect(() => {
    if (!bgOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!bgRef.current?.contains(event.target as Node)) setBgOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setBgOpen(false);
    };
    window.addEventListener('pointerdown', onPointerDown);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('pointerdown', onPointerDown);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [bgOpen]);

  const percent = `${Math.round(viewport.zoom * viewportSchema.percentage_multiplier)}%`;
  const background = canvasSchema.frontend.backgrounds.find((item) => item.value === canvasBg);

  return (
    <Surface
      variant="toolbar"
      radius="xl"
      elevation="lg"
      role="toolbar"
      aria-label={text('ui.studio.canvas.zoom')}
      data-testid="canvas-zoom-controls"
      className="absolute bottom-3 right-3 z-float flex items-center gap-0.5 p-1"
    >
      <RailButton label={text('ui.canvas.toolbar.zoom_out')} hint={{ shortcut: shortcutCaps(schema, 'zoom_out_shortcut') }} onClick={() => stepZoom(-1)}>
        <Minus className="size-4" />
      </RailButton>
      <HintTooltip name={text('ui.canvas.toolbar.reset')} shortcut={shortcutCaps(schema, 'zoom_actual_shortcut')}>
        <Action
          type="button"
          variant="ghost"
          size="sm"
          aria-label={`${text('ui.canvas.toolbar.reset')} ${percent}`}
          onClick={() => setViewport(sessionId, zoomAroundCenter(viewport, drawingSize(), 1, viewportSchema))}
          className="min-w-12 px-1.5 tabular-nums text-muted-foreground hover:text-foreground"
        >
          {percent}
        </Action>
      </HintTooltip>
      <RailButton label={text('ui.canvas.toolbar.zoom_in')} hint={{ shortcut: shortcutCaps(schema, 'zoom_in_shortcut') }} onClick={() => stepZoom(1)}>
        <Plus className="size-4" />
      </RailButton>
      <RailButton
        label={text('ui.canvas.toolbar.fit')}
        hint={{ shortcut: descriptorCaps(`mod+${keyboard.recenter_key}`, text) }}
        onClick={() => {
          const element = containerRef.current;
          if (!element) return;
          // With a dashed frame the frame fills the area (the same rule as Ctrl+0); without one, fit every layer.
          const view = artboard ? fitArtboardViewport(artboard, drawingSize(), viewportSchema, stageFitInsets(railExpanded)) : null;
          if (view) setViewport(sessionId, view);
          else fitToScreen(sessionId, element.clientWidth, element.clientHeight);
        }}
      >
        <Maximize className="size-4" />
      </RailButton>
      {onRecenter ? (
        // A Space tap only recenters while the stage shows no picture (a picture makes the tap open the large view),
        // so the Space caps are left out while that tap is taken.
        <RailButton label={text('ui.canvas.toolbar.recenter')} hint={{ shortcut: spaceTapTaken?.() ? undefined : shortcutCaps(schema, 'space_code') }} onClick={onRecenter}>
          <LocateFixed className="size-4" />
        </RailButton>
      ) : null}
      <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
      <div className="relative" ref={bgRef}>
        <RailButton label={text('ui.canvas.toolbar.background')} active={bgOpen} onClick={() => setBgOpen((value) => !value)}>
          <span className="size-4 rounded-md border border-border" style={{ backgroundColor: background?.background_color }} />
        </RailButton>
        {bgOpen ? (
          <CanvasBackgroundMenu
            options={canvasSchema.frontend.backgrounds}
            value={canvasBg}
            label={text('ui.canvas.toolbar.background')}
            optionLabel={(value) => text(`ui.canvas.toolbar.background.${value}`)}
            onChoose={(value) => { void chooseBackground(value); }}
          />
        ) : null}
      </div>
    </Surface>
  );
}

/**
 * The background choices of the zoom bar, from the Schema. The current one is a checked radio with a check
 * mark in its own column, kept while the pointer or the keyboard focus is on another row: hover only tints a
 * row and focus only draws the ring, so neither looks like the choice (#277).
 */
export function CanvasBackgroundMenu({ options, value, label, optionLabel, onChoose }: {
  options: ReadonlyArray<{ value: string; background_color: string }>;
  value: string;
  label: string;
  optionLabel: (value: string) => string;
  onChoose: (value: string) => void;
}) {
  return (
    <Surface
      variant="panel"
      radius="xl"
      elevation="xl"
      role="radiogroup"
      aria-label={label}
      data-testid="canvas-background-menu"
      className="absolute bottom-full right-0 z-float mb-2 flex flex-col gap-0.5 p-1"
    >
      {options.map((item) => {
        const selected = value === item.value;
        return (
          <button
            key={item.value}
            type="button"
            role="radio"
            aria-checked={selected}
            data-testid="canvas-background-option"
            data-value={item.value}
            onClick={() => onChoose(item.value)}
            className={cn(
              'flex h-8 items-center gap-2 whitespace-nowrap rounded-md pl-1.5 pr-3 text-left text-xs transition-colors focus-visible:outline-2 focus-visible:outline-ring',
              selected ? 'bg-primary/15 font-medium text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
            )}
          >
            <span data-slot="background-check" className="flex size-3.5 shrink-0 text-primary">{selected ? <Check aria-hidden className="size-3.5" /> : null}</span>
            <span aria-hidden className={cn('size-4 shrink-0 rounded-sm border', selected ? 'border-primary' : 'border-border')} style={{ backgroundColor: item.background_color }} />
            {optionLabel(item.value)}
          </button>
        );
      })}
    </Surface>
  );
}
