/**
 * #310: the mask on the board is ONE layer. Every step of the selection — finished brush strokes, marquee /
 * lasso regions, erasing, inversion and the stroke being drawn — is painted at full strength into one
 * scratch raster with the same `paintMask` that builds the submitted mask, and that raster is laid over the
 * board once with the mask's own Schema tint (`canvas.frontend.mask.*`: the pre-#310 single-stroke look, kept
 * apart from the lighter `selection.tint_*` so the mask stays clearly readable). Overlapping or repeated
 * strokes therefore only widen the editable area; they never darken it or stack into visible layers.
 *
 * The scratch raster has the board canvas's own device-pixel size and transform, so the tint stays sharp
 * at any zoom (unlike the small `renderSelectionPreview` thumbnail used by the layers panel).
 */
import type { MaskStroke } from '@/store/mask-store';
import type { ArtboardRect, InpaintGeometry } from './inpaint-export';
import { paintSelectionTint } from './selection-preview';

export interface MaskOverlayStyle {
  tintColor: string;
  tintOpacity: number;
}

interface TransformMatrix { a: number; b: number; c: number; d: number; e: number; f: number }

/** The board's 2D context (Konva hands its native context to a custom shape). */
export interface MaskOverlayTarget {
  readonly canvas: { width: number; height: number };
  globalAlpha: number;
  globalCompositeOperation: string;
  getTransform(): TransformMatrix;
  setTransform(a: number, b: number, c: number, d: number, e: number, f: number): void;
  save(): void;
  restore(): void;
  drawImage(image: never, dx: number, dy: number): void;
}

/** A reusable off-screen canvas the overlay is composed on. */
export interface MaskOverlayScratch {
  width: number;
  height: number;
  getContext(kind: '2d'): unknown;
}

type ScratchContext = Parameters<typeof paintSelectionTint>[0] & {
  setTransform(a: number, b: number, c: number, d: number, e: number, f: number): void;
  translate(x: number, y: number): void;
  clearRect(x: number, y: number, width: number, height: number): void;
};

/** The selection as drawn on the board: the finished steps, then the stroke under the pointer. */
export function maskOverlaySteps(strokes: readonly MaskStroke[] | undefined, current: MaskStroke | null | undefined): MaskStroke[] {
  const finished = strokes ? [...strokes] : [];
  return current ? [...finished, current] : finished;
}

/**
 * Which Schema tint the merged area carries: a pure marquee / lasso / inversion selection keeps the
 * selection tint (`selection.tint_*`); once the brush has painted or erased (mixed steps included) the whole
 * area is the quick mask and carries the mask tint (`mask.*`), still laid over it only once.
 */
export function overlayTintKind(steps: readonly MaskStroke[]): 'mask' | 'selection' {
  return steps.some((step) => !step.region && !step.invert) ? 'mask' : 'selection';
}

/**
 * Draws the tinted editable area onto `target` (whose transform maps canvas units to device pixels) in a
 * single composite. Returns false when there is nothing to draw or no scratch context.
 */
export function drawMaskOverlay(
  target: MaskOverlayTarget,
  scratch: MaskOverlayScratch,
  artboard: ArtboardRect,
  steps: readonly MaskStroke[],
  style: MaskOverlayStyle,
): boolean {
  if (steps.length === 0 || artboard.width <= 0 || artboard.height <= 0) return false;
  const { width, height } = target.canvas;
  if (width <= 0 || height <= 0) return false;
  // Resizing a canvas clears it; an unchanged size is cleared below.
  if (scratch.width !== width) scratch.width = width;
  if (scratch.height !== height) scratch.height = height;
  const context = scratch.getContext('2d') as ScratchContext | null;
  if (!context) return false;
  context.setTransform(1, 0, 0, 1, 0, 0);
  context.globalCompositeOperation = 'source-over';
  context.globalAlpha = 1;
  context.clearRect(0, 0, width, height);
  const matrix = target.getTransform();
  context.setTransform(matrix.a, matrix.b, matrix.c, matrix.d, matrix.e, matrix.f);
  context.translate(artboard.x, artboard.y);
  // Artboard units, unscaled: the device transform above already maps them to screen pixels.
  const geometry: InpaintGeometry = { frame: artboard, size: { width: artboard.width, height: artboard.height }, scaleX: 1, scaleY: 1 };
  paintSelectionTint(context, geometry, steps, style.tintColor, 1);
  context.setTransform(1, 0, 0, 1, 0, 0);

  target.save();
  target.setTransform(1, 0, 0, 1, 0, 0);
  target.globalCompositeOperation = 'source-over';
  target.globalAlpha *= style.tintOpacity;
  target.drawImage(scratch as never, 0, 0);
  target.restore();
  return true;
}

/**
 * The brush cursor's radius in canvas units: half the brush size, so on screen its diameter is exactly the
 * width a stroke paints at the current zoom. It never shrinks below one screen pixel so it stays visible.
 */
export function brushCursorRadius(size: number, zoom: number): number {
  return Math.max(size / 2, 1 / zoom);
}
