import { describe, expect, it } from 'vitest';

import { canvasFont, layoutText, wrapTextLines, type TextMeasuringContext } from './text-layout';

/** Every character is 10 wide, including CJK, so widths are easy to reason about. */
const measure = (value: string) => Array.from(value).length * 10;

describe('wrapTextLines', () => {
  it('keeps a short paragraph whole and honours line breaks', () => {
    expect(wrapTextLines('hello\nworld', 100, measure)).toEqual(['hello', 'world']);
    expect(wrapTextLines('', 100, measure)).toEqual(['']);
  });

  it('breaks at the last space before the overflow', () => {
    expect(wrapTextLines('aaa bbb ccc', 70, measure)).toEqual(['aaa bbb', 'ccc']);
    expect(wrapTextLines('aaa-bbbbb', 60, measure)).toEqual(['aaa-', 'bbbbb']);
  });

  it('breaks between characters when there is no space (Chinese, a long word)', () => {
    expect(wrapTextLines('你好世界你好世界', 50, measure)).toEqual(['你好世界你', '好世界']);
    expect(wrapTextLines('abcdefghij', 40, measure)).toEqual(['abcd', 'efgh', 'ij']);
  });

  it('stops when not even one character fits, like Konva', () => {
    expect(wrapTextLines('abc', 5, measure)).toEqual([]);
  });
});

describe('layoutText', () => {
  const context = (ascent: number, descent: number): TextMeasuringContext => ({
    font: '',
    measureText: (value: string) => ({ width: measure(value), fontBoundingBoxAscent: ascent, fontBoundingBoxDescent: descent }),
  });

  it('puts the first alphabetic baseline where Konva does and uses the node font', () => {
    const layout = layoutText(context(20, 6), { text: 'a b c d', width: 30, fontSize: 24, fontFamily: 'sans-serif', lineHeight: 1.2 });
    expect(layout.font).toBe(canvasFont(24, 'sans-serif'));
    expect(layout.lines).toEqual(['a b', 'c d']);
    expect(layout.lineHeightPx).toBeCloseTo(28.8);
    expect(layout.firstBaselineY).toBeCloseTo((20 - 6) / 2 + 28.8 / 2);
  });

  it('a larger font size changes the line box, not the wrap rule', () => {
    const small = layoutText(context(10, 3), { text: 'x', width: 100, fontSize: 16, fontFamily: 'sans-serif', lineHeight: 1.2 });
    const large = layoutText(context(10, 3), { text: 'x', width: 100, fontSize: 48, fontFamily: 'sans-serif', lineHeight: 1.2 });
    expect(large.lineHeightPx / small.lineHeightPx).toBeCloseTo(3);
  });
});
