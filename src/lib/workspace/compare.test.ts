import { describe, expect, it } from 'vitest';

import { compareCapacity, compareFromGroup, isModelSlot, removeCompareSlot, setCompareMode, sliderSlots, slotKey, toggleCompareSlot, type CompareSlot, type CompareState } from './compare';

const slot = (id: string): CompareSlot => ({ key: slotKey('t', id), taskId: 't', assetId: id, url: `/a/${id}`, label: id });

describe('compare state', () => {
  it('fills panes in order and takes a picture out when it is picked again', () => {
    let state: CompareState = { mode: 'two', slots: [] };
    state = toggleCompareSlot(state, slot('a'));
    state = toggleCompareSlot(state, slot('b'));
    expect(state.slots.map((item) => item.assetId)).toEqual(['a', 'b']);
    state = toggleCompareSlot(state, slot('a'));
    expect(state.slots.map((item) => item.assetId)).toEqual(['b']);
  });

  it('drops the oldest pane when the grid is full', () => {
    let state: CompareState = { mode: 'two', slots: [slot('a'), slot('b')] };
    state = toggleCompareSlot(state, slot('c'));
    expect(state.slots.map((item) => item.assetId)).toEqual(['b', 'c']);
    expect(compareCapacity('four')).toBe(4);
    expect(compareCapacity('slider')).toBe(2);
  });

  it('keeps the latest pictures that still fit when the mode narrows, and all when it widens', () => {
    const four: CompareState = { mode: 'four', slots: [slot('a'), slot('b'), slot('c'), slot('d')] };
    expect(setCompareMode(four, 'slider').slots.map((item) => item.assetId)).toEqual(['c', 'd']);
    expect(setCompareMode(setCompareMode(four, 'two'), 'four').slots).toHaveLength(2);
    expect(setCompareMode(four, 'four')).toEqual(four);
  });

  it('removes one pane and builds a group comparison', () => {
    expect(removeCompareSlot({ mode: 'two', slots: [slot('a'), slot('b')] }, slotKey('t', 'a')).slots).toHaveLength(1);
    expect(compareFromGroup([slot('a'), slot('b')]).mode).toBe('two');
    const group = compareFromGroup([slot('a'), slot('b'), slot('c'), slot('d'), slot('e')]);
    expect(group.mode).toBe('four');
    expect(group.slots).toHaveLength(4);
  });

  it('takes models as slots, and leaves them out of the slider', () => {
    const model: CompareSlot = { ...slot('m'), kind: 'model' };
    expect(isModelSlot(model)).toBe(true);
    expect(isModelSlot(slot('a'))).toBe(false);
    let state: CompareState = { mode: 'two', slots: [] };
    state = toggleCompareSlot(state, slot('a'));
    state = toggleCompareSlot(state, model);
    expect(state.slots.map((item) => item.key)).toEqual([slotKey('t', 'a'), slotKey('t', 'm')]);
    expect(sliderSlots(state.slots).map((item) => item.key)).toEqual([slotKey('t', 'a')]);
  });
});
