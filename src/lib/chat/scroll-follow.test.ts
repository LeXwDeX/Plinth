import { describe, expect, it } from 'vitest';

import { isNearBottom, shouldFollow } from './scroll-follow';

describe('scroll follow', () => {
  it('knows the bottom with a small tolerance', () => {
    expect(isNearBottom({ scrollTop: 500, scrollHeight: 1000, clientHeight: 500 })).toBe(true);
    expect(isNearBottom({ scrollTop: 460, scrollHeight: 1000, clientHeight: 500 })).toBe(true);
    expect(isNearBottom({ scrollTop: 100, scrollHeight: 1000, clientHeight: 500 })).toBe(false);
  });
  it('never pulls a reader who scrolled up, except for their own message', () => {
    expect(shouldFollow({ following: false, ownMessage: false })).toBe(false);
    expect(shouldFollow({ following: false, ownMessage: true })).toBe(true);
    expect(shouldFollow({ following: true, ownMessage: false })).toBe(true);
  });
});
