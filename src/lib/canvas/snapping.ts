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
