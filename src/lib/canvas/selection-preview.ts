/**
 * How the canvas-level selection looks on the board: the editable area tinted (a raster of the very
 * mask that is submitted, drawn small) and the outlines of the marquee / lasso regions as marching ants.
 */
import type { MaskStroke } from '@/store/mask-store';
import { inpaintGeometry, paintMask, type ArtboardRect, type InpaintGeometry, type MaskContext, type PixelSize } from './inpaint-export';

/** The preview raster's pixel size: the artboard's ratio with its long edge at most `maxEdge`. */
export function previewSize(artboard: ArtboardRect, maxEdge: number): PixelSize {
  const scale = Math.min(1, maxEdge / Math.max(artboard.width, artboard.height));
  return { width: Math.max(1, Math.round(artboard.width * scale)), height: Math.max(1, Math.round(artboard.height * scale)) };
}

/** Outlines worth drawing as ants: every added region that no later step took away or inverted. */
export function antsOutlines(strokes: readonly MaskStroke[]): number[][] {
  const lastInvert = strokes.map((stroke) => Boolean(stroke.invert)).lastIndexOf(true);
  return strokes
    .map((stroke, index) => ({ stroke, index }))
    .filter(({ stroke, index }) => stroke.region === true && stroke.erase !== true && index > lastInvert && stroke.points.length >= 6)
    .map(({ stroke }) => stroke.points);
}

export interface PreviewStyle {
  tintColor: string;
  tintOpacity: number;
  maxEdge: number;
}

/**
 * Paints the tint over the editable area of `geometry` and nothing elsewhere: the very `paintMask` that builds
 * the submitted mask lays the kept part down opaque, then "source-out" puts the tint only where the mask is
 * clear. Each step only adds to or removes from that one area, so overlapping strokes never stack.
 */
export function paintSelectionTint(
  context: MaskContext & { globalAlpha: number },
  geometry: InpaintGeometry,
  strokes: readonly MaskStroke[],
  tintColor: string,
  tintAlpha: number,
): void {
  paintMask(context, geometry, strokes, '#000000');
  context.globalCompositeOperation = 'source-out';
  context.globalAlpha = tintAlpha;
  context.fillStyle = tintColor;
  context.fillRect(0, 0, geometry.size.width, geometry.size.height);
  context.globalCompositeOperation = 'source-over';
  context.globalAlpha = 1;
}

/** A canvas (artboard-sized ratio) holding the tint over the editable area and nothing elsewhere. */
export function renderSelectionPreview(strokes: readonly MaskStroke[], artboard: ArtboardRect, style: PreviewStyle): HTMLCanvasElement | null {
  if (strokes.length === 0 || typeof document === 'undefined') return null;
  const size = previewSize(artboard, style.maxEdge);
  const canvas = document.createElement('canvas');
  canvas.width = size.width;
  canvas.height = size.height;
  const context = canvas.getContext('2d');
  if (!context) return null;
  paintSelectionTint(context, inpaintGeometry(artboard, size), strokes, style.tintColor, style.tintOpacity);
  return canvas;
}
