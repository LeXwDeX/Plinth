/**
 * #246 phase 2: a new canvas starts with one bitmap layer covering it, so the brush and eraser work without
 * inserting a picture first. It is an ordinary picture layer (selectable in the layers panel, hideable,
 * deletable, reorderable, never locked); until the first stroke is committed it has no picture yet
 * (`blank`, empty imageUrl) and only the Schema's fill. A transparent blank layer draws nothing, so it is not
 * canvas content: exporting, the "frame is still free" rule and picture actions pass it by.
 */
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import type { CanvasArtboard } from './artboard';

export function isBlankLayer(node: Pick<CanvasNode, 'type' | 'blank'>): boolean {
  return node.type === 'image' && node.blank === true;
}

/** The colour a blank layer is filled with; null when it is transparent (or not a blank layer). */
export function blankLayerFill(node: Pick<CanvasNode, 'type' | 'blank' | 'fillColor'>, transparentValue?: string): string | null {
  if (!isBlankLayer(node) || !node.fillColor || node.fillColor === transparentValue) return null;
  return node.fillColor;
}

/** A blank layer that draws nothing at all. */
export function isTransparentBlank(node: Pick<CanvasNode, 'type' | 'blank' | 'fillColor'>, transparentValue?: string): boolean {
  return isBlankLayer(node) && blankLayerFill(node, transparentValue) === null;
}

/**
 * The bitmap the brush paints an unpainted layer in: the layer's own pixels, scaled down uniformly when that
 * would exceed the Schema's maximum_bitmap_pixels (an 8192x8192 canvas would otherwise allocate ~256 MB).
 */
export function blankBitmapSize(node: Pick<CanvasNode, 'width' | 'height'>, maximumPixels: number): { width: number; height: number } {
  const width = Math.max(1, Math.round(node.width));
  const height = Math.max(1, Math.round(node.height));
  if (!(maximumPixels > 0) || width * height <= maximumPixels) return { width, height };
  const scale = Math.sqrt(maximumPixels / (width * height));
  return { width: Math.max(1, Math.floor(width * scale)), height: Math.max(1, Math.floor(height * scale)) };
}

export function initialLayerNode(
  id: string,
  frame: CanvasArtboard,
  options: { fill: string; name: string; taskId: string },
  transparentValue: string,
): CanvasNode {
  return {
    id, type: 'image', imageUrl: '', taskId: options.taskId, name: options.name,
    x: frame.x, y: frame.y, width: frame.width, height: frame.height, zIndex: 0, blank: true,
    ...(options.fill !== transparentValue ? { fillColor: options.fill } : {}),
  };
}

/**
 * The nodes a canvas starts with after its frame is resolved (`resolveCanvasFrame`): only a canvas that has
 * just been given its first size and was not migrated from legacy units gets the initial layer, and only
 * while it is empty. A saved size, a migrated legacy canvas or a canvas a capture already wrote to keeps its
 * nodes; a conflict reload never comes through here.
 */
export function initialCanvasNodes(
  resolution: { nodes: CanvasNode[]; initialized: boolean; migrated: boolean },
  frame: CanvasArtboard,
  canvas: Pick<CanvasProductSchema, 'frontend'>,
  name: string,
  newId: () => string,
): CanvasNode[] {
  if (!resolution.initialized || resolution.migrated) return resolution.nodes;
  return withInitialLayer(resolution.nodes, frame, canvas, name, newId);
}

/** A new, empty canvas gets the Schema's initial layer; any other canvas is returned as it is. */
export function withInitialLayer(
  nodes: CanvasNode[],
  frame: CanvasArtboard,
  canvas: Pick<CanvasProductSchema, 'frontend'>,
  name: string,
  newId: () => string,
): CanvasNode[] {
  const declared = canvas.frontend.initial_layer;
  if (!declared?.enabled || nodes.length > 0) return nodes;
  return [canvasLayerNode(newId(), frame, canvas, name)];
}

/** A canvas layer (blank, covering the frame) with the Schema's fill. */
export function canvasLayerNode(
  id: string,
  frame: CanvasArtboard,
  canvas: Pick<CanvasProductSchema, 'frontend'>,
  name: string,
): CanvasNode {
  return initialLayerNode(id, frame, { fill: canvas.frontend.initial_layer.fill, name, taskId: canvas.frontend.upload_task_id },
    canvas.frontend.export.transparent_background_value);
}

/**
 * #246: the canvas is always paintable. A brush stroke that starts inside the frame where no picture layer
 * is under the pointer (a canvas from before the initial layer existed, the new area after enlarging, a
 * deleted or hidden canvas layer) gets a new blank canvas layer to land on, placed below every other layer
 * so the #245 rule (the top-most picture under the pointer) still decides where later strokes go. The
 * eraser has nothing to erase there, so it keeps explaining that instead.
 */
export function brushCreatesCanvasLayer(
  state: 'ready' | 'loading' | 'locked' | 'none',
  tool: string,
  point: { x: number; y: number },
  frame: CanvasArtboard | null | undefined,
  enabled: boolean,
): boolean {
  return enabled && tool === 'brush' && state === 'none' && Boolean(frame)
    && point.x >= frame!.x && point.x <= frame!.x + frame!.width
    && point.y >= frame!.y && point.y <= frame!.y + frame!.height;
}
