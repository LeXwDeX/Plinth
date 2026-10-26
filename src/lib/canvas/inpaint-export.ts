/**
 * The inpaint submission contract. What goes upstream is the content of the dashed artboard,
 * rasterised into ONE picture whose pixel size is the chosen output size; the mask is drawn at
 * exactly that size and origin. Both therefore share this geometry, so a stroke at an artboard
 * position lands on the same pixel in the picture and in the mask.
 */
import type { MaskStroke } from '@/store/mask-store';

export interface ArtboardRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PixelSize {
  width: number;
  height: number;
}

export interface InpaintGeometry {
  /** Artboard in world coordinates: the frame handed to exportCanvasToBlob. */
  frame: ArtboardRect;
  /** Output pixels of both the picture and the mask. */
  size: PixelSize;
  scaleX: number;
  scaleY: number;
}

export function inpaintGeometry(artboard: ArtboardRect, pixels: PixelSize | null | undefined): InpaintGeometry {
  const valid = pixels && Number.isInteger(pixels.width) && Number.isInteger(pixels.height) && pixels.width > 0 && pixels.height > 0;
  const size = valid ? { width: pixels.width, height: pixels.height } : {
    width: Math.max(1, Math.round(artboard.width)),
    height: Math.max(1, Math.round(artboard.height)),
  };
  return { frame: artboard, size, scaleX: size.width / artboard.width, scaleY: size.height / artboard.height };
}

/** Artboard-relative point → output pixel (not rounded; the canvas rasterises it). */
export function artboardToPixel(geometry: InpaintGeometry, point: { x: number; y: number }): { x: number; y: number } {
  return { x: point.x * geometry.scaleX, y: point.y * geometry.scaleY };
}

/** World point → artboard-relative point (what the mask store keeps). */
export function worldToArtboard(artboard: ArtboardRect | null, point: { x: number; y: number }): { x: number; y: number } {
  return artboard ? { x: point.x - artboard.x, y: point.y - artboard.y } : point;
}

/** Artboard-relative point → world point (for drawing the strokes on the board). */
export function artboardToWorld(artboard: ArtboardRect | null, point: { x: number; y: number }): { x: number; y: number } {
  return artboard ? { x: point.x + artboard.x, y: point.y + artboard.y } : point;
}

export interface PixelStroke {
  points: number[];
  lineWidth: number;
}

/** A stroke in output pixels, with its own brush size scaled with the picture. */
export function strokeToPixels(geometry: InpaintGeometry, stroke: MaskStroke): PixelStroke {
  const points: number[] = [];
  for (let index = 0; index < stroke.points.length; index += 2) {
    const pixel = artboardToPixel(geometry, { x: stroke.points[index], y: stroke.points[index + 1] });
    points.push(pixel.x, pixel.y);
  }
  return { points, lineWidth: stroke.size * ((geometry.scaleX + geometry.scaleY) / 2) };
}

export type MaskContext = Pick<CanvasRenderingContext2D, 'fillRect' | 'beginPath' | 'moveTo' | 'lineTo' | 'closePath' | 'stroke' | 'fill' | 'save' | 'restore'> & {
  fillStyle: unknown; strokeStyle: unknown; lineWidth: number; lineCap: string; lineJoin: string; globalCompositeOperation: string;
};

function tracePath(context: MaskContext, points: readonly number[], closed: boolean): void {
  context.beginPath();
  context.moveTo(points[0], points[1]);
  if (points.length === 2) context.lineTo(points[0], points[1]);
  for (let index = 2; index < points.length; index += 2) context.lineTo(points[index], points[index + 1]);
  if (closed) context.closePath();
}

/**
 * Draws the mask at the output size: opaque where the picture is kept, erased under the strokes
 * (the editable region). `context` is any 2D context of a canvas of `geometry.size`. The canvas-level
 * selection is one list of steps applied in order: a brush stroke or a marquee / lasso region adds to
 * the editable area, an erase stroke or a subtracted region gives the picture back, and an invert
 * step swaps the two.
 */
export function paintMask(
  context: MaskContext,
  geometry: InpaintGeometry,
  strokes: readonly MaskStroke[],
  keepColor: string,
): void {
  context.fillStyle = keepColor;
  context.fillRect(0, 0, geometry.size.width, geometry.size.height);
  context.save();
  context.lineCap = 'round';
  context.lineJoin = 'round';
  context.strokeStyle = keepColor;
  for (const stroke of strokes) {
    if (stroke.invert) {
      // An opaque layer XORed over the mask turns kept pixels clear and clear ones opaque.
      context.globalCompositeOperation = 'xor';
      context.fillStyle = keepColor;
      context.fillRect(0, 0, geometry.size.width, geometry.size.height);
      continue;
    }
    const pixels = strokeToPixels(geometry, stroke);
    if (pixels.points.length < 2) continue;
    // Painting clears the mask (editable); erasing paints the opaque keep colour back over it.
    context.globalCompositeOperation = stroke.erase ? 'source-over' : 'destination-out';
    if (stroke.region) {
      context.fillStyle = keepColor;
      tracePath(context, pixels.points, true);
      context.fill();
    } else {
      context.lineWidth = pixels.lineWidth;
      tracePath(context, pixels.points, false);
      context.stroke();
    }
  }
  context.restore();
}

/** Distance from a point to a segment. */
function distanceToSegment(px: number, py: number, x0: number, y0: number, x1: number, y1: number): number {
  const dx = x1 - x0;
  const dy = y1 - y0;
  const lengthSquared = dx * dx + dy * dy;
  const t = lengthSquared === 0 ? 0 : Math.max(0, Math.min(1, ((px - x0) * dx + (py - y0) * dy) / lengthSquared));
  return Math.hypot(px - (x0 + t * dx), py - (y0 + t * dy));
}

function insidePolygon(px: number, py: number, points: readonly number[]): boolean {
  let inside = false;
  const count = points.length / 2;
  for (let index = 0, previous = count - 1; index < count; previous = index++) {
    const xi = points[index * 2];
    const yi = points[index * 2 + 1];
    const xj = points[previous * 2];
    const yj = points[previous * 2 + 1];
    if ((yi > py) !== (yj > py) && px < ((xj - xi) * (py - yi)) / (yj - yi) + xi) inside = !inside;
  }
  return inside;
}

/**
 * Whether a point (artboard coordinates) is in the editable region after all the steps. This is the
 * reference meaning of `paintMask`, evaluated one point at a time.
 */
export function editableAt(strokes: readonly MaskStroke[], point: { x: number; y: number }): boolean {
  let editable = false;
  for (const stroke of strokes) {
    if (stroke.invert) {
      editable = !editable;
      continue;
    }
    let hit = false;
    if (stroke.region) {
      hit = stroke.points.length >= 6 && insidePolygon(point.x, point.y, stroke.points);
    } else if (stroke.points.length === 2) {
      hit = Math.hypot(point.x - stroke.points[0], point.y - stroke.points[1]) <= stroke.size / 2;
    } else {
      for (let index = 0; index + 3 < stroke.points.length && !hit; index += 2) {
        hit = distanceToSegment(point.x, point.y, stroke.points[index], stroke.points[index + 1], stroke.points[index + 2], stroke.points[index + 3]) <= stroke.size / 2;
      }
    }
    if (hit) editable = !stroke.erase;
  }
  return editable;
}

/** A rectangle as a region stroke (artboard coordinates). */
export function rectangleRegion(from: { x: number; y: number }, to: { x: number; y: number }, erase = false): MaskStroke {
  const left = Math.min(from.x, to.x);
  const right = Math.max(from.x, to.x);
  const top = Math.min(from.y, to.y);
  const bottom = Math.max(from.y, to.y);
  return { points: [left, top, right, top, right, bottom, left, bottom], size: 0, region: true, ...(erase ? { erase: true } : {}) };
}

/**
 * The same strokes after the artboard moved or changed shape: each point keeps its place on the board
 * (the picture under it is where the person painted), so artboard-relative coordinates shift by the
 * difference between the old and new top-left corners.
 */
export function remapStrokesToFrame(strokes: readonly MaskStroke[], from: ArtboardRect, to: ArtboardRect): MaskStroke[] {
  const dx = from.x - to.x;
  const dy = from.y - to.y;
  if (dx === 0 && dy === 0) return strokes.map((stroke) => ({ ...stroke, points: [...stroke.points] }));
  return strokes.map((stroke) => ({
    ...stroke,
    points: stroke.points.map((value, index) => value + (index % 2 === 0 ? dx : dy)),
  }));
}

/**
 * True when some stroke can still touch the artboard (strokes are in artboard coordinates, so the frame is
 * 0..width x 0..height). A segment counts when its bounding box, grown by half the brush, overlaps the frame.
 */
export function strokesTouchFrame(strokes: readonly MaskStroke[], frame: { width: number; height: number }): boolean {
  return strokes.some((stroke) => {
    if (stroke.invert) return true;
    if (stroke.region) {
      // A region counts by its bounding box (it may enclose the frame without an edge inside it).
      const xs = stroke.points.filter((_, index) => index % 2 === 0);
      const ys = stroke.points.filter((_, index) => index % 2 === 1);
      return xs.length > 0 && Math.max(...xs) >= 0 && Math.min(...xs) <= frame.width && Math.max(...ys) >= 0 && Math.min(...ys) <= frame.height;
    }
    const reach = stroke.size / 2;
    for (let index = 0; index + 1 < stroke.points.length; index += 2) {
      const x0 = stroke.points[index];
      const y0 = stroke.points[index + 1];
      const x1 = stroke.points[index + 2] ?? x0;
      const y1 = stroke.points[index + 3] ?? y0;
      if (Math.max(x0, x1) + reach >= 0 && Math.min(x0, x1) - reach <= frame.width
        && Math.max(y0, y1) + reach >= 0 && Math.min(y0, y1) - reach <= frame.height) return true;
    }
    return false;
  });
}
