'use client';

import { useMemo, useRef, useState, type ReactNode } from 'react';
import {
  AlignCenterHorizontal,
  AlignCenterVertical,
  AlignEndHorizontal,
  AlignEndVertical,
  AlignHorizontalSpaceBetween,
  AlignStartHorizontal,
  AlignStartVertical,
  AlignVerticalSpaceBetween,
  ArrowLeftRight,
  RotateCcw,
} from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { SHAPE_ICONS, RailButton } from '@/components/canvas/canvas-tool-rail';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Slider } from '@/components/ui/slider';
import { Surface } from '@/components/ui/surface';
import { alignUnitCount, type AlignMode, type DistributeAxis, type Rect } from '@/lib/canvas/align';
import { clampChannel, hexToRgb, normalizeHex, rgbToHex } from '@/lib/canvas/color-input';
import { useMaskStore } from '@/store/mask-store';
import { useStudioStore } from '@/store/studio-store';
import { layerBarClearance, railClearance } from '@/lib/workspace/tool-rail-layout';
import { layerBarShown, selectedImageLayer } from '@/lib/canvas/selected-layer';
import { createStyleGesture, selectionStyle, styleUpdates, type StylePatch } from '@/lib/canvas/style-patch';
import { shortcutCaps } from '@/lib/workspace/stage-actions';
import { cn } from '@/lib/utils';
import { useCanvasStore, type CanvasToolType, type ShapeType } from '@/store/canvas-store';

/** Tools whose parameters live in this bar. */
const STYLED_TOOLS = new Set<CanvasToolType>(['shape', 'pencil', 'text']);
const BRUSH_TOOLS = new Set<CanvasToolType>(['brush', 'eraser', 'mask']);
/** Tools that paint with the foreground colour (or pick it): they get the colour fields. */
const COLOR_TOOLS = new Set<CanvasToolType>(['shape', 'pencil', 'text', 'brush', 'eyedropper']);

const ALIGN_BUTTONS: Array<{ mode: AlignMode; icon: typeof AlignStartVertical }> = [
  { mode: 'left', icon: AlignStartVertical },
  { mode: 'hcenter', icon: AlignCenterVertical },
  { mode: 'right', icon: AlignEndVertical },
  { mode: 'top', icon: AlignStartHorizontal },
  { mode: 'vcenter', icon: AlignCenterHorizontal },
  { mode: 'bottom', icon: AlignEndHorizontal },
];

/**
 * Foreground / background colours with a free colour entry: the native picker, a hex field and the
 * red / green / blue channels (range from the Schema), plus swap (X) and default (D).
 */
function ColorFields() {
  const { schema, text } = useProductSchema();
  const style = schema.canvas.frontend.style;
  const foreground = useCanvasStore((state) => state.lastStyle.strokeColor);
  const background = useCanvasStore((state) => state.backgroundColor);
  const setLastStyle = useCanvasStore((state) => state.setLastStyle);
  const setBackgroundColor = useCanvasStore((state) => state.setBackgroundColor);
  const swapColors = useCanvasStore((state) => state.swapColors);
  const resetColors = useCanvasStore((state) => state.resetColors);
  const [draft, setDraft] = useState<string | null>(null);
  const rgb = hexToRgb(foreground) ?? { r: 0, g: 0, b: 0 };
  const commit = (value: string) => {
    const hex = normalizeHex(value);
    if (hex) setLastStyle({ strokeColor: hex });
    setDraft(null);
  };
  const channel = (name: 'r' | 'g' | 'b') => (
    <input
      key={name}
      type="number"
      inputMode="numeric"
      min={style.rgb_minimum}
      max={style.rgb_maximum}
      aria-label={`${text('ui.canvas.style.rgb')} ${name.toUpperCase()}`}
      value={rgb[name]}
      onChange={(event) => setLastStyle({ strokeColor: rgbToHex({ ...rgb, [name]: clampChannel(Number(event.target.value), style) }, style) })}
      className="h-6 w-12 rounded-md border border-border bg-background px-1 text-xs tabular-nums"
    />
  );
  return (
    <div className="flex flex-wrap items-center gap-1.5" data-testid="canvas-color-fields">
      <HintTooltip name={text('ui.canvas.style.custom_color')} className="shrink-0">
        <label className="relative size-6 cursor-pointer">
          <span className="sr-only">{text('ui.canvas.style.custom_color')}</span>
          <input
            type="color"
            value={normalizeHex(foreground) ?? style.default_stroke_color}
            onChange={(event) => setLastStyle({ strokeColor: event.target.value })}
            className="absolute inset-0 size-full cursor-pointer opacity-0"
          />
          <span className="absolute inset-0 rounded-md border border-border" style={{ backgroundColor: foreground }} />
        </label>
      </HintTooltip>
      <input
        type="text"
        aria-label={text('ui.canvas.style.hex')}
        data-testid="canvas-hex-input"
        value={draft ?? foreground}
        maxLength={7}
        spellCheck={false}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => commit(draft ?? foreground)}
        onKeyDown={(event) => { if (event.key === 'Enter') commit(draft ?? foreground); }}
        className="h-6 w-20 rounded-md border border-border bg-background px-1.5 font-mono text-xs"
      />
      {(['r', 'g', 'b'] as const).map(channel)}
      <RailButton label={text('ui.canvas.style.swap_colors')} hint={{ shortcut: shortcutCaps(schema, 'swap_colors_key') }} onClick={swapColors} className="size-6">
        <ArrowLeftRight className="size-3.5" />
      </RailButton>
      <HintTooltip name={text('ui.canvas.style.background')} className="shrink-0">
        <label className="relative size-6 cursor-pointer">
          <span className="sr-only">{text('ui.canvas.style.background')}</span>
          <input
            type="color"
            value={normalizeHex(background) ?? style.default_background_color}
            onChange={(event) => setBackgroundColor(event.target.value)}
            className="absolute inset-0 size-full cursor-pointer opacity-0"
          />
          <span className="absolute inset-0 rounded-md border border-border" style={{ backgroundColor: background }} />
        </label>
      </HintTooltip>
      <RailButton label={text('ui.canvas.style.default_colors')} hint={{ shortcut: shortcutCaps(schema, 'default_colors_key') }} onClick={resetColors} className="size-6">
        <RotateCcw className="size-3.5" />
      </RailButton>
    </div>
  );
}

function Swatch({ color, selected, onClick }: { color: string; selected: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={color}
      aria-pressed={selected}
      onClick={onClick}
      style={{ backgroundColor: color }}
      className={cn(
        'size-5 shrink-0 rounded-md border focus-visible:outline-2 focus-visible:outline-ring',
        selected ? 'border-primary ring-2 ring-primary/40' : 'border-border',
      )}
    />
  );
}

function Group({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center gap-1.5">
      <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
      {children}
    </div>
  );
}

export interface CanvasToolOptionsProps {
  sessionId: string;
  activeTool: CanvasToolType;
  /** Hint text of the tool in use (mask, crop). */
  hint?: string;
  /** Confirm / cancel actions of a tool that has a pending result (mask, crop). */
  actions?: ReactNode;
  /** The dashed frame: the align target when a single layer is selected. */
  artboard?: Rect | null;
}

/**
 * 2D 舞台顶部的工具参数条：随当前工具显示颜色、填充、线宽、笔刷大小、形状种类。
 * 蒙版与裁剪的"应用 / 取消"也在这里，所以画布上不再叠悬浮按钮和弹层。
 */
export function CanvasToolOptions({ sessionId, activeTool, hint, actions, artboard = null }: CanvasToolOptionsProps) {
  const { schema, text } = useProductSchema();
  const style = schema.canvas.frontend.style;
  const activeShapeType = useCanvasStore((state) => state.activeShapeType);
  const setActiveShapeType = useCanvasStore((state) => state.setActiveShapeType);
  const lastStyle = useCanvasStore((state) => state.lastStyle);
  const setLastStyle = useCanvasStore((state) => state.setLastStyle);
  const brushSize = useCanvasStore((state) => state.brushSize);
  const setBrushSize = useCanvasStore((state) => state.setBrushSize);
  const brushRange = schema.canvas.frontend.brush;
  const autoSelect = useCanvasStore((state) => state.autoSelect);
  const setAutoSelect = useCanvasStore((state) => state.setAutoSelect);
  const alignSelected = useCanvasStore((state) => state.alignSelected);
  const distributeSelected = useCanvasStore((state) => state.distributeSelected);
  const erasing = useMaskStore((state) => state.erasing);
  const toggleErasing = useMaskStore((state) => state.toggleErasing);
  // The bar floats beside the tool rail, so it starts where the rail (collapsed or expanded) ends.
  const barLeft = railClearance(useStudioStore((state) => state.railExpanded));

  const nodes = useCanvasStore((state) => state.nodesBySession[sessionId]);
  const selectedIds = useCanvasStore((state) => state.selectedNodeIds);
  const patchNodes = useCanvasStore((state) => state.patchNodes);
  // The floating bar over a selected picture sits at the top-right: keep out of its strip, wrapping instead of overlapping.
  const rightStrip = layerBarShown(activeTool, selectedImageLayer(nodes, selectedIds)) ? layerBarClearance() : 16;
  const gesture = useRef(createStyleGesture());
  const fontSizes = style.font_sizes;
  const defaultFontSize = style.default_font_size;
  const selection = useMemo(
    () => (activeTool === 'select' && nodes ? selectionStyle(nodes, selectedIds, { fontSize: defaultFontSize }) : null),
    [activeTool, nodes, selectedIds, defaultFontSize],
  );
  /** A discrete change (swatch, preset) is one undo entry. */
  const applyOnce = (patch: StylePatch) => {
    if (!nodes) return;
    patchNodes(sessionId, styleUpdates(nodes, selectedIds, patch));
  };
  /** A slider drag is one undo entry: the first tick records, the rest ride on it. */
  const applyGesture = (patch: StylePatch) => {
    if (!nodes) return;
    patchNodes(sessionId, styleUpdates(nodes, selectedIds, patch), { recordHistory: gesture.current.begin() });
  };
  const transparent = style.transparent_fill_value;
  const isTransparent = (color?: string) => !color || color === transparent || color === 'transparent';

  const styled = STYLED_TOOLS.has(activeTool);
  const brush = BRUSH_TOOLS.has(activeTool);
  const units = nodes && activeTool === 'select' ? alignUnitCount(nodes, selectedIds) : 0;
  if (!styled && !brush && !hint && !actions && !selection && activeTool !== 'select' && activeTool !== 'eyedropper') return null;

  return (
    <Surface
      variant="toolbar"
      radius="xl"
      elevation="lg"
      role="toolbar"
      aria-label={text('ui.studio.canvas.options')}
      data-testid="canvas-tool-options"
      style={{ left: barLeft, maxWidth: `calc(100% - ${barLeft + rightStrip}px)` }}
      className="absolute top-3 z-float flex flex-wrap items-center gap-x-3 gap-y-1 px-2.5 py-1.5"
    >
      {activeTool === 'select' ? (
        <>
          <Group label={text('ui.canvas.options.auto_select')}>
            <div className="flex items-center gap-1" role="group" aria-label={text('ui.canvas.options.auto_select')}>
              {(['group', 'layer'] as const).map((mode) => (
                <HintTooltip
                  key={mode}
                  name={text(`ui.canvas.options.auto_select_${mode}`)}
                  hint={mode === 'group' ? text('ui.canvas.options.auto_select_hint') : undefined}
                >
                  <Action
                    type="button"
                    variant="outline"
                    size="xs"
                    data-testid={`auto-select-${mode}`}
                    aria-pressed={autoSelect === mode}
                    onClick={() => setAutoSelect(mode)}
                    className={cn(autoSelect === mode ? 'border-primary text-primary' : 'border-border text-muted-foreground')}
                  >
                    {text(`ui.canvas.options.auto_select_${mode}`)}
                  </Action>
                </HintTooltip>
              ))}
            </div>
          </Group>
          {units >= 2 || (units === 1 && artboard) ? (
            <div className="flex items-center gap-0.5" role="group" aria-label={text('ui.canvas.options.align')} data-testid="canvas-align-bar">
              {ALIGN_BUTTONS.map(({ mode, icon: Icon }) => (
                <RailButton key={mode} label={text(`ui.canvas.options.align_${mode}`)} onClick={() => alignSelected(sessionId, mode, artboard)} className="size-7">
                  <Icon className="size-3.5" />
                </RailButton>
              ))}
              {units >= 3 ? (
                <>
                  {(['horizontal', 'vertical'] as DistributeAxis[]).map((axis) => {
                    const Icon = axis === 'horizontal' ? AlignHorizontalSpaceBetween : AlignVerticalSpaceBetween;
                    return (
                      <RailButton key={axis} label={text(`ui.canvas.options.distribute_${axis}`)} onClick={() => distributeSelected(sessionId, axis)} className="size-7">
                        <Icon className="size-3.5" />
                      </RailButton>
                    );
                  })}
                </>
              ) : null}
            </div>
          ) : null}
        </>
      ) : null}
      {activeTool === 'shape' ? (
        <div className="flex items-center gap-0.5" role="group" aria-label={text('ui.canvas.toolbar.shapes')}>
          {(Object.keys(SHAPE_ICONS) as ShapeType[]).map((shape) => {
            const Icon = SHAPE_ICONS[shape];
            return (
              <RailButton key={shape} label={text(`ui.canvas.toolbar.${shape}`)} active={activeShapeType === shape} onClick={() => setActiveShapeType(shape)} className="size-7">
                <Icon className="size-3.5" />
              </RailButton>
            );
          })}
        </div>
      ) : null}
      {styled ? (
        <Group label={text('ui.canvas.style.line_color')}>
          <div className="flex items-center gap-1">
            {style.preset_colors.map((color) => (
              <Swatch key={color} color={color} selected={lastStyle.strokeColor === color} onClick={() => setLastStyle({ strokeColor: color })} />
            ))}
          </div>
        </Group>
      ) : null}
      {COLOR_TOOLS.has(activeTool) ? <ColorFields /> : null}
      {activeTool === 'shape' ? (
        <Group label={text('ui.canvas.style.fill')}>
          <div className="flex items-center gap-1">
            <Action
              type="button"
              variant="outline"
              size="xs"
              aria-pressed={!lastStyle.fillColor || lastStyle.fillColor === 'transparent'}
              onClick={() => setLastStyle({ fillColor: 'transparent' })}
              className={cn(!lastStyle.fillColor || lastStyle.fillColor === 'transparent' ? 'border-primary text-primary' : 'text-muted-foreground')}
            >
              {text('ui.canvas.style.no_fill')}
            </Action>
            {style.preset_colors.slice(0, 6).map((color) => (
              <Swatch key={color} color={color} selected={lastStyle.fillColor === color} onClick={() => setLastStyle({ fillColor: color })} />
            ))}
          </div>
        </Group>
      ) : null}
      {activeTool === 'shape' || activeTool === 'pencil' ? (
        <Group label={text('ui.canvas.style.line_width')}>
          <div className="flex items-center gap-1">
            {style.stroke_widths.map((width) => (
              <Action
                key={width}
                type="button"
                variant="outline"
                size="xs"
                aria-pressed={lastStyle.strokeWidth === width}
                onClick={() => setLastStyle({ strokeWidth: width })}
                className={cn(lastStyle.strokeWidth === width ? 'border-primary text-primary' : 'border-border text-muted-foreground')}
              >
                {width}
              </Action>
            ))}
          </div>
        </Group>
      ) : null}
      {brush ? (
        <Group label={text('ui.canvas.toolbar.brush_size')}>
          <div className="flex items-center gap-1.5">
            <Slider
              aria-label={text('ui.canvas.toolbar.brush_size')}
              data-testid="brush-size-slider"
              className="w-28"
              min={brushRange.minimum_size}
              max={brushRange.maximum_size}
              step={1}
              value={[brushSize]}
              onValueChange={([value]) => setBrushSize(value)}
            />
            <input
              type="number"
              min={brushRange.minimum_size}
              max={brushRange.maximum_size}
              aria-label={text('ui.canvas.toolbar.brush_size')}
              value={brushSize}
              onChange={(event) => setBrushSize(Number(event.target.value))}
              className="h-6 w-14 rounded-md border border-border bg-background px-1 text-xs tabular-nums"
            />
          </div>
        </Group>
      ) : null}
      {activeTool === 'mask' ? (
        <HintTooltip name={text('ui.canvas.mask.erase_toggle')} shortcut={shortcutCaps(schema, 'swap_colors_key')}>
          <Action
            type="button"
            variant="outline"
            size="xs"
            data-testid="mask-erase-toggle"
            aria-pressed={erasing}
            onClick={toggleErasing}
            className={cn(erasing ? 'border-primary text-primary' : 'border-border text-muted-foreground')}
          >
            {text(erasing ? 'ui.canvas.mask.mode_erase' : 'ui.canvas.mask.mode_paint')}
          </Action>
        </HintTooltip>
      ) : null}
      {selection ? (
        <>
          <Group label={text(selection.hasShape ? 'ui.canvas.style.line_color' : 'ui.canvas.style.font_color')}>
            <div className="flex items-center gap-1">
              {style.preset_colors.map((color) => (
                <Swatch key={color} color={color} selected={selection.strokeColor === color} onClick={() => applyOnce({ strokeColor: color })} />
              ))}
            </div>
          </Group>
          {selection.hasShape ? (
            <Group label={text('ui.canvas.style.fill')}>
              <div className="flex items-center gap-1">
                <Action
                  type="button"
                  variant="outline"
                  size="xs"
                  aria-pressed={isTransparent(selection.fillColor)}
                  onClick={() => applyOnce({ fillColor: transparent })}
                  className={cn(isTransparent(selection.fillColor) ? 'border-primary text-primary' : 'text-muted-foreground')}
                >
                  {text('ui.canvas.style.no_fill')}
                </Action>
                {style.preset_colors.slice(0, 6).map((color) => (
                  <Swatch key={color} color={color} selected={selection.fillColor === color} onClick={() => applyOnce({ fillColor: color })} />
                ))}
              </div>
            </Group>
          ) : null}
          {selection.hasShape ? (
            <Group label={text('ui.canvas.style.line_width')}>
              <div className="flex items-center gap-1">
                {style.stroke_widths.map((width) => (
                  <Action
                    key={width}
                    type="button"
                    variant="outline"
                    size="xs"
                    aria-pressed={selection.strokeWidth === width}
                    onClick={() => applyOnce({ strokeWidth: width })}
                    className={cn(selection.strokeWidth === width ? 'border-primary text-primary' : 'border-border text-muted-foreground')}
                  >
                    {width}
                  </Action>
                ))}
              </div>
            </Group>
          ) : null}
          {selection.hasText && fontSizes.length > 0 ? (
            <Group label={text('ui.canvas.style.font_size')}>
              <div className="flex items-center gap-1">
                {fontSizes.map((size) => (
                  <Action
                    key={size}
                    type="button"
                    variant="outline"
                    size="xs"
                    aria-pressed={selection.fontSize === size}
                    onClick={() => applyOnce({ fontSize: size })}
                    className={cn(selection.fontSize === size ? 'border-primary text-primary' : 'border-border text-muted-foreground')}
                  >
                    {size}
                  </Action>
                ))}
              </div>
            </Group>
          ) : null}
          <Group label={text('ui.canvas.style.opacity')}>
            <Slider
              aria-label={text('ui.canvas.style.opacity')}
              className="w-24"
              min={5}
              max={100}
              step={5}
              value={[Math.round(selection.opacity * 100)]}
              onValueChange={([value]) => applyGesture({ opacity: value / 100 })}
              // onValueCommit can be missed (pointer released outside, focus lost); a new press or the
              // release/blur also closes the gesture so the next drag records its own undo entry.
              onPointerDownCapture={() => gesture.current.end()}
              onPointerUp={() => gesture.current.end()}
              onBlur={() => gesture.current.end()}
              onValueCommit={() => gesture.current.end()}
            />
          </Group>
        </>
      ) : null}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
      {actions ? <div className="ml-auto flex items-center gap-1.5">{actions}</div> : null}
    </Surface>
  );
}
