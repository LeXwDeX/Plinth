import { describe, expect, it } from 'vitest';
import { Group } from 'konva/lib/Group';
import { canvasPointerToWorld, nodeContainsCanvasPoint, shapeGeometryFromDrag } from './drawing-geometry';
import type { CanvasNode } from '@/store/canvas-store';

describe('canvas pointer coordinates', () => {
  it('keeps the drawing point under the pointer after Konva pans before React rerenders', () => {
    const stage = new Group({ x: 20, y: 40, scaleX: 1.5, scaleY: 1.5 });
    stage.position({ x: 170, y: -35 });
    const pointer = { x: 470, y: 115 };
    const world = canvasPointerToWorld(stage, pointer);
    expect(world.x).toBeCloseTo(200);
    expect(world.y).toBeCloseTo(100);
    const drawn = stage.getAbsoluteTransform().point(world);
    expect(drawn.x).toBeCloseTo(pointer.x);
    expect(drawn.y).toBeCloseTo(pointer.y);
    // The old stored pan would have placed the same drawing at (620, 40).
    const stale = { x: (pointer.x - 20) / 1.5, y: (pointer.y - 40) / 1.5 };
    expect(stage.getAbsoluteTransform().point(stale)).not.toEqual(pointer);
  });

  it.each([0.25, 1, 3])('round trips pan and zoom at scale %s', (zoom) => {
    const stage = new Group({ x: -137, y: 89, scaleX: zoom, scaleY: zoom });
    const pointer = { x: 318, y: 224 };
    const world = canvasPointerToWorld(stage, pointer);
    const drawn = stage.getAbsoluteTransform().point(world);
    expect(drawn.x).toBeCloseTo(pointer.x);
    expect(drawn.y).toBeCloseTo(pointer.y);
  });
});

describe('directed shapes', () => {
  it.each([
    { x: 250, y: 180 }, { x: 50, y: 180 },
    { x: 50, y: 20 }, { x: 250, y: 20 },
    { x: 250, y: 100 }, { x: 50, y: 100 },
    { x: 150, y: 180 }, { x: 150, y: 20 },
  ])('preserves line and arrow endpoints for a drag to %j', (end) => {
    const start = { x: 150, y: 100 };
    for (const shape of ['line', 'arrow'] as const) {
      const geometry = shapeGeometryFromDrag(shape, start, end);
      const node = new Group(geometry);
      expect(node.getAbsoluteTransform().point({ x: 0, y: 0 })).toEqual(start);
      const tip = node.getAbsoluteTransform().point({ x: geometry.width, y: geometry.height });
      expect(tip.x).toBeCloseTo(end.x);
      expect(tip.y).toBeCloseTo(end.y);
      expect(geometry.width).toBeGreaterThan(0);
      expect(geometry.height).toBeGreaterThan(0);
    }
  });

  it('keeps rectangle and ellipse bounds when dragged backwards', () => {
    for (const shape of ['rectangle', 'ellipse'] as const) {
      expect(shapeGeometryFromDrag(shape, { x: 250, y: 180 }, { x: 50, y: 20 }))
        .toEqual({ x: 50, y: 20, width: 200, height: 160, rotation: 0 });
    }
  });
});

describe('rotated image hit testing', () => {
  const image = { x: 100, y: 100, width: 100, height: 100, rotation: 45 } as CanvasNode;
  it('rejects the empty corner inside a rotated bounding box', () => {
    expect(nodeContainsCanvasPoint(image, { x: 160, y: 105 })).toBe(false);
  });
  it('accepts the visible rotated image', () => {
    expect(nodeContainsCanvasPoint(image, { x: 100, y: 170 })).toBe(true);
  });
});
