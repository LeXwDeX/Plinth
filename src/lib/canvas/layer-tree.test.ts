import { describe, expect, it } from 'vitest';

import { arrangeLayers, layerRows, type TreeNode } from './layer-tree';

const layer = (id: string, zIndex: number, extra: Partial<TreeNode> = {}): TreeNode => ({ id, zIndex, ...extra });
const ids = (rows: ReturnType<typeof layerRows>) => rows.map((row) => (row.kind === 'group' ? `[${row.groupId}]` : row.node.id));

describe('layerRows', () => {
  const nodes = [layer('a', 1), layer('b', 2, { groupId: 'g' }), layer('c', 3, { groupId: 'g' }), layer('d', 4)];

  it('lists top to bottom with the group folder at its highest member', () => {
    expect(ids(layerRows(nodes, new Set()))).toEqual(['d', '[g]', 'c', 'b', 'a']);
  });

  it('hides the members of a collapsed group', () => {
    const rows = layerRows(nodes, new Set(['g']));
    expect(ids(rows)).toEqual(['d', '[g]', 'a']);
    expect(rows[1]).toMatchObject({ kind: 'group', collapsed: true, memberIds: ['c', 'b'] });
  });

  it('a group is visible when any member is, and locked when every member is', () => {
    const mixed = [layer('b', 2, { groupId: 'g', visible: false, locked: true }), layer('c', 3, { groupId: 'g' })];
    expect(layerRows(mixed, new Set())[0]).toMatchObject({ visible: true, locked: false });
    const hidden = [layer('b', 2, { groupId: 'g', visible: false, locked: true }), layer('c', 3, { groupId: 'g', visible: false, locked: true })];
    expect(layerRows(hidden, new Set())[0]).toMatchObject({ visible: false, locked: true });
  });
});

describe('arrangeLayers', () => {
  const nodes = [layer('a', 1), layer('b', 2), layer('c', 3), layer('d', 4)];

  it('dragging the bottom layer above the top one puts it on top', () => {
    const result = arrangeLayers(nodes, ['a'], { nodeId: 'd', position: 'above' });
    expect(result?.order).toEqual(['b', 'c', 'd', 'a']);
  });

  it('dropping below a row puts the layer right under it', () => {
    expect(arrangeLayers(nodes, ['d'], { nodeId: 'b', position: 'below' })?.order).toEqual(['a', 'd', 'b', 'c']);
  });

  it('a drop that changes nothing is null, as is a drop onto itself', () => {
    expect(arrangeLayers(nodes, ['b'], { nodeId: 'a', position: 'above' })).toBeNull();
    expect(arrangeLayers(nodes, ['b'], { nodeId: 'b', position: 'above' })).toBeNull();
    expect(arrangeLayers(nodes, [], { nodeId: 'b', position: 'above' })).toBeNull();
  });

  it('drops into a group and out of it again', () => {
    const grouped = [layer('a', 1), layer('b', 2, { groupId: 'g' }), layer('c', 3, { groupId: 'g' }), layer('d', 4)];
    const into = arrangeLayers(grouped, ['d'], { groupId: 'g', position: 'into' });
    expect(into?.groupIds.d).toBe('g');
    expect(into?.order).toEqual(['a', 'b', 'c', 'd']);
    const next = grouped.map((node) => ({ ...node, zIndex: into!.order.indexOf(node.id) + 1, groupId: into!.groupIds[node.id] }));
    const out = arrangeLayers(next, ['d'], { nodeId: 'a', position: 'below' });
    expect(out?.groupIds.d).toBeUndefined();
    expect(out?.order[0]).toBe('d');
  });

  it('a layer dropped beside a member joins that member group', () => {
    const grouped = [layer('a', 1), layer('b', 2, { groupId: 'g' }), layer('c', 3, { groupId: 'g' })];
    expect(arrangeLayers(grouped, ['a'], { nodeId: 'c', position: 'above' })?.groupIds.a).toBe('g');
  });

  it('moves a whole group as a unit and never nests it', () => {
    const grouped = [layer('a', 1), layer('b', 2, { groupId: 'g' }), layer('c', 3, { groupId: 'g' }), layer('d', 4, { groupId: 'h' }), layer('e', 5, { groupId: 'h' })];
    const result = arrangeLayers(grouped, ['b', 'c'], { nodeId: 'd', position: 'above' });
    expect(result?.order).toEqual(['a', 'd', 'e', 'b', 'c']);
    expect(result?.groupIds).toMatchObject({ b: 'g', c: 'g', d: 'h', e: 'h' });
    expect(arrangeLayers(grouped, ['b', 'c'], { groupId: 'h', position: 'into' })).toBeNull();
  });

  it('a group left with one layer is dissolved', () => {
    const grouped = [layer('a', 1, { groupId: 'g' }), layer('b', 2, { groupId: 'g' }), layer('c', 3)];
    const result = arrangeLayers(grouped, ['a'], { nodeId: 'c', position: 'above' });
    expect(result?.groupIds).toMatchObject({ a: undefined, b: undefined });
  });
});
