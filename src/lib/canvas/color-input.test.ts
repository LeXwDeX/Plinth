import { describe, expect, it } from 'vitest';

import { clampChannel, hexToRgb, normalizeHex, rgbToHex } from './color-input';

const range = { rgb_minimum: 0, rgb_maximum: 255 };

describe('custom colour entry', () => {
  it('normalises hex in its usual spellings', () => {
    expect(normalizeHex('#FF8800')).toBe('#ff8800');
    expect(normalizeHex('ff8800')).toBe('#ff8800');
    expect(normalizeHex(' #f80 ')).toBe('#ffaa00'.replace('aa', '88'));
    expect(normalizeHex('#ff88')).toBeNull();
    expect(normalizeHex('red')).toBeNull();
    expect(normalizeHex('')).toBeNull();
  });

  it('converts between hex and channels', () => {
    expect(hexToRgb('#ff8800')).toEqual({ r: 255, g: 136, b: 0 });
    expect(hexToRgb('nope')).toBeNull();
    expect(rgbToHex({ r: 255, g: 136, b: 0 }, range)).toBe('#ff8800');
  });

  it('clamps channels to the Schema range', () => {
    expect(clampChannel(300, range)).toBe(255);
    expect(clampChannel(-4, range)).toBe(0);
    expect(clampChannel(Number.NaN, range)).toBe(0);
    expect(clampChannel(12.6, range)).toBe(13);
    expect(rgbToHex({ r: 999, g: -1, b: 16 }, range)).toBe('#ff0010');
  });
});
