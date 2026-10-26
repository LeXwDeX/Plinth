/**
 * #246 phase 2: what a canvas resize does to the layers. The boundary changes; content keeps its pixel size
 * and its position relative to the chosen anchor (the canvas is centred on the Schema anchor point, so a
 * centre anchor moves nothing and an edge anchor shifts the layers with that edge). Growing adds empty space;
 * shrinking never drops anything: layers outside the new boundary stay on the canvas (dimmed outside the
 * frame), the export crops them, and an undo or a larger canvas brings them back. Nothing is scaled.
 */
import type { CanvasNode } from '@/store/canvas-store';
import { canvasArtboard, type CanvasArtboard, type CanvasPixels } from './artboard';
import { rotatedBounds } from './export-canvas';
import { isBlankLayer } from './initial-layer';

export type ResizeAnchor =
  | 'top-left' | 'top' | 'top-right'
  | 'left' | 'center' | 'right'
  | 'bottom-left' | 'bottom' | 'bottom-right';

export const RESIZE_ANCHORS: readonly ResizeAnchor[] = [
  'top-left', 'top', 'top-right', 'left', 'center', 'right', 'bottom-left', 'bottom', 'bottom-right',
];

export function isResizeAnchor(value: unknown): value is ResizeAnchor {
  return typeof value === 'string' && (RESIZE_ANCHORS as readonly string[]).includes(value);
}

/** How far content moves so it keeps its distance to the anchored edges. */
export function anchorOffset(from: CanvasPixels, to: CanvasPixels, anchor: ResizeAnchor): { x: number; y: number } {
  const horizontal = anchor.endsWith('left') ? -1 : anchor.endsWith('right') ? 1 : 0;
  const vertical = anchor.startsWith('top') ? -1 : anchor.startsWith('bottom') ? 1 : 0;
  // `+ 0` turns a -0 (an anchor centred on that axis) into 0.
  return { x: horizontal * ((to.width - from.width) / 2) + 0, y: vertical * ((to.height - from.height) / 2) + 0 };
}

const EPSILON = 0.5;

function sameRect(node: CanvasNode, frame: CanvasArtboard): boolean {
  return Math.abs(node.x - frame.x) < EPSILON && Math.abs(node.y - frame.y) < EPSILON
    && Math.abs(node.width - frame.width) < EPSILON && Math.abs(node.height - frame.height) < EPSILON
    && !(node.rotation ?? 0);
}

function inside(node: CanvasNode, frame: CanvasArtboard): boolean {
  const bounds = rotatedBounds(node);
  return bounds.minX >= frame.x - EPSILON && bounds.minY >= frame.y - EPSILON
    && bounds.maxX <= frame.x + frame.width + EPSILON && bounds.maxY <= frame.y + frame.height + EPSILON;
}

export interface CanvasResizeResult {
  nodes: CanvasNode[];
  /** Layers that were inside the old canvas and now reach past the new one (kept, but cropped on export). */
  clipped: string[];
}

export function resizeCanvasNodes(
  nodes: readonly CanvasNode[],
  from: CanvasPixels,
  to: CanvasPixels,
  anchor: ResizeAnchor,
  center: { x: number; y: number },
  /** Layers with a local brush bitmap: content even while still marked unpainted (refitting would stretch it). */
  painted: ReadonlySet<string> = new Set(),
): CanvasResizeResult {
  const anchorPoint = { center_x: center.x, center_y: center.y };
  const before = canvasArtboard(anchorPoint, from);
  const after = canvasArtboard(anchorPoint, to);
  const offset = anchorOffset(from, to, anchor);
  const clipped: string[] = [];
  const resized = nodes.map((node) => {
    // A layer with no picture yet is the canvas itself: it follows the new boundary.
    if (isBlankLayer(node) && !painted.has(node.id) && sameRect(node, before)) {
      return { ...node, x: after.x, y: after.y, width: after.width, height: after.height };
    }
    const moved = offset.x === 0 && offset.y === 0 ? node : { ...node, x: node.x + offset.x, y: node.y + offset.y };
    if (node.visible !== false && node.type !== 'frame' && inside(node, before) && !inside(moved, after)) clipped.push(node.id);
    return moved;
  });
  return { nodes: resized, clipped };
}
