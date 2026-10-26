/**
 * #246: what a canvas read from the server means in pixels. Every path that writes canvas nodes (the board,
 * a 3D capture saved while the board is not mounted, a conflict reload) resolves the frame here, so a canvas
 * is migrated from legacy units at most once and a pixel canvas is never scaled again.
 */
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import { canvasArtboard, clampCanvasPixels, legacyArtboard, migrateLegacyNodes, type CanvasPixels } from './artboard';
import type { CanvasFrame } from './browser-client';

export interface CanvasFrameResolution {
  /** The canvas pixel size to use and to save. */
  size: CanvasPixels;
  /** The nodes in pixel coordinates (scaled only when `migrated`). */
  nodes: CanvasNode[];
  /** The canvas had no saved size: the size is new and must be saved. */
  initialized: boolean;
  /** The nodes were in legacy 512-unit coordinates and were scaled into the pixel frame. */
  migrated: boolean;
}

/**
 * - A saved size is used as it is.
 * - A canvas with the current coordinate version but no size yet (a new project) gets `initial` (the size the
 *   chosen 2D model produces); its nodes are already pixels and stay as they are.
 * - A canvas without a version is legacy: it gets `initial` too, and its nodes are scaled from the old
 *   legacy_max_edge frame of that ratio into the pixel frame around the same centre.
 */
export function resolveCanvasFrame(
  canvas: Pick<CanvasProductSchema, 'frontend' | 'limits'>,
  saved: { frame?: CanvasFrame | null; coordinateVersion?: number | null },
  nodes: CanvasNode[],
  initial: CanvasPixels,
): CanvasFrameResolution {
  if (saved.frame) {
    return { size: { width: saved.frame.width, height: saved.frame.height }, nodes, initialized: false, migrated: false };
  }
  const anchor = canvas.frontend.artboard;
  const size = clampCanvasPixels(initial, canvas.limits) ?? { width: anchor.default_width, height: anchor.default_height };
  if (saved.coordinateVersion || nodes.length === 0) return { size, nodes, initialized: true, migrated: false };
  return {
    size,
    nodes: migrateLegacyNodes(nodes, legacyArtboard(anchor, size), canvasArtboard(anchor, size), {
      strokeWidth: canvas.frontend.style.default_stroke_width,
      fontSize: canvas.frontend.style.default_font_size,
    }),
    initialized: true,
    migrated: true,
  };
}
