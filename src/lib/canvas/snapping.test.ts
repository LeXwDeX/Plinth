import { describe, expect, it } from 'vitest';

import { snapRect, type SnapRect } from './snapping';

const artboard: SnapRect = { x: 0, y: 0, width: 400, height: 300 };

describe('snapRect', () => {
  it('snaps the left edge to the artboard edge within the threshold', () => {
    const result = snapRect({ x: 4, y: 100, width: 50, height: 50 }, [artboard], 6);
    expect(result.x).toBe(0);
    expect(result.guides.some((g) => g.orientation === 'vertical' && g.position === 0)).toBe(true);
  });

  it('snaps the right edge to the artboard edge', () => {
    const result = snapRect({ x: 347, y: 100, width: 50, height: 50 }, [artboard], 6);
    expect(result.x).toBe(350);
  });

  it('snaps the center to the artboard center lines', () => {
    const result = snapRect({ x: 176, y: 123, width: 50, height: 50 }, [artboard], 6);
    expect(result.x).toBe(175);
    expect(result.y).toBe(125);
    expect(result.guides.map((g) => g.orientation).sort()).toEqual(['horizontal', 'vertical']);
  });

  it('does not snap beyond the threshold and returns no guides', () => {
    const result = snapRect({ x: 20, y: 60, width: 50, height: 50 }, [artboard], 6);
    expect(result).toEqual({ x: 20, y: 60, guides: [] });
  });

  it('snaps to another node edge', () => {
    const other: SnapRect = { x: 100, y: 500, width: 80, height: 80 };
    const result = snapRect({ x: 183, y: 40, width: 50, height: 50 }, [artboard, other], 6);
    expect(result.x).toBe(180);
    expect(result.guides.some((g) => g.position === 180 && g.orientation === 'vertical')).toBe(true);
  });

  it('prefers the closest candidate', () => {
    const result = snapRect({ x: 3, y: 100, width: 50, height: 50 }, [artboard, { x: 5, y: 0, width: 10, height: 10 }], 6);
    expect(result.x).toBe(5);
  });

  it('skips rotated nodes', () => {
    const rotated = { x: 4, y: 4, width: 50, height: 50, rotation: 30 };
    expect(snapRect(rotated, [artboard], 6)).toEqual({ x: 4, y: 4, guides: [] });
    const result = snapRect({ x: 4, y: 100, width: 50, height: 50 }, [{ ...artboard, rotation: 10 }], 6);
    expect(result.x).toBe(4);
  });

  it('uses secondary targets only on an axis no primary target reaches', () => {
    const inner: SnapRect = { x: 300, y: 1000, width: 100, height: 10 };
    const fallback = snapRect({ x: 303, y: 600, width: 20, height: 20 }, [artboard], 6, [inner]);
    expect(fallback.x).toBe(300);
    expect(fallback.y).toBe(600);
    expect(fallback.guides.map((g) => g.position)).toEqual([300]);
    // The artboard's right edge (400) is within reach too: the primary target wins and draws the guide.
    const primary = snapRect({ x: 378, y: 600, width: 20, height: 20 }, [artboard], 6, [{ ...inner, x: 381 }]);
    expect(primary.x).toBe(380);
    expect(primary.guides.map((g) => g.position)).toEqual([400]);
  });
});
