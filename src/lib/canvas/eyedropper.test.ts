import { describe, expect, it } from 'vitest';

import { pixelHex, sampleColor, toLocalPoint, type SampleNode } from './eyedropper';

const picture = (over: Partial<SampleNode> = {}): SampleNode => ({ id: 'p', type: 'image', x: 0, y: 0, width: 100, height: 50, zIndex: 1, ...over });

describe('eyedropper', () => {
  it('formats opaque pixels and ignores transparent ones', () => {
    expect(pixelHex(255, 0, 16, 255)).toBe('#ff0010');
    expect(pixelHex(1, 2, 3, 0)).toBeNull();
  });

  it('maps a board point to the picture pixel, through the natural size', () => {
    const reads: Array<[number, number]> = [];
    const color = sampleColor([picture()], { x: 50, y: 25 }, (_id, x, y) => { reads.push([x, y]); return [10, 20, 30, 255]; }, () => ({ width: 200, height: 100 }));
    expect(color).toBe('#0a141e');
    expect(reads).toEqual([[100, 50]]);
  });

  it('uses the crop rectangle as the source area', () => {
    const reads: Array<[number, number]> = [];
    sampleColor([picture({ crop: { x: 40, y: 10, width: 50, height: 25 } })], { x: 100, y: 50 }, (_id, x, y) => { reads.push([x, y]); return [0, 0, 0, 255]; }, () => null);
    expect(reads).toEqual([[90, 35]]);
  });

  it('takes the topmost layer, skips hidden ones and falls through transparent pixels', () => {
    const nodes = [
      picture({ id: 'low', zIndex: 1 }),
      picture({ id: 'top', zIndex: 3 }),
      { id: 'hidden', type: 'shape', x: 0, y: 0, width: 100, height: 50, zIndex: 4, visible: false, strokeColor: '#111111' },
    ];
    const read = (id: string): [number, number, number, number] => (id === 'top' ? [0, 0, 0, 0] : [1, 2, 3, 255]);
    expect(sampleColor(nodes, { x: 5, y: 5 }, read, () => ({ width: 100, height: 50 }))).toBe('#010203');
  });

  it('reads the fill, then the stroke of shapes and nothing outside every layer', () => {
    const shape: SampleNode = { id: 's', type: 'shape', x: 0, y: 0, width: 10, height: 10, zIndex: 1, fillColor: '#ff0000', strokeColor: '#00ff00' };
    expect(sampleColor([shape], { x: 5, y: 5 }, () => null, () => null)).toBe('#ff0000');
    expect(sampleColor([{ ...shape, fillColor: 'transparent' }], { x: 5, y: 5 }, () => null, () => null)).toBe('#00ff00');
    expect(sampleColor([shape], { x: 50, y: 5 }, () => null, () => null)).toBeNull();
  });

  it('undoes the rotation around the top-left corner', () => {
    const point = toLocalPoint({ x: 0, y: 0, rotation: 90 }, { x: 0, y: 10 });
    expect(point.x).toBeCloseTo(10);
    expect(point.y).toBeCloseTo(0);
  });
});
