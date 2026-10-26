import { describe, expect, it } from 'vitest';

import { rectangleRegion } from './inpaint-export';
import { antsOutlines, previewSize } from './selection-preview';

describe('selection preview', () => {
  it('keeps the artboard ratio and never enlarges it', () => {
    expect(previewSize({ x: 0, y: 0, width: 1024, height: 512 }, 512)).toEqual({ width: 512, height: 256 });
    expect(previewSize({ x: 0, y: 0, width: 200, height: 100 }, 512)).toEqual({ width: 200, height: 100 });
  });

  it('outlines only the added regions that are still in effect', () => {
    const add = rectangleRegion({ x: 0, y: 0 }, { x: 10, y: 10 });
    const subtract = rectangleRegion({ x: 2, y: 2 }, { x: 4, y: 4 }, true);
    const brush = { points: [1, 1, 5, 5], size: 4 };
    expect(antsOutlines([add, subtract, brush])).toEqual([add.points]);
    expect(antsOutlines([add, { points: [], size: 0, invert: true }])).toEqual([]);
    expect(antsOutlines([add, { points: [], size: 0, invert: true }, add])).toEqual([add.points]);
  });
});
