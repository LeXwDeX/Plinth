import { describe, expect, it } from 'vitest';

import { outsideDimRects, visibleWorld } from './outside-dim';

const area = (rects: Array<{ width: number; height: number }>) => rects.reduce((sum, rect) => sum + rect.width * rect.height, 0);

describe('outside dim', () => {
  it('covers exactly the visible area minus the frame', () => {
    const visible = { x: 0, y: 0, width: 1000, height: 800 };
    const frame = { x: 200, y: 100, width: 300, height: 400 };
    const rects = outsideDimRects(visible, frame);
    expect(area(rects)).toBe(1000 * 800 - 300 * 400);
    for (const rect of rects) {
      const overlapX = Math.min(rect.x + rect.width, frame.x + frame.width) - Math.max(rect.x, frame.x);
      const overlapY = Math.min(rect.y + rect.height, frame.y + frame.height) - Math.max(rect.y, frame.y);
      expect(overlapX > 0 && overlapY > 0).toBe(false);
    }
  });

  it('copes with a frame partly or wholly out of view', () => {
    const visible = { x: 0, y: 0, width: 100, height: 100 };
    expect(area(outsideDimRects(visible, { x: 50, y: 50, width: 200, height: 200 }))).toBe(10000 - 2500);
    expect(area(outsideDimRects(visible, { x: 500, y: 500, width: 10, height: 10 }))).toBe(10000);
    expect(outsideDimRects(visible, { x: -10, y: -10, width: 500, height: 500 })).toEqual([]);
  });

  it('turns the viewport into the visible world rectangle', () => {
    expect(visibleWorld({ zoom: 2, panX: -100, panY: 40 }, { width: 800, height: 600 })).toEqual({ x: 50, y: -20, width: 400, height: 300 });
  });
});
