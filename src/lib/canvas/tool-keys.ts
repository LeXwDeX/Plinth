/**
 * Tool keys, Photoshop style: one bare key per tool, read from the Schema's keyboard settings.
 * Shift on the brush key swaps brush and pencil, Shift on the shape key steps through the shape cycle.
 * Pure: the board applies the result (switching tool, remembering the shape kind).
 */
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasToolType, ShapeType } from '@/store/canvas-store';

type Keyboard = CanvasProductSchema['frontend']['keyboard'];

export interface ToolKeyEvent {
  key: string;
  shiftKey: boolean;
}

export interface ToolKeyContext {
  tool: CanvasToolType;
  shapeType: ShapeType;
}

export interface ToolKeyResult {
  tool: CanvasToolType;
  /** Set when the key also decides the shape kind. */
  shapeType?: ShapeType;
}

/** The tool a key selects, or null when the key is not a tool key. */
export function resolveToolKey(event: ToolKeyEvent, keyboard: Keyboard, context: ToolKeyContext): ToolKeyResult | null {
  const key = event.key.toLowerCase();
  const is = (configured: string) => configured.toLowerCase() === key;

  if (is(keyboard.select_tool_key)) return { tool: 'select' };
  if (is(keyboard.hand_tool_key)) return { tool: 'hand' };
  if (is(keyboard.marquee_tool_key)) return { tool: 'marquee' };
  if (is(keyboard.lasso_tool_key)) return { tool: 'lasso' };
  if (is(keyboard.mask_tool_key)) return { tool: 'mask' };
  if (is(keyboard.crop_tool_key)) return { tool: 'crop' };
  if (is(keyboard.eraser_tool_key)) return { tool: 'eraser' };
  if (is(keyboard.text_tool_key)) return { tool: 'text' };
  if (is(keyboard.zoom_tool_key)) return { tool: 'zoom' };
  if (is(keyboard.eyedropper_tool_key)) return { tool: 'eyedropper' };
  if (is(keyboard.brush_tool_key)) {
    // Shift+B walks the group: brush -> pencil -> brush. From any other tool it lands on the brush.
    if (event.shiftKey) return { tool: context.tool === 'brush' ? 'pencil' : 'brush' };
    return { tool: 'brush' };
  }
  if (is(keyboard.shape_tool_key)) {
    if (!event.shiftKey || context.tool !== 'shape') return { tool: 'shape' };
    return { tool: 'shape', shapeType: nextShape(keyboard.shape_cycle, context.shapeType) };
  }
  return null;
}

/** The shape after `current` in the Schema's cycle (the first one when `current` is not listed). */
export function nextShape(cycle: readonly ShapeType[], current: ShapeType): ShapeType {
  const index = cycle.indexOf(current);
  return cycle[(index + 1) % cycle.length] ?? current;
}

/** A drag moves the view: the hand tool, or Space held on any tool (Photoshop's temporary hand). */
export function pansView(tool: CanvasToolType, spaceDown: boolean): boolean {
  return tool === 'hand' || spaceDown;
}

/**
 * A drag moves the layer under the pointer: only with the move tool, never a locked layer, and never while
 * Space is held — the stage pans then, wherever the drag starts (a draggable picture would take it first).
 */
export function layerDraggable(tool: CanvasToolType, spaceDown: boolean, locked: boolean | undefined): boolean {
  return tool === 'select' && !spaceDown && !locked;
}
