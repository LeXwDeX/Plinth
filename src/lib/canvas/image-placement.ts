import type { CanvasProductSchema } from '@/lib/product-schema';
import { computeNodeSize, type CanvasNode } from '@/store/canvas-store';
import type { CanvasArtboard } from './artboard';
import { isBlankLayer } from './initial-layer';

/** True while no picture overlaps the dashed frame: the frame is still empty (an unpainted initial layer is not a picture). */
export function artboardIsFree(nodes: readonly CanvasNode[], artboard: CanvasArtboard): boolean {
  return !nodes.some((node) =>
    node.type === 'image' && !isBlankLayer(node) &&
    node.x < artboard.x + artboard.width && artboard.x < node.x + node.width &&
    node.y < artboard.y + artboard.height && artboard.y < node.y + node.height,
  );
}

/**
 * #246: one world unit is one pixel, so a picture keeps its own pixels, only shrunk (never enlarged) to fit
 * inside the canvas. Without a canvas the Schema's node edge still bounds it.
 */
export function canvasNodeSize(
  natural: { width: number; height: number },
  frontend: Pick<CanvasProductSchema['frontend'], 'maximum_node_edge' | 'drawing'>,
  artboard?: CanvasArtboard | null,
): { width: number; height: number } {
  if (!artboard || natural.width <= 0 || natural.height <= 0) {
    return computeNodeSize(natural.width, natural.height, frontend.maximum_node_edge, frontend.drawing.default_node_size);
  }
  const scale = Math.min(artboard.width / natural.width, artboard.height / natural.height, 1);
  return { width: Math.round(natural.width * scale), height: Math.round(natural.height * scale) };
}

/** Use the same Schema geometry for a restored image and a model screenshot. */
export function imageNodePlacement(
  nodes: readonly CanvasNode[],
  natural: { width: number; height: number },
  frontend: CanvasProductSchema['frontend'],
  artboard?: CanvasArtboard,
): { x: number; y: number; width: number; height: number } {
  const imageNodes = nodes.filter((node) => node.type === 'image' && !isBlankLayer(node));
  const artboardFree = artboard && artboardIsFree(nodes, artboard);
  if (artboardFree && natural.width > 0 && natural.height > 0) {
    // #246: centred at its own pixels, only shrunk (never enlarged) to fit the canvas.
    const fit = Math.min(artboard.width / natural.width, artboard.height / natural.height, 1);
    const width = natural.width * fit;
    const height = natural.height * fit;
    return {
      width, height,
      x: artboard.x + (artboard.width - width) / 2,
      y: artboard.y + (artboard.height - height) / 2,
    };
  }
  const size = canvasNodeSize(natural, frontend, artboard);
  const rightmost = imageNodes.reduce<CanvasNode | null>(
    (last, node) => !last || node.x + node.width > last.x + last.width ? node : last, null,
  );
  // #284: the frame holds the person's layout and is what the next turn sends. A picture placed after the
  // rightmost one used to start inside the frame whenever that one ended inside it, covering a strip of the layout
  // (and of the next snapshot). It now starts after the frame's right edge too: next to the frame, at its top,
  // when the frame reaches further right; otherwise next to the rightmost picture, as before.
  if (artboard && rightmost && rightmost.x + rightmost.width <= artboard.x + artboard.width) {
    return { ...size, x: artboard.x + artboard.width + frontend.node_gap_pixels, y: artboard.y };
  }
  return {
    ...size,
    x: rightmost ? rightmost.x + rightmost.width + frontend.node_gap_pixels : frontend.initial_node_x,
    y: rightmost ? rightmost.y : frontend.initial_node_y,
  };
}
