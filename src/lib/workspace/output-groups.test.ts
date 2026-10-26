import { describe, expect, it } from 'vitest';

import { clearSelection, escapeClearsSelection, groupByDay, selectedCount, selectionAfterClick, toggleSelection } from './output-groups';

const at = (year: number, month: number, day: number, hour = 12) => new Date(year, month - 1, day, hour).toISOString();
const item = (key: string, createdAt: string) => ({ key, createdAt });

describe('groupByDay', () => {
  const now = new Date(2026, 9, 8, 15);

  it('groups by calendar day, newest day first, keeping the order given inside a day', () => {
    const groups = groupByDay([
      item('a', at(2026, 10, 6)), item('b', at(2026, 10, 8, 9)), item('c', at(2026, 10, 7)), item('d', at(2026, 10, 8, 14)),
    ], (entry) => entry.createdAt, now);
    expect(groups.map((group) => [group.kind, group.items.map((entry) => entry.key)])).toEqual([
      ['today', ['b', 'd']],
      ['yesterday', ['c']],
      ['date', ['a']],
    ]);
    expect(groups[2]).toMatchObject({ year: 2026, month: 10, dayOfMonth: 6, day: '2026-10-06' });
  });

  it('puts what has no time yet (work just started) under today', () => {
    const groups = groupByDay([item('new', ''), item('odd', 'not a time')], (entry) => entry.createdAt, now);
    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({ kind: 'today' });
    expect(groups[0].items.map((entry) => entry.key)).toEqual(['new', 'odd']);
  });

  it('knows yesterday across a month edge', () => {
    const groups = groupByDay([item('a', at(2026, 9, 30))], (entry) => entry.createdAt, new Date(2026, 9, 1, 8));
    expect(groups[0].kind).toBe('yesterday');
  });
});

describe('selectionAfterClick', () => {
  const order = ['a', 'b', 'c', 'd', 'e'];

  it('flips one tile on a plain click', () => {
    expect([...selectionAfterClick(order, new Set(), null, 'b', false)]).toEqual(['b']);
    expect([...selectionAfterClick(order, new Set(['b', 'c']), 'b', 'b', false)]).toEqual(['c']);
  });

  it('selects the run from the last click to this one with Shift, either direction', () => {
    expect([...selectionAfterClick(order, new Set(['b']), 'b', 'd', true)].sort()).toEqual(['b', 'c', 'd']);
    expect([...selectionAfterClick(order, new Set(['d']), 'd', 'b', true)].sort()).toEqual(['b', 'c', 'd']);
  });

  it('keeps what was already chosen and falls back to one tile when there is nothing to start from', () => {
    expect([...selectionAfterClick(order, new Set(['a']), 'c', 'e', true)].sort()).toEqual(['a', 'c', 'd', 'e']);
    expect([...selectionAfterClick(order, new Set(), null, 'c', true)]).toEqual(['c']);
    expect([...selectionAfterClick(order, new Set(), 'gone', 'c', true)]).toEqual(['c']);
  });
});

describe('tile selection helpers', () => {
  it('toggles one key without touching the others', () => {
    const one = toggleSelection(new Set(['a']), 'b');
    expect([...one].sort()).toEqual(['a', 'b']);
    expect([...toggleSelection(one, 'a')]).toEqual(['b']);
  });

  it('clears, and counts only keys that can still be chosen', () => {
    expect(clearSelection().size).toBe(0);
    expect(selectedCount(['a', 'b', 'c'], new Set(['a', 'c', 'gone']))).toBe(2);
  });

  it('lets Esc clear only when something is selected and nothing above owns the key', () => {
    const free = { lightbox: false, popupOpen: false, editable: false };
    expect(escapeClearsSelection(2, free)).toBe(true);
    expect(escapeClearsSelection(0, free)).toBe(false);
    expect(escapeClearsSelection(2, { ...free, lightbox: true })).toBe(false);
    expect(escapeClearsSelection(2, { ...free, popupOpen: true })).toBe(false);
    expect(escapeClearsSelection(2, { ...free, editable: true })).toBe(false);
  });
});
