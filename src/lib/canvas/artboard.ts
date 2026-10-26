import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';

export interface CanvasArtboard {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A canvas size in whole pixels (#246). */
export interface CanvasPixels {
  width: number;
  height: number;
}

type ArtboardSchema = CanvasProductSchema['frontend']['artboard'];

function ratioParts(value: unknown): [number, number] | null {
  if (typeof value !== 'string' || !/^\d+(\.\d+)?:\d+(\.\d+)?$/.test(value.trim())) return null;
  const [width, height] = value.trim().split(':').map(Number);
  return Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0
    ? [width, height]
    : null;
}

/**
 * The ratio shown above the artboard: the nearest of the Schema's common ratios,
 * compared by log(width / height). A size such as 1536x1024 reads as 3:2.
 */
export function artboardRatioLabel(width: number, height: number, commonRatios: readonly string[]): string {
  if (!(width > 0) || !(height > 0)) return '';
  const target = Math.log(width / height);
  let best = '';
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const common of commonRatios) {
    const parts = ratioParts(common);
    if (!parts) continue;
    const distance = Math.abs(Math.log(parts[0] / parts[1]) - target);
    if (distance < bestDistance) {
      best = common;
      bestDistance = distance;
    }
  }
  return best;
}

export function samePixels(left: CanvasPixels | null | undefined, right: CanvasPixels | null | undefined): boolean {
  return Boolean(left && right && left.width === right.width && left.height === right.height);
}

/** Whole pixels inside the Schema's canvas range; null when the size cannot be a canvas. */
export function clampCanvasPixels(
  size: CanvasPixels | null | undefined,
  limits: Pick<CanvasProductSchema['limits'], 'minimum_dimension' | 'maximum_dimension'>,
): CanvasPixels | null {
  if (!size || !Number.isFinite(size.width) || !Number.isFinite(size.height) || size.width <= 0 || size.height <= 0) return null;
  const clamp = (value: number) => Math.min(limits.maximum_dimension, Math.max(limits.minimum_dimension, Math.round(value)));
  return { width: clamp(size.width), height: clamp(size.height) };
}

/**
 * The canvas (the dashed frame) in world coordinates, coordinate version 2: one world unit is one pixel,
 * and the canvas is its pixel size centred on the Schema's anchor. A resize keeps the centre, so content
 * keeps its pixel position and size while the frame grows or shrinks around it.
 */
export function canvasArtboard(anchor: Pick<ArtboardSchema, 'center_x' | 'center_y'>, pixels: CanvasPixels): CanvasArtboard {
  if (!Number.isFinite(anchor.center_x) || !Number.isFinite(anchor.center_y) ||
      !Number.isFinite(pixels.width) || !Number.isFinite(pixels.height) || pixels.width <= 0 || pixels.height <= 0) {
    throw new Error('invalid canvas artboard configuration');
  }
  return { x: anchor.center_x - pixels.width / 2, y: anchor.center_y - pixels.height / 2, width: pixels.width, height: pixels.height };
}

/**
 * The frame a canvas saved before coordinate version 2 showed for this ratio: legacy_max_edge world units
 * on the long edge, centred on the anchor. Read only to migrate such a canvas.
 */
export function legacyArtboard(anchor: Pick<ArtboardSchema, 'center_x' | 'center_y' | 'legacy_max_edge'>, pixels: CanvasPixels): CanvasArtboard {
  if (!Number.isFinite(anchor.legacy_max_edge) || anchor.legacy_max_edge <= 0) throw new Error('invalid canvas artboard configuration');
  const longest = Math.max(pixels.width, pixels.height);
  return canvasArtboard(anchor, {
    width: anchor.legacy_max_edge * (pixels.width / longest),
    height: anchor.legacy_max_edge * (pixels.height / longest),
  });
}

/** Scales every coordinate of an absolute M/L path (the pencil's own format); any other path is kept. */
function scalePathData(path: string, scale: number): string {
  if (!/^[\sMLml\d.,eE+-]*$/.test(path)) return path;
  return path.replace(/-?\d*\.?\d+(?:[eE][+-]?\d+)?/g, (number) => String(Number(number) * scale));
}

/**
 * Coordinate migration of a legacy canvas (#246): the old frame (`from`, legacy units) becomes the pixel
 * frame (`to`) around the shared centre, and every node scales with it, so the composition keeps its
 * place in the frame. Stroke widths, font sizes and pencil paths scale too; image crops stay in image
 * pixels and rotations are unchanged.
 */
export function migrateLegacyNodes(
  nodes: readonly CanvasNode[],
  from: CanvasArtboard,
  to: CanvasArtboard,
  defaults: { strokeWidth: number; fontSize: number },
): CanvasNode[] {
  const scale = to.width / from.width;
  if (!Number.isFinite(scale) || scale <= 0) throw new Error('invalid canvas migration');
  const fromX = from.x + from.width / 2;
  const fromY = from.y + from.height / 2;
  const toX = to.x + to.width / 2;
  const toY = to.y + to.height / 2;
  return nodes.map((node) => {
    const migrated: CanvasNode = {
      ...node,
      x: toX + (node.x - fromX) * scale,
      y: toY + (node.y - fromY) * scale,
      width: node.width * scale,
      height: node.height * scale,
    };
    if (node.type === 'shape' || node.type === 'pencil') migrated.strokeWidth = (node.strokeWidth ?? defaults.strokeWidth) * scale;
    if (node.type === 'text') migrated.fontSize = (node.fontSize ?? defaults.fontSize) * scale;
    if (node.pathData) migrated.pathData = scalePathData(node.pathData, scale);
    return migrated;
  });
}
