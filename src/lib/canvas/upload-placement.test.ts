import { describe, expect, it } from 'vitest';

import { uploadPlacement } from './upload-placement';

const base = { size: { width: 100, height: 60 }, gap: 20, fallback: { x: 5, y: 7 } };

describe('uploadPlacement', () => {
  it('centres a dropped or pasted picture on the pointer', () => {
    expect(uploadPlacement({ ...base, at: { x: 300, y: 200 } })).toEqual({ x: 250, y: 170, width: 100, height: 60 });
  });

  it('fits the first button upload into a free frame', () => {
    const frameSlot = { x: 10, y: 20, width: 80, height: 50 };
    expect(uploadPlacement({ ...base, frameSlot, viewCenter: { x: 500, y: 500 } })).toEqual(frameSlot);
  });

  it('puts a button upload in the middle of the view when the frame is taken', () => {
    expect(uploadPlacement({ ...base, viewCenter: { x: 400, y: 300 } })).toEqual({ x: 350, y: 270, width: 100, height: 60 });
  });

  it('lines the pictures of one batch up to the right of the previous one', () => {
    const first = uploadPlacement({ ...base, at: { x: 300, y: 200 } });
    const second = uploadPlacement({ ...base, size: { width: 50, height: 90 }, at: { x: 300, y: 200 }, previous: first });
    expect(second).toEqual({ x: first.x + 100 + 20, y: first.y, width: 50, height: 90 });
  });

  it('falls back to the Schema position when nothing places it', () => {
    expect(uploadPlacement(base)).toEqual({ x: 5, y: 7, width: 100, height: 60 });
  });
});
