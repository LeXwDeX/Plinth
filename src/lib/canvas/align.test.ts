import { describe, expect, it } from 'vitest';

import { alignUnitCount, alignUpdates, distributeUpdates, nodeBox, type AlignNode } from './align';

const node = (id: string, x: number, y: number, width = 40, height = 20, extra: Partial<AlignNode> = {}): AlignNode => ({ id, x, y, width, height, ...extra });
const apply = (nodes: AlignNode[], updates: Array<{ id: string; x: number; y: number }>) =>
  nodes.map((item) => ({ ...item, ...(updates.find((update) => update.id === item.id) ?? {}) }));

describe('alignUpdates', () => {
  const nodes = [node('a', 10, 5, 40, 20), node('b', 100, 50, 30, 30), node('c', 60, 90, 80, 10)];
  const ids = ['a', 'b', 'c'];

  it('left alignment gives every selected node the same x', () => {
    const next = apply(nodes, alignUpdates(nodes, ids, 'left'));
    expect(new Set(next.map((item) => item.x))).toEqual(new Set([10]));
    expect(next.map((item) => item.y)).toEqual([5, 50, 90]);
  });

  it('right, top, bottom and the centres line up the matching edge', () => {
    const right = apply(nodes, alignUpdates(nodes, ids, 'right'));
    expect(new Set(right.map((item) => item.x + item.width))).toEqual(new Set([140]));
    const top = apply(nodes, alignUpdates(nodes, ids, 'top'));
    expect(new Set(top.map((item) => item.y))).toEqual(new Set([5]));
    const bottom = apply(nodes, alignUpdates(nodes, ids, 'bottom'));
    expect(new Set(bottom.map((item) => item.y + item.height))).toEqual(new Set([100]));
    const hcenter = apply(nodes, alignUpdates(nodes, ids, 'hcenter'));
    expect(new Set(hcenter.map((item) => item.x + item.width / 2))).toEqual(new Set([75]));
    const vcenter = apply(nodes, alignUpdates(nodes, ids, 'vcenter'));
    expect(new Set(vcenter.map((item) => item.y + item.height / 2))).toEqual(new Set([52.5]));
  });

  it('one node aligns to the dashed frame', () => {
    const frame = { x: 0, y: 0, width: 200, height: 100 };
    const [update] = alignUpdates([node('a', 10, 5, 40, 20)], ['a'], 'right', frame);
    expect(update).toEqual({ id: 'a', x: 160, y: 5 });
    expect(alignUpdates([node('a', 10, 5)], ['a'], 'left')).toEqual([]);
  });

  it('records nothing when everything is already aligned', () => {
    const aligned = [node('a', 10, 0), node('b', 10, 50)];
    expect(alignUpdates(aligned, ['a', 'b'], 'left')).toEqual([]);
  });

  it('keeps locked nodes where they are and moves a group as one unit', () => {
    const grouped = [node('a', 10, 0, 20, 20, { groupId: 'g' }), node('b', 50, 0, 20, 20, { groupId: 'g' }), node('c', 200, 0, 20, 20), node('d', 300, 0, 20, 20, { locked: true })];
    const next = apply(grouped, alignUpdates(grouped, ['a', 'b', 'c', 'd'], 'left'));
    expect(next.map((item) => item.x)).toEqual([10, 50, 10, 300]);
  });

  it('uses the box around a rotated node', () => {
    const box = nodeBox({ x: 0, y: 0, width: 10, height: 20, rotation: 90 });
    expect(box.x).toBeCloseTo(-20);
    expect(box.width).toBeCloseTo(20);
    expect(box.height).toBeCloseTo(10);
  });
});

describe('distributeUpdates', () => {
  it('makes the gaps between three nodes equal (within one pixel)', () => {
    const nodes = [node('a', 0, 0, 30, 10), node('b', 40, 0, 50, 10), node('c', 200, 0, 20, 10), node('d', 300, 0, 40, 10)];
    const next = apply(nodes, distributeUpdates(nodes, ['a', 'b', 'c', 'd'], 'horizontal'));
    const sorted = [...next].sort((left, right) => left.x - right.x);
    const gaps = sorted.slice(1).map((item, index) => item.x - (sorted[index].x + sorted[index].width));
    expect(Math.max(...gaps) - Math.min(...gaps)).toBeLessThanOrEqual(1);
    expect(sorted[0].x).toBe(0);
    expect(sorted[3].x).toBe(300);
  });

  it('spaces vertically too, and needs three nodes', () => {
    const nodes = [node('a', 0, 0, 10, 10), node('b', 0, 20, 10, 10), node('c', 0, 100, 10, 10)];
    const next = apply(nodes, distributeUpdates(nodes, ['a', 'b', 'c'], 'vertical'));
    expect(next.map((item) => item.y)).toEqual([0, 50, 100]);
    expect(distributeUpdates(nodes, ['a', 'b'], 'vertical')).toEqual([]);
  });

  it('counts units, a group being one', () => {
    const nodes = [node('a', 0, 0, 10, 10, { groupId: 'g' }), node('b', 20, 0, 10, 10, { groupId: 'g' }), node('c', 50, 0)];
    expect(alignUnitCount(nodes, ['a', 'b', 'c'])).toBe(2);
  });
});
