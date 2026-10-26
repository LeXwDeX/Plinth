import { describe, expect, it } from 'vitest';

import { clampPan, clampZoom, stepIndex, zoomAt, ZOOM_MAXIMUM } from './lightbox';

describe('lightbox arithmetic', () => {
  it('steps through versions and wraps at both ends', () => {
    expect(stepIndex(0, 1, 3)).toBe(1);
    expect(stepIndex(2, 1, 3)).toBe(0);
    expect(stepIndex(0, -1, 3)).toBe(2);
    expect(stepIndex(0, 1, 0)).toBe(0);
  });

  it('clamps zoom to the allowed range', () => {
    expect(clampZoom(0.2)).toBe(1);
    expect(clampZoom(100)).toBe(ZOOM_MAXIMUM);
    expect(clampZoom(Number.NaN)).toBe(1);
  });

  it('keeps the point under the pointer fixed while zooming', () => {
    const view = { zoom: 1, x: 0, y: 0 };
    const pointer = { x: 100, y: 50 };
    const next = zoomAt(view, 2, pointer);
    expect(next.zoom).toBe(2);
    // the picture point under the pointer: (pointer - offset) / zoom
    expect((pointer.x - next.x) / next.zoom).toBeCloseTo((pointer.x - view.x) / view.zoom, 6);
    expect((pointer.y - next.y) / next.zoom).toBeCloseTo((pointer.y - view.y) / view.zoom, 6);
  });

  it('returns to the centred fit when zoomed all the way out, and stops at the maximum', () => {
    expect(zoomAt({ zoom: 2, x: 40, y: 30 }, 0.1, { x: 5, y: 5 })).toEqual({ zoom: 1, x: 0, y: 0 });
    const max = { zoom: ZOOM_MAXIMUM, x: 3, y: 4 };
    expect(zoomAt(max, 2, { x: 1, y: 1 })).toBe(max);
  });

  it('does not let the picture be dragged out of reach', () => {
    const box = { width: 800, height: 600 };
    expect(clampPan({ zoom: 2, x: 5000, y: -5000 }, box)).toEqual({ zoom: 2, x: 800, y: -600 });
    expect(clampPan({ zoom: 1, x: 10, y: 10 }, box)).toEqual({ zoom: 1, x: 10, y: 10 });
  });
});
