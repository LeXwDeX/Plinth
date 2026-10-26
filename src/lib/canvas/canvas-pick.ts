/**
 * Which layers a pointer gesture on the canvas can pick (marquee, crop). An unpainted initial layer (#246)
 * covers the whole canvas, so picking it would select, move or delete the canvas itself with every marquee;
 * it is picked only in the layers panel until it has been painted.
 */
import type { CanvasNode } from '@/store/canvas-store';
import { rotatedBounds } from './export-canvas';
import { isBlankLayer } from './initial-layer';

/** Layers a canvas gesture may pick: visible, unlocked, and not an unpainted initial layer. */
export function pickableOnCanvas(node: Pick<CanvasNode, 'type' | 'blank' | 'visible' | 'locked'>): boolean {
  return node.visible !== false && !node.locked && !isBlankLayer(node);
}

/** The layers a marquee touches (by their rotated bounds). */
export function marqueeSelection(
  nodes: readonly CanvasNode[],
  rect: { left: number; top: number; right: number; bottom: number },
): string[] {
  return nodes
    .filter((node) => {
      if (!pickableOnCanvas(node)) return false;
      const bounds = rotatedBounds(node);
      return bounds.minX < rect.right && bounds.maxX > rect.left && bounds.minY < rect.bottom && bounds.maxY > rect.top;
    })
    .map((node) => node.id);
}

/** The top-most unrotated picture under the point that the crop tool can crop. */
export function cropTargetAt(nodesBottomToTop: readonly CanvasNode[], point: { x: number; y: number }): CanvasNode | undefined {
  return [...nodesBottomToTop]
    .filter((node) => node.type === 'image' && pickableOnCanvas(node) && !node.rotation)
    .reverse()
    .find((node) => point.x >= node.x && point.x <= node.x + node.width && point.y >= node.y && point.y <= node.y + node.height);
}
