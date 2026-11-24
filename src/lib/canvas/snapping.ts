/**
 * Drag snapping for the 2D canvas. Pure geometry: the caller supplies the
 * threshold already converted to board units (screen pixels / zoom).
 *
 * Rects carrying a rotation are skipped here: their stored x/y/width/height is
 * not what is drawn. Callers pass axis-aligned bounds instead; the board does
 * that through `multi-drag.ts`, which turns every node into the box it covers
 * on screen (a rotated node included) and every group into one box (#273).
 */

export interface SnapRect {
  x: number;
  y: number;
  width: number;
  height: number;
  rotation?: number;
}

export interface SnapGuide {
  /** 'vertical' is a line of constant x; 'horizontal' a line of constant y. */
  orientation: 'vertical' | 'horizontal';
  /** Board coordinate of the line. */
  position: number;
  /** Extent along the line, in board coordinates. */
  from: number;
  to: number;
}

export interface SnapResult {
  x: number;
  y: number;
  guides: SnapGuide[];
}

const EPSILON = 1e-6;

export function isSnappable(rect: SnapRect): boolean {
  return (rect.rotation ?? 0) % 360 === 0;
}

function lines(start: number, size: number): number[] {
  return [start, start + size / 2, start + size];
}

interface AxisMatch {
  delta: number;
}

function bestDelta(moving: number[], targets: number[], threshold: number): AxisMatch | null {
  if (targets.length === 0) return null;
  let best: AxisMatch | null = null;
  for (const m of moving) {
    for (const t of targets) {
      const delta = t - m;
      if (Math.abs(delta) <= threshold && (!best || Math.abs(delta) < Math.abs(best.delta))) {
        best = { delta };
      }
    }
  }
  return best;
}

/**
 * Snaps `moving` to the nearest left/center/right (top/middle/bottom) line of
 * `targets` within `threshold`. `secondary` targets are consulted per axis only
 * when no primary target is within reach, so they never override a primary match.
 */
export function snapRect(
  moving: SnapRect,
  targets: SnapRect[],
  threshold: number,
  secondary: SnapRect[] = [],
): SnapResult {
  if (!isSnappable(moving) || !(threshold > 0)) {
    return { x: moving.x, y: moving.y, guides: [] };
  }
  const primary = targets.filter(isSnappable);
  const fallback = secondary.filter(isSnappable);
  const movingX = lines(moving.x, moving.width);
  const movingY = lines(moving.y, moving.height);
  const axis = (pick: (t: SnapRect) => number[], movingLines: number[]) => {
    const match = bestDelta(movingLines, primary.flatMap(pick), threshold);
    if (match) return { match, from: primary };
    const second = bestDelta(movingLines, fallback.flatMap(pick), threshold);
    return second ? { match: second, from: fallback } : null;
  };
  const xSnap = axis((t) => lines(t.x, t.width), movingX);
  const ySnap = axis((t) => lines(t.y, t.height), movingY);
  const x = moving.x + (xSnap?.match.delta ?? 0);
  const y = moving.y + (ySnap?.match.delta ?? 0);

  const guides: SnapGuide[] = [];
  const snapped = { ...moving, x, y };
  const seen = new Set<string>();
  const add = (guide: SnapGuide) => {
    const key = `${guide.orientation}:${guide.position.toFixed(4)}`;
    if (seen.has(key)) return;
    seen.add(key);
    guides.push(guide);
  };
  if (xSnap) {
    for (const t of xSnap.from) {
      for (const line of lines(t.x, t.width)) {
        if (lines(x, snapped.width).some((m) => Math.abs(m - line) <= EPSILON)) {
          add({
            orientation: 'vertical',
            position: line,
            from: Math.min(t.y, t.y + t.height, y, y + snapped.height),
            to: Math.max(t.y, t.y + t.height, y, y + snapped.height),
          });
        }
      }
    }
  }
  if (ySnap) {
    for (const t of ySnap.from) {
      for (const line of lines(t.y, t.height)) {
        if (lines(y, snapped.height).some((m) => Math.abs(m - line) <= EPSILON)) {
          add({
            orientation: 'horizontal',
            position: line,
            from: Math.min(t.x, t.x + t.width, x, x + snapped.width),
            to: Math.max(t.x, t.x + t.width, x, x + snapped.width),
          });
        }
      }
    }
  }
  return { x, y, guides };
}

/** The key held to turn snapping off for one move or resize (Schema `canvas.frontend.snap.bypass_modifier`). */
export type SnapBypassModifier = 'mod' | 'alt' | 'shift';

/** Whether the pointer event holds the bypass key; 'mod' is Ctrl, or Command on macOS. */
export function snapBypassed(
  event: { ctrlKey?: boolean; metaKey?: boolean; altKey?: boolean; shiftKey?: boolean } | null | undefined,
  modifier: SnapBypassModifier,
): boolean {
  if (!event) return false;
  if (modifier === 'mod') return Boolean(event.ctrlKey || event.metaKey);
  if (modifier === 'alt') return Boolean(event.altKey);
  return Boolean(event.shiftKey);
}

/** A corner handle of the Transformer (the board offers no side handles). */
export type ResizeAnchor = 'top-left' | 'top-right' | 'bottom-left' | 'bottom-right';

export function isResizeAnchor(value: unknown): value is ResizeAnchor {
  return value === 'top-left' || value === 'top-right' || value === 'bottom-left' || value === 'bottom-right';
}

export interface ResizeSnapOptions {
  threshold: number;
  /** Proportional resize: only one axis snaps, and the other follows the box's ratio. */
  keepRatio: boolean;
  /** Consulted per axis only when no primary target is within reach (as for a move). */
  secondary?: SnapRect[];
}

export interface ResizeSnapResult {
  box: SnapRect;
  guides: SnapGuide[];
}

interface LineMatch {
  line: number;
  delta: number;
  from: SnapRect[];
}

function nearestLine(moving: number, primary: SnapRect[], fallback: SnapRect[], pick: (t: SnapRect) => number[], threshold: number): LineMatch | null {
  for (const from of [primary, fallback]) {
    let best: LineMatch | null = null;
    for (const line of from.flatMap(pick)) {
      const delta = line - moving;
      if (Math.abs(delta) <= threshold && (!best || Math.abs(delta) < Math.abs(best.delta))) best = { line, delta, from };
    }
    if (best) return best;
  }
  return null;
}

/**
 * Snapping while a corner handle resizes `box` (#546). The dragged corner's two edges move; the opposite corner stays.
 * A moving edge within `threshold` of another box's left/centre/right (top/middle/bottom) line lands on it, with the
 * same guides as a move. In a proportional resize only the axis closer to a line snaps and the other edge follows the
 * box's ratio, so the ratio is kept. Rotated or empty boxes are left as they are.
 */
export function snapResize(box: SnapRect, anchor: ResizeAnchor, targets: SnapRect[], options: ResizeSnapOptions): ResizeSnapResult {
  if (!isSnappable(box) || !(options.threshold > 0) || !(box.width > 0) || !(box.height > 0)) return { box, guides: [] };
  const primary = targets.filter(isSnappable);
  const fallback = (options.secondary ?? []).filter(isSnappable);
  const left = anchor.endsWith('left');
  const top = anchor.startsWith('top');
  const fixedX = left ? box.x + box.width : box.x;
  const fixedY = top ? box.y + box.height : box.y;
  const movingX = left ? box.x : box.x + box.width;
  const movingY = top ? box.y : box.y + box.height;
  // A line on the far side of the fixed edge would turn the box inside out: not a candidate.
  const keepSide = (match: LineMatch | null, fixed: number, before: boolean) =>
    match && (before ? match.line < fixed : match.line > fixed) ? match : null;
  let xMatch = keepSide(nearestLine(movingX, primary, fallback, (t) => lines(t.x, t.width), options.threshold), fixedX, left);
  let yMatch = keepSide(nearestLine(movingY, primary, fallback, (t) => lines(t.y, t.height), options.threshold), fixedY, top);
  if (options.keepRatio && xMatch && yMatch) {
    if (Math.abs(xMatch.delta) <= Math.abs(yMatch.delta)) yMatch = null;
    else xMatch = null;
  }
  let width = box.width;
  let height = box.height;
  if (xMatch) width = Math.abs(xMatch.line - fixedX);
  if (yMatch) height = Math.abs(yMatch.line - fixedY);
  if (options.keepRatio) {
    const ratio = box.width / box.height;
    if (xMatch) height = width / ratio;
    else if (yMatch) width = height * ratio;
  }
  const snapped: SnapRect = {
    ...box,
    x: left ? fixedX - width : fixedX,
    y: top ? fixedY - height : fixedY,
    width,
    height,
  };
  const guides: SnapGuide[] = [];
  if (xMatch) {
    const along = xMatch.from.filter((t) => lines(t.x, t.width).some((line) => Math.abs(line - xMatch!.line) <= EPSILON));
    guides.push({
      orientation: 'vertical',
      position: xMatch.line,
      from: Math.min(snapped.y, ...along.map((t) => t.y)),
      to: Math.max(snapped.y + snapped.height, ...along.map((t) => t.y + t.height)),
    });
  }
  if (yMatch) {
    const along = yMatch.from.filter((t) => lines(t.y, t.height).some((line) => Math.abs(line - yMatch!.line) <= EPSILON));
    guides.push({
      orientation: 'horizontal',
      position: yMatch.line,
      from: Math.min(snapped.x, ...along.map((t) => t.x)),
      to: Math.max(snapped.x + snapped.width, ...along.map((t) => t.x + t.width)),
    });
  }
  return { box: snapped, guides };
}
