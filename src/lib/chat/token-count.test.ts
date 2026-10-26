import { describe, expect, it } from 'vitest';

import { formatContextUsage, formatTokenCount } from '@/lib/chat/token-count';

describe('formatTokenCount (#322)', () => {
  it.each([
    [0, '0'],
    [7, '7'],
    [999, '999'],
    [1_000, '1K'],
    [1_049, '1K'],
    [1_050, '1.1K'],
    [12_345, '12.3K'],
    [99_949, '99.9K'],
    [99_950, '100K'],
    [99_960, '100K'],
    [128_000, '128K'],
    [356_000, '356K'],
    [356_400, '356K'],
    [999_499, '999K'],
    [999_500, '1M'],
    [999_999, '1M'],
    [1_000_000, '1M'],
    [1_048_576, '1M'],
    [1_049_999, '1M'],
    [1_050_000, '1.1M'],
    [1_200_000, '1.2M'],
    [99_949_999, '99.9M'],
    [99_950_000, '100M'],
    [123_456_789, '123M'],
  ])('writes %d as %s', (value, text) => {
    expect(formatTokenCount(value)).toBe(text);
  });

  it('never writes a trailing ".0"', () => {
    for (const value of [1_000, 2_000, 10_000, 99_000, 1_000_000, 2_000_000, 10_000_000]) {
      expect(formatTokenCount(value)).not.toContain('.0');
    }
  });
});

describe('formatContextUsage (#322)', () => {
  it('writes used / window when both are known', () => {
    expect(formatContextUsage(12_345, 1_000_000, '—')).toBe('12.3K / 1M');
    expect(formatContextUsage(356_000, 1_000_000, '—')).toBe('356K / 1M');
    expect(formatContextUsage(12_300, 128_000, '—')).toBe('12.3K / 128K');
    expect(formatContextUsage(812, 128_000, '—')).toBe('812 / 128K');
  });

  it('writes only what was used when no window is declared', () => {
    expect(formatContextUsage(12_345, null, '—')).toBe('12.3K');
  });

  it('writes the unknown mark, never a guess, when the upstream did not report usage', () => {
    expect(formatContextUsage(null, 1_000_000, '—')).toBe('— / 1M');
    expect(formatContextUsage(null, null, '—')).toBe('—');
  });
});
