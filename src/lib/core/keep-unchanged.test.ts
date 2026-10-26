import { describe, expect, it } from 'vitest';

import { keepUnchanged, sameData } from './keep-unchanged';

describe('sameData', () => {
  it('compares plain data by value', () => {
    expect(sameData({ a: 1, b: [1, { c: 'x' }] }, { b: [1, { c: 'x' }], a: 1 })).toBe(true);
    expect(sameData({ a: 1 }, { a: 2 })).toBe(false);
    expect(sameData({ a: 1 }, { a: 1, b: undefined })).toBe(false);
    expect(sameData([1, 2], [1, 2, 3])).toBe(false);
    expect(sameData([1], { 0: 1 })).toBe(false);
    expect(sameData(null, {})).toBe(false);
    expect(sameData(Number.NaN, Number.NaN)).toBe(true);
  });

  it('compares Maps and Sets by content', () => {
    expect(sameData(new Map([['@img1', { url: 'u' }]]), new Map([['@img1', { url: 'u' }]]))).toBe(true);
    expect(sameData(new Map([['@img1', { url: 'u' }]]), new Map([['@img1', { url: 'v' }]]))).toBe(false);
    expect(sameData(new Set(['a', 'b']), new Set(['b', 'a']))).toBe(true);
    expect(sameData(new Set(['a']), new Set(['b']))).toBe(false);
  });

  it('treats functions and class instances as equal only when they are the same object', () => {
    const run = () => 1;
    expect(sameData({ run }, { run })).toBe(true);
    expect(sameData({ run }, { run: () => 1 })).toBe(false);
    expect(sameData(new Date(1), new Date(1))).toBe(false);
  });
});

describe('keepUnchanged', () => {
  const key = (item: { id: string }) => item.id;

  it('keeps the previous object of every unchanged item and takes the changed ones', () => {
    const previous = [{ id: 'a', text: 'one' }, { id: 'b', text: 'two' }];
    const next = keepUnchanged(previous, [{ id: 'a', text: 'one' }, { id: 'b', text: 'TWO' }, { id: 'c', text: 'three' }], key);
    expect(next[0]).toBe(previous[0]);
    expect(next[1]).not.toBe(previous[1]);
    expect(next.map((item) => item.text)).toEqual(['one', 'TWO', 'three']);
  });

  it('returns the previous list itself when nothing changed', () => {
    const previous = [{ id: 'a', text: 'one' }, { id: 'b', text: 'two' }];
    expect(keepUnchanged(previous, [{ id: 'a', text: 'one' }, { id: 'b', text: 'two' }], key)).toBe(previous);
  });

  it('returns a new list when items were removed or reordered, still sharing the items', () => {
    const previous = [{ id: 'a', text: 'one' }, { id: 'b', text: 'two' }];
    const removed = keepUnchanged(previous, [{ id: 'a', text: 'one' }], key);
    expect(removed).not.toBe(previous);
    expect(removed[0]).toBe(previous[0]);
    const reordered = keepUnchanged(previous, [{ id: 'b', text: 'two' }, { id: 'a', text: 'one' }], key);
    expect(reordered).not.toBe(previous);
    expect(reordered).toEqual([previous[1], previous[0]]);
    expect(reordered[0]).toBe(previous[1]);
  });

  it('passes the new list through when there is nothing to compare with', () => {
    const next = [{ id: 'a', text: 'one' }];
    expect(keepUnchanged(undefined, next, key)).toBe(next);
    expect(keepUnchanged([], next, key)).toBe(next);
  });
});
