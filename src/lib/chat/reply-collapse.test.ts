import { describe, expect, it } from 'vitest';

import { REPLY_CHARACTERS_PER_LINE, REPLY_LINE_HEIGHT_EM, replyLineCount, replyNeedsCollapse } from '@/lib/chat/reply-collapse';

describe('the lines a reply occupies', () => {
  it('has no line for an empty reply and keeps explicit line breaks', () => {
    expect(replyLineCount('')).toBe(0);
    expect(replyLineCount('one line')).toBe(1);
    expect(replyLineCount('a\nb\nc')).toBe(3);
    expect(replyLineCount('a\n\nb')).toBe(3);
  });

  it('wraps at the character budget', () => {
    expect(replyLineCount('a'.repeat(REPLY_CHARACTERS_PER_LINE))).toBe(1);
    expect(replyLineCount('a'.repeat(REPLY_CHARACTERS_PER_LINE + 1))).toBe(2);
    expect(replyLineCount('a'.repeat(REPLY_CHARACTERS_PER_LINE * 2 + 1))).toBe(3);
    expect(replyLineCount(`a\n${'b'.repeat(REPLY_CHARACTERS_PER_LINE + 1)}`)).toBe(3);
  });

  it('counts a wide character as two columns, including full-width punctuation', () => {
    expect(replyLineCount('中'.repeat(REPLY_CHARACTERS_PER_LINE / 2))).toBe(1);
    expect(replyLineCount('中'.repeat(REPLY_CHARACTERS_PER_LINE / 2 + 1))).toBe(2);
    expect(replyLineCount('，'.repeat(REPLY_CHARACTERS_PER_LINE / 2 + 1))).toBe(2);
    expect(replyLineCount('한'.repeat(REPLY_CHARACTERS_PER_LINE / 2 + 1))).toBe(2);
  });

  it('never divides by a zero or negative budget', () => {
    expect(replyLineCount('abc', 0)).toBe(3);
    expect(replyLineCount('abc', -4)).toBe(3);
  });

  it('uses the line height the reply is rendered with', () => {
    expect(REPLY_LINE_HEIGHT_EM).toBe(1.625);
  });
});

describe('whether a finished reply starts folded', () => {
  it('folds only a reply longer than the declared line count', () => {
    const twelve = Array.from({ length: 12 }, (_, index) => `line ${index}`).join('\n');
    expect(replyNeedsCollapse(twelve, 12)).toBe(false);
    expect(replyNeedsCollapse(`${twelve}\nline 12`, 12)).toBe(true);
    expect(replyNeedsCollapse('short', 12)).toBe(false);
  });

  it('never folds when the Schema declares no line budget', () => {
    expect(replyNeedsCollapse('x\n'.repeat(100), 0)).toBe(false);
    expect(replyNeedsCollapse('', 12)).toBe(false);
  });
});
