import { describe, expect, it } from 'vitest';
import { reorderLayers } from './layer-order';

const n = (id: string, zIndex: number, groupId?: string) => ({ id, zIndex, groupId });
const flat = [n('a', 1), n('b', 2), n('c', 3), n('d', 4)];

describe('reorderLayers', () => {
  it('moves one node a step up or down', () => {
    expect(reorderLayers(flat, ['b'], 'forward')).toEqual(['a', 'c', 'b', 'd']);
    expect(reorderLayers(flat, ['c'], 'backward')).toEqual(['a', 'c', 'b', 'd']);
  });
  it('moves to the top and bottom', () => {
    expect(reorderLayers(flat, ['b'], 'front')).toEqual(['a', 'c', 'd', 'b']);
    expect(reorderLayers(flat, ['c'], 'back')).toEqual(['c', 'a', 'b', 'd']);
  });
  it('returns null at the edges and for an empty selection', () => {
    expect(reorderLayers(flat, ['d'], 'forward')).toBeNull();
    expect(reorderLayers(flat, ['d'], 'front')).toBeNull();
    expect(reorderLayers(flat, ['a'], 'backward')).toBeNull();
    expect(reorderLayers(flat, ['a'], 'back')).toBeNull();
    expect(reorderLayers(flat, [], 'front')).toBeNull();
  });
  it('keeps the relative order of a multi-selection', () => {
    expect(reorderLayers(flat, ['d', 'a'], 'front')).toEqual(['b', 'c', 'a', 'd']);
    expect(reorderLayers(flat, ['a', 'c'], 'forward')).toEqual(['b', 'a', 'd', 'c']);
    expect(reorderLayers(flat, ['b', 'd'], 'backward')).toEqual(['b', 'a', 'd', 'c']);
    expect(reorderLayers(flat, ['b', 'd'], 'forward')).toEqual(['a', 'c', 'b', 'd']);
  });
  it('moves a group as a unit and takes siblings along', () => {
    const grouped = [n('a', 1, 'g'), n('b', 2), n('c', 3, 'g'), n('d', 4)];
    expect(reorderLayers(grouped, ['a'], 'front')).toEqual(['b', 'd', 'a', 'c']);
    expect(reorderLayers(grouped, ['a'], 'forward')).toEqual(['b', 'a', 'c', 'd']);
  });
  it('steps over a whole group rather than into it', () => {
    const grouped = [n('a', 1), n('b', 2, 'g'), n('c', 3, 'g'), n('d', 4)];
    expect(reorderLayers(grouped, ['a'], 'forward')).toEqual(['b', 'c', 'a', 'd']);
    expect(reorderLayers(grouped, ['d'], 'backward')).toEqual(['a', 'd', 'b', 'c']);
  });
});
