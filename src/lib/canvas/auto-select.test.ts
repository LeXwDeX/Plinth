import { describe, expect, it } from 'vitest';

import { clickTargets, oppositeMode, selectionAfterClick } from './auto-select';

const nodes = [{ id: 'a', groupId: 'g' }, { id: 'b', groupId: 'g' }, { id: 'c' }];

describe('auto-select', () => {
  it('a plain click on a group member selects the whole group by default', () => {
    expect(selectionAfterClick([], nodes, 'a', 'group', { shift: false, ctrl: false })).toEqual(['a', 'b']);
  });

  it('Ctrl+click picks the single layer, and the other way round in layer mode', () => {
    expect(selectionAfterClick([], nodes, 'a', 'group', { shift: false, ctrl: true })).toEqual(['a']);
    expect(selectionAfterClick([], nodes, 'a', 'layer', { shift: false, ctrl: false })).toEqual(['a']);
    expect(selectionAfterClick([], nodes, 'a', 'layer', { shift: false, ctrl: true })).toEqual(['a', 'b']);
  });

  it('an ungrouped layer is always just itself', () => {
    expect(clickTargets(nodes, 'c', 'group', false)).toEqual(['c']);
    expect(clickTargets(nodes, 'missing', 'group', false)).toEqual([]);
  });

  it('Shift adds a group to the selection and a second Shift click removes it', () => {
    const added = selectionAfterClick(['c'], nodes, 'a', 'group', { shift: true, ctrl: false });
    expect(added).toEqual(['c', 'a', 'b']);
    expect(selectionAfterClick(added, nodes, 'b', 'group', { shift: true, ctrl: false })).toEqual(['c']);
  });

  it('flips the mode', () => {
    expect(oppositeMode('group')).toBe('layer');
    expect(oppositeMode('layer')).toBe('group');
  });
});
