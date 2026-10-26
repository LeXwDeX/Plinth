'use client';

import { useEffect, useRef, useState } from 'react';
import { ArrowUpRight, Circle, Slash, Square } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { ToolRail, type ToolHandler } from '@/components/studio/tool-rail';
import { Action } from '@/components/ui/button';
import { HintTooltip, type HintContent } from '@/components/ui/hint-tooltip';
import { Surface } from '@/components/ui/surface';
import { useRailExpanded } from '@/hooks/use-rail-expanded';
import type { CanvasToolId } from '@/lib/workspace/tools';
import { cn } from '@/lib/utils';
import { useCanvasStore, type CanvasToolType, type ShapeType } from '@/store/canvas-store';

export const SHAPE_ICONS: Record<ShapeType, typeof Square> = {
  rectangle: Square,
  arrow: ArrowUpRight,
  line: Slash,
  ellipse: Circle,
};

interface RailButtonProps {
  label: string;
  /** What the hover hint adds under the name (`label`): the shortcut caps, a hint line, the reason it is disabled. */
  hint?: Omit<HintContent, 'name'>;
  /** Where the hint opens: below the button, or to its right from a vertical column (the shape flyout). */
  hintSide?: 'bottom' | 'right';
  active?: boolean;
  disabled?: boolean;
  onClick: () => void;
  className?: string;
  children: React.ReactNode;
}

export function RailButton({ label, hint, hintSide, active, disabled, onClick, className, children }: RailButtonProps) {
  return (
    <HintTooltip name={label} side={hintSide} {...hint}>
      <Action
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-pressed={active}
        disabled={disabled}
        onClick={onClick}
        className={cn(
          'transition-colors',
          active ? 'bg-primary/20 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
          className,
        )}
      >
        {children}
      </Action>
    </HintTooltip>
  );
}

export interface CanvasToolRailProps {
  sessionId: string;
  activeTool: CanvasToolType;
  onToolChange: (tool: CanvasToolType) => void;
  onUploadImage: () => void;
  /** 所选模型是否支持蒙版局部重绘（按 Offering 参数声明判断）。 */
  maskSupported?: boolean;
  /** 点击蒙版工具但当前模型不支持时回调（由外层弹确认切换模型）。 */
  onMaskUnsupported?: () => void;
  className?: string;
}

/**
 * 2D 舞台左侧的竖向工具栏。有哪些工具、顺序、分组、图标、名字、悬停说明和快捷键都来自 Schema
 * `canvas.frontend.tools`（rail 为 canvas），画法在 `ToolRail`；这里只给每个工具 id 接上处理函数和状态：
 * 当前工具、撤销重做可用、吸附开关、形状下拉，以及模型不支持蒙版时的原因。
 */
export function CanvasToolRail({
  sessionId, activeTool, onToolChange, onUploadImage, maskSupported = true, onMaskUnsupported, className,
}: CanvasToolRailProps) {
  const { schema, text } = useProductSchema();
  const [expanded, setExpanded] = useRailExpanded();
  const activeShapeType = useCanvasStore((state) => state.activeShapeType);
  const setActiveShapeType = useCanvasStore((state) => state.setActiveShapeType);
  const undo = useCanvasStore((state) => state.undo);
  const redo = useCanvasStore((state) => state.redo);
  const canUndo = useCanvasStore((state) => state.canUndo(sessionId));
  const canRedo = useCanvasStore((state) => state.canRedo(sessionId));
  const snapDefault = schema.canvas.frontend.snap.default_enabled;
  const snapEnabled = useCanvasStore((state) => state.snapEnabledOverride ?? snapDefault);
  const setSnapEnabled = useCanvasStore((state) => state.setSnapEnabled);
  const [shapesOpen, setShapesOpen] = useState(false);
  const shapesRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!shapesOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!shapesRef.current?.contains(event.target as Node)) setShapesOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setShapesOpen(false);
    };
    window.addEventListener('pointerdown', onPointerDown);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('pointerdown', onPointerDown);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [shapesOpen]);

  const ActiveShape = SHAPE_ICONS[activeShapeType] ?? Square;
  /** A tool that is current while the board's active tool is it. */
  const pick = (tool: CanvasToolType): ToolHandler => ({ run: () => onToolChange(tool), pressed: activeTool === tool });

  // One handler per Schema tool id. `Record<CanvasToolId, ...>` makes a missing id a compile error, and
  // tools.test.ts checks the ids the Schema declares are exactly these.
  const handlers: Record<CanvasToolId, ToolHandler> = {
    select: pick('select'),
    hand: pick('hand'),
    marquee: pick('marquee'),
    lasso: pick('lasso'),
    // A model without mask parameters keeps the tool clickable: the click offers a model that has them.
    mask: {
      run: () => {
        if (!maskSupported) {
          onMaskUnsupported?.();
          return;
        }
        onToolChange('mask');
      },
      pressed: activeTool === 'mask',
      disabled: !maskSupported && !onMaskUnsupported,
      reason: maskSupported ? undefined : text('ui.canvas.mask.unsupported_tooltip'),
      dimmed: !maskSupported,
    },
    eyedropper: pick('eyedropper'),
    zoom: pick('zoom'),
    upload: { run: onUploadImage },
    shape: {
      run: () => {
        setShapesOpen((value) => !value);
        onToolChange('shape');
      },
      pressed: activeTool === 'shape' || shapesOpen,
      icon: ActiveShape,
      slotRef: shapesRef,
      flyout: shapesOpen ? (
        <Surface variant="panel" radius="xl" elevation="xl" className="absolute left-full top-0 z-float ml-2 flex flex-col gap-0.5 p-1">
          {(Object.keys(SHAPE_ICONS) as ShapeType[]).map((shape) => {
            const Icon = SHAPE_ICONS[shape];
            return (
              <RailButton
                key={shape}
                label={text(`ui.canvas.toolbar.${shape}`)}
                hintSide="right"
                active={activeShapeType === shape}
                onClick={() => {
                  setActiveShapeType(shape);
                  setShapesOpen(false);
                  onToolChange('shape');
                }}
              >
                <Icon className="size-4" />
              </RailButton>
            );
          })}
        </Surface>
      ) : null,
    },
    text: pick('text'),
    pencil: pick('pencil'),
    brush: pick('brush'),
    eraser: pick('eraser'),
    crop: pick('crop'),
    snap: { run: () => setSnapEnabled(!snapEnabled), pressed: snapEnabled },
    undo: { run: () => undo(sessionId), disabled: !canUndo, reason: canUndo ? undefined : text('ui.canvas.toolbar.undo_empty') },
    redo: { run: () => redo(sessionId), disabled: !canRedo, reason: canRedo ? undefined : text('ui.canvas.toolbar.redo_empty') },
  };

  return (
    <ToolRail
      rail="canvas"
      label={text('ui.studio.canvas.tools')}
      handlers={handlers}
      expanded={expanded}
      onExpandedChange={setExpanded}
      testId="canvas-tool-rail"
      itemTestId={(id) => `canvas-tool-${id}`}
      className={cn('left-3 top-3', className)}
    />
  );
}
