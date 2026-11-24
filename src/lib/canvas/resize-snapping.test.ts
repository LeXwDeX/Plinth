import { describe, expect, it } from 'vitest';

import { isResizeAnchor, snapBypassed, snapResize } from './snapping';

// A neighbour to the right: its left edge at x=300, centre 350, right 400; top 100, middle 150, bottom 200.
const neighbour = { x: 300, y: 100, width: 100, height: 100 };

describe('snapping while a corner handle resizes (#546)', () => {
  it("pulls the dragged right edge onto a neighbour's left edge, the left edge staying put", () => {
    const { box, guides } = snapResize({ x: 0, y: 0, width: 296, height: 80 }, 'bottom-right', [neighbour], { threshold: 6, keepRatio: false });
    expect(box).toMatchObject({ x: 0, y: 0, width: 300, height: 80 });
    expect(guides).toEqual([{ orientation: 'vertical', position: 300, from: 0, to: 200 }]);
  });

  it("pulls the dragged edge onto a neighbour's centre line too", () => {
    const { box } = snapResize({ x: 0, y: 0, width: 353, height: 40 }, 'top-right', [neighbour], { threshold: 6, keepRatio: false });
    expect(box.width).toBe(350);
    // The top edge was dragged too; 0 is not near any of 100/150/200, so it stays.
    expect(box.y).toBe(0);
  });

  it('moves the left and top edges when the top-left corner is dragged, keeping the bottom-right corner', () => {
    const { box, guides } = snapResize({ x: 403, y: 197, width: 97, height: 103 }, 'top-left', [neighbour], { threshold: 6, keepRatio: false });
    expect(box).toMatchObject({ x: 400, y: 200, width: 100, height: 100 });
    expect(guides.map((guide) => guide.orientation).sort()).toEqual(['horizontal', 'vertical']);
  });

  it('keeps the ratio: only the nearer axis snaps and the other edge follows', () => {
    // 2:1 box; its right edge is 4 from x=300, its bottom edge 3 from y=100: the bottom wins.
    const { box, guides } = snapResize({ x: 0, y: 0, width: 194, height: 97 }, 'bottom-right', [neighbour], { threshold: 6, keepRatio: true });
    expect(box).toMatchObject({ x: 0, y: 0, width: 200, height: 100 });
    expect(box.width / box.height).toBeCloseTo(2);
    expect(guides).toHaveLength(1);
    expect(guides[0]).toMatchObject({ orientation: 'horizontal', position: 100 });
  });

  it('keeps the ratio when the dragged corner is the top-left one, anchored at the opposite corner', () => {
    const { box } = snapResize({ x: 402, y: 300, width: 98, height: 49 }, 'top-left', [neighbour], { threshold: 6, keepRatio: true });
    expect(box.x).toBe(400);
    expect(box.x + box.width).toBe(500);
    expect(box.y + box.height).toBe(349);
    expect(box.width / box.height).toBeCloseTo(98 / 49);
  });

  it('does not snap to a line beyond the threshold', () => {
    const start = { x: 0, y: 0, width: 290, height: 80 };
    const { box, guides } = snapResize(start, 'bottom-right', [neighbour], { threshold: 6, keepRatio: false });
    expect(box).toEqual(start);
    expect(guides).toEqual([]);
  });

  it('does not snap a moving edge past the fixed one, nor a rotated box', () => {
    // The only line in reach (x=-1, a neighbour's right edge) lies on the far side of the fixed left edge at x=0.
    expect(snapResize({ x: 0, y: 500, width: 4, height: 8 }, 'bottom-right', [{ x: -11, y: 0, width: 10, height: 10 }], { threshold: 6, keepRatio: false }).box.width).toBe(4);
    const rotated = { x: 0, y: 0, width: 296, height: 80, rotation: 30 };
    expect(snapResize(rotated, 'bottom-right', [neighbour], { threshold: 6, keepRatio: false }).box).toBe(rotated);
  });

  it('turns off while the bypass key is held: Ctrl or Command for "mod"', () => {
    expect(snapBypassed({ ctrlKey: true }, 'mod')).toBe(true);
    expect(snapBypassed({ metaKey: true }, 'mod')).toBe(true);
    expect(snapBypassed({ altKey: true }, 'mod')).toBe(false);
    expect(snapBypassed({ altKey: true }, 'alt')).toBe(true);
    expect(snapBypassed({ shiftKey: true }, 'shift')).toBe(true);
    expect(snapBypassed(undefined, 'mod')).toBe(false);
  });

  it('knows the four corner handles and nothing else', () => {
    expect(['top-left', 'top-right', 'bottom-left', 'bottom-right'].every(isResizeAnchor)).toBe(true);
    expect(isResizeAnchor('rotater')).toBe(false);
    expect(isResizeAnchor(null)).toBe(false);
  });
});
