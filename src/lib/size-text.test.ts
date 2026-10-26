import { describe, expect, it } from 'vitest';
import { formatFileSize, sizeText } from './size-text';

// #327: one way to write sizes — decimal MB with the unit — so the page, Schema limits and the
// Agent's tool results (Go schema.SizeText) agree: the dev-test Bang result is 91.8 MB, not 87.5.
describe('sizeText', () => {
  it.each([
    [91_800_000, true, '91.8 MB'],
    [91_754_311, true, '91.76 MB'],
    [10_516_505, true, '10.52 MB'],
    [10_485_760, false, '10.48 MB'],
    [10_485_761, true, '10.49 MB'],
    [52_428_800, false, '52.42 MB'],
    [20_000_000, false, '20 MB'],
    [20_000_001, true, '20.01 MB'],
    [150_000_000, false, '150 MB'],
    [4097, true, '4.1 KB'],
    [1000, false, '1 KB'],
    [999_999, true, '1000 KB'],
    [0, true, '0 KB'],
  ])('writes %d bytes (round up %s) as %s, like Go schema.SizeText', (bytes, roundUp, want) => {
    expect(sizeText(bytes, roundUp)).toBe(want);
  });

  it('writes a file size rounded up and treats invalid input as zero', () => {
    expect(formatFileSize(91_800_000)).toBe('91.8 MB');
    expect(formatFileSize(Number.NaN)).toBe('0 KB');
    expect(formatFileSize(-5)).toBe('0 KB');
  });
});
