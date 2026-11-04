/** X5: the veil outside the dashed frame, as up to four rectangles covering the visible world area minus the frame. */
export interface DimRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export function outsideDimRects(visible: DimRect, frame: DimRect): DimRect[] {
  // Only the part of the frame inside the visible area matters.
  const left = Math.max(frame.x, visible.x);
  const top = Math.max(frame.y, visible.y);
  const right = Math.min(frame.x + frame.width, visible.x + visible.width);
  const bottom = Math.min(frame.y + frame.height, visible.y + visible.height);
  if (right <= left || bottom <= top) return [{ ...visible }];
  const rects: DimRect[] = [
    // Above and below span the whole visible width; left and right fill the band between them.
    { x: visible.x, y: visible.y, width: visible.width, height: top - visible.y },
    { x: visible.x, y: bottom, width: visible.width, height: visible.y + visible.height - bottom },
    { x: visible.x, y: top, width: left - visible.x, height: bottom - top },
    { x: right, y: top, width: visible.x + visible.width - right, height: bottom - top },
  ];
  return rects.filter((rect) => rect.width > 0 && rect.height > 0);
}

/** The world rectangle the drawing area shows, grown by `margin` world units. */
export function visibleWorld(viewport: { zoom: number; panX: number; panY: number }, size: { width: number; height: number }, margin = 0): DimRect {
  return {
    x: -viewport.panX / viewport.zoom - margin,
    y: -viewport.panY / viewport.zoom - margin,
    width: size.width / viewport.zoom + margin * 2,
    height: size.height / viewport.zoom + margin * 2,
  };
}

/** A layer as it is painted on the board, in paint order, with its world bounding box. */
export interface PaintedNode {
  id: string;
  rect: DimRect;
}

/**
 * One step of the cut-out mask: a selected layer adds its pixels (`reveal`), a layer painted over it takes
 * its own pixels away again, so only what the selected layer actually shows on the board is undimmed.
 */
export interface RevealStep {
  id: string;
  reveal: boolean;
}

const hasArea = (rect: DimRect) => rect.width > 0 && rect.height > 0;

export function rectInside(inner: DimRect, outer: DimRect): boolean {
  return inner.x >= outer.x && inner.y >= outer.y
    && inner.x + inner.width <= outer.x + outer.width
    && inner.y + inner.height <= outer.y + outer.height;
}

export function rectsOverlap(a: DimRect, b: DimRect): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

/**
 * #91: the layers that shape the hole cut into the veil, in paint order. Only a selected layer that reaches
 * outside the frame reveals anything (inside the frame there is no veil); a later, unselected layer that
 * overlaps one of them is painted over it, so it takes its own pixels back out and stays dimmed. Nothing
 * selected outside the frame gives no steps, and the veil is drawn as before.
 */
export function revealSteps(painted: readonly PaintedNode[], selected: ReadonlySet<string>, frame: DimRect): RevealStep[] {
  const steps: RevealStep[] = [];
  const revealed: DimRect[] = [];
  for (const node of painted) {
    if (!hasArea(node.rect)) continue;
    if (selected.has(node.id)) {
      if (rectInside(node.rect, frame)) continue;
      revealed.push(node.rect);
      steps.push({ id: node.id, reveal: true });
    } else if (revealed.some((rect) => rectsOverlap(rect, node.rect))) {
      steps.push({ id: node.id, reveal: false });
    }
  }
  return steps;
}

export interface OutsideDimStyle {
  color: string;
  opacity: number;
}

/** The veil layer's 2D context (Konva hands its native context to a custom shape); world units → device pixels. */
export interface OutsideDimTarget {
  fillStyle: unknown;
  globalAlpha: number;
  globalCompositeOperation: string;
  save(): void;
  restore(): void;
  setTransform(a: number, b: number, c: number, d: number, e: number, f: number): void;
  fillRect(x: number, y: number, width: number, height: number): void;
  drawImage(image: never, dx: number, dy: number): void;
}

/**
 * Paints the cut-out mask at the veil canvas's own device-pixel size and transform: `begin` sizes and clears
 * it, `paint` draws one layer exactly as the board shows it (adding it, or for `reveal: false` taking it
 * away), `result` is the finished raster.
 */
export interface RevealPainter {
  begin(): boolean;
  paint(step: RevealStep): void;
  result(): unknown;
}

/**
 * Draws the veil (one fill over the non-overlapping rectangles, the same look as one rectangle each), then
 * removes it where the reveal steps say with a single `destination-out` composite. The veil must be the first
 * thing drawn in its layer so the composite only cuts the veil. Returns true when a hole was cut.
 */
export function drawOutsideDim(
  target: OutsideDimTarget,
  rects: readonly DimRect[],
  style: OutsideDimStyle,
  steps: readonly RevealStep[],
  painter: RevealPainter | null,
): boolean {
  if (rects.length === 0) return false;
  target.save();
  target.globalCompositeOperation = 'source-over';
  target.globalAlpha *= style.opacity;
  target.fillStyle = style.color;
  for (const rect of rects) target.fillRect(rect.x, rect.y, rect.width, rect.height);
  target.restore();
  if (!painter || !steps.some((step) => step.reveal) || !painter.begin()) return false;
  for (const step of steps) painter.paint(step);
  target.save();
  target.setTransform(1, 0, 0, 1, 0, 0);
  target.globalCompositeOperation = 'destination-out';
  target.globalAlpha = 1;
  target.drawImage(painter.result() as never, 0, 0);
  target.restore();
  return true;
}
