import { describe, expect, it } from 'vitest';
import { dropPointToWorld } from './drop-placement';
import { editableAt, rectangleRegion, artboardToPixel, artboardToWorld, inpaintGeometry, paintMask, remapStrokesToFrame, strokeToPixels, strokesTouchFrame, worldToArtboard } from './inpaint-export';

const artboard = { x: -512, y: -256, width: 1024, height: 512 };

describe('inpaint geometry', () => {
  it('gives the picture and the mask the same pixel size: the chosen output size', () => {
    const geometry = inpaintGeometry(artboard, { width: 1536, height: 768 });
    expect(geometry.size).toEqual({ width: 1536, height: 768 });
    expect(geometry.frame).toBe(artboard);
    expect(geometry.scaleX).toBe(1.5);
    expect(geometry.scaleY).toBe(1.5);
  });

  it('falls back to the artboard size when no output size is chosen', () => {
    expect(inpaintGeometry(artboard, null).size).toEqual({ width: 1024, height: 512 });
  });

  it('puts a stroke at an artboard position on the matching pixel', () => {
    const geometry = inpaintGeometry(artboard, { width: 1536, height: 768 });
    // The world point (0, 0) is the artboard centre, which is the output centre in both rasters.
    const local = worldToArtboard(artboard, { x: 0, y: 0 });
    expect(local).toEqual({ x: 512, y: 256 });
    expect(artboardToPixel(geometry, local)).toEqual({ x: 768, y: 384 });
    expect(artboardToWorld(artboard, local)).toEqual({ x: 0, y: 0 });
    // The export draws the frame with ctx.scale(size/frame) after translating by -frame: same mapping.
    const worldPoint = { x: -512 + 100, y: -256 + 40 };
    const exported = { x: (worldPoint.x - artboard.x) * geometry.scaleX, y: (worldPoint.y - artboard.y) * geometry.scaleY };
    expect(artboardToPixel(geometry, worldToArtboard(artboard, worldPoint))).toEqual(exported);
  });

  it('scales each stroke by its own brush size', () => {
    const geometry = inpaintGeometry(artboard, { width: 2048, height: 1024 });
    expect(strokeToPixels(geometry, { points: [10, 20, 30, 40], size: 12 })).toEqual({ points: [20, 40, 60, 80], lineWidth: 24 });
    expect(strokeToPixels(geometry, { points: [0, 0], size: 40 }).lineWidth).toBe(80);
  });
});

describe('paintMask', () => {
  it('fills the full output size and erases each stroke with its own width', () => {
    const calls: string[] = [];
    const widths: number[] = [];
    const context = {
      fillStyle: '', strokeStyle: '', lineWidth: 0, lineCap: '', lineJoin: '', globalCompositeOperation: '',
      fillRect: (...args: number[]) => calls.push(`fill:${args.join(',')}`),
      beginPath: () => calls.push('begin'),
      moveTo: (x: number, y: number) => calls.push(`move:${x},${y}`),
      lineTo: () => undefined,
      closePath: () => undefined,
      fill: () => undefined,
      stroke() { widths.push(context.lineWidth); calls.push(`op:${context.globalCompositeOperation}`); },
      save: () => undefined,
      restore: () => undefined,
    };
    const geometry = inpaintGeometry(artboard, { width: 1536, height: 768 });
    paintMask(context, geometry, [{ points: [100, 100, 200, 200], size: 10 }, { points: [300, 300], size: 40 }], '#000');
    expect(calls[0]).toBe('fill:0,0,1536,768');
    expect(calls).toContain('move:150,150');
    expect(calls).toContain('move:450,450');
    expect(widths).toEqual([15, 60]);
    expect(calls.filter((c) => c.startsWith('op:')).every((c) => c === 'op:destination-out')).toBe(true);
  });
});

describe('screen to mask pixel under pan, zoom and a non-1:1 output size', () => {
  it('maps the same artboard spot to the same pixel whatever the view', () => {
    const board = { x: 100, y: 50, width: 800, height: 400 };
    const geometry = inpaintGeometry(board, { width: 2000, height: 1000 }); // 2.5x
    // Artboard spot (200, 100): world (300, 150).
    for (const view of [{ zoom: 1, panX: 0, panY: 0 }, { zoom: 2, panX: -120, panY: 75 }, { zoom: 0.5, panX: 300, panY: 40 }]) {
      const client = { x: 10 + 300 * view.zoom + view.panX, y: 20 + 150 * view.zoom + view.panY };
      const world = dropPointToWorld(client, { left: 10, top: 20 }, view);
      expect(artboardToPixel(geometry, worldToArtboard(board, world))).toEqual({ x: 500, y: 250 });
    }
  });

  it('keeps independent horizontal and vertical scales', () => {
    const geometry = inpaintGeometry({ x: 0, y: 0, width: 100, height: 100 }, { width: 200, height: 400 });
    expect(artboardToPixel(geometry, { x: 10, y: 10 })).toEqual({ x: 20, y: 40 });
  });
});

describe('mask strokes follow the artboard when it changes shape', () => {
  const before = { x: 344, y: 144, width: 512, height: 512 };
  const after = { x: 344, y: 229.5, width: 512, height: 341 };

  it('keeps each stroke over the same part of the board', () => {
    const [moved] = remapStrokesToFrame([{ points: [100, 200, 150, 260], size: 20 }], before, after);
    // The stroke started at board (444, 344); against the new top-left (344, 229.5) that is (100, 114.5).
    expect(moved.points).toEqual([100, 114.5, 150, 174.5]);
    expect(moved.size).toBe(20);
    expect(artboardToWorld(after, { x: moved.points[0], y: moved.points[1] })).toEqual(artboardToWorld(before, { x: 100, y: 200 }));
  });

  it('does not touch strokes when the artboard stays put, and copies instead of aliasing', () => {
    const original = [{ points: [1, 2], size: 5 }];
    const copy = remapStrokesToFrame(original, before, { ...before });
    expect(copy).toEqual(original);
    expect(copy[0]).not.toBe(original[0]);
  });

  it('tells a stroke that left the artboard from one that still touches it', () => {
    const frame = { width: 512, height: 341 };
    expect(strokesTouchFrame([{ points: [100, 400, 120, 420], size: 20 }], frame)).toBe(false);
    expect(strokesTouchFrame([{ points: [100, 330, 120, 420], size: 20 }], frame)).toBe(true);
    // The brush radius counts: a centre just outside still paints inside.
    expect(strokesTouchFrame([{ points: [100, 350], size: 40 }], frame)).toBe(true);
    expect(strokesTouchFrame([], frame)).toBe(false);
  });
});

describe('mask pixels line up with the delivered picture', () => {
  it('a stroke between two pictures lands between them in picture and mask alike, at any scale and offset', () => {
    // Two 100-wide pictures at board x 0..100 and 200..300, the gap in between; artboard offset and a non 1:1 scale.
    const artboard = { x: -20, y: 10, width: 400, height: 200 };
    const geometry = inpaintGeometry(artboard, { width: 1200, height: 500 });
    expect(geometry.scaleX).toBe(3);
    expect(geometry.scaleY).toBe(2.5);
    const stroke = { points: [160, 60, 160, 100], size: 10 }; // artboard coordinates: board x 140, y 70..110
    const mask = strokeToPixels(geometry, worldStroke(artboard, { x: 140, y: 70 }, { x: 140, y: 110 }, 10));
    expect(mask.points).toEqual(strokeToPixels(geometry, stroke).points);
    // The picture raster puts board x 100..200 (the gap) at pixels (100 + 20) * 3 .. (200 + 20) * 3.
    const gapLeft = (100 - artboard.x) * geometry.scaleX;
    const gapRight = (200 - artboard.x) * geometry.scaleX;
    expect(mask.points[0]).toBeGreaterThan(gapLeft);
    expect(mask.points[0]).toBeLessThan(gapRight);
    expect(mask.points[0]).toBeCloseTo((140 - artboard.x) * geometry.scaleX, 6);
  });
});

function worldStroke(artboard: { x: number; y: number }, from: { x: number; y: number }, to: { x: number; y: number }, size: number) {
  const a = worldToArtboard({ ...artboard, width: 0, height: 0 }, from);
  const b = worldToArtboard({ ...artboard, width: 0, height: 0 }, to);
  return { points: [a.x, a.y, b.x, b.y], size };
}

describe('paintMask with regions, erase and invert', () => {
  function recorder() {
    const calls: string[] = [];
    const context = {
      fillStyle: '', strokeStyle: '', lineWidth: 0, lineCap: '', lineJoin: '', globalCompositeOperation: '',
      fillRect: (...args: number[]) => calls.push(`fillRect:${context.globalCompositeOperation || 'none'}:${args.join(',')}`),
      beginPath: () => calls.push('begin'),
      moveTo: (x: number, y: number) => calls.push(`move:${x},${y}`),
      lineTo: (x: number, y: number) => calls.push(`line:${x},${y}`),
      closePath: () => calls.push('close'),
      stroke: () => calls.push(`stroke:${context.globalCompositeOperation}`),
      fill: () => calls.push(`fill:${context.globalCompositeOperation}`),
      save: () => undefined,
      restore: () => undefined,
    };
    return { calls, context };
  }
  const geometry = inpaintGeometry({ x: 0, y: 0, width: 100, height: 100 }, { width: 200, height: 200 });

  it('a rectangle region is filled (not stroked) at output pixels, clearing the mask under it', () => {
    const { calls, context } = recorder();
    paintMask(context, geometry, [rectangleRegion({ x: 10, y: 10 }, { x: 40, y: 30 })], '#000');
    expect(calls).toEqual(['fillRect:none:0,0,200,200', 'begin', 'move:20,20', 'line:80,20', 'line:80,60', 'line:20,60', 'close', 'fill:destination-out']);
  });

  it('a subtracted region and an erase stroke paint the keep colour back over the mask', () => {
    const { calls, context } = recorder();
    paintMask(context, geometry, [rectangleRegion({ x: 0, y: 0 }, { x: 50, y: 50 }, true), { points: [10, 10, 20, 20], size: 6, erase: true }], '#000');
    expect(calls).toContain('fill:source-over');
    expect(calls).toContain('stroke:source-over');
    expect(calls.some((call) => call.includes('destination-out'))).toBe(false);
  });

  it('invert XORs an opaque layer over the whole mask', () => {
    const { calls, context } = recorder();
    paintMask(context, geometry, [{ points: [], size: 0, invert: true }], '#000');
    expect(calls).toEqual(['fillRect:none:0,0,200,200', 'fillRect:xor:0,0,200,200']);
  });

  it('regions count as touching the frame by their bounding box, and invert always does', () => {
    const frame = { width: 100, height: 100 };
    expect(strokesTouchFrame([rectangleRegion({ x: -50, y: -50 }, { x: 150, y: 150 })], frame)).toBe(true);
    expect(strokesTouchFrame([rectangleRegion({ x: 200, y: 200 }, { x: 250, y: 250 })], frame)).toBe(false);
    expect(strokesTouchFrame([{ points: [], size: 0, invert: true }], frame)).toBe(true);
  });

  it('editableAt applies the steps in order for brush, region and erase', () => {
    const region = rectangleRegion({ x: 0, y: 0 }, { x: 100, y: 100 });
    const brushOut = { points: [50, 50, 60, 50], size: 20, erase: true };
    expect(editableAt([region, brushOut], { x: 55, y: 50 })).toBe(false);
    expect(editableAt([region, brushOut], { x: 10, y: 10 })).toBe(true);
    expect(editableAt([{ points: [30, 30], size: 10 }], { x: 33, y: 30 })).toBe(true);
    expect(editableAt([{ points: [30, 30], size: 10 }], { x: 40, y: 30 })).toBe(false);
  });
});
