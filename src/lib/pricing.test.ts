import { describe, expect, it } from 'vitest';
import { formatNativeAmount, formatNativePrice, formatPriceTotal, parseNativeQuote, parsePriceTotals } from './pricing';

const validQuote = {
  version: 1,
  unit: 'hyper3d.credit',
  unit_label: 'Hyper3D credits',
  base_amount: '0.5',
  extra_amount: '2.5',
  total_amount: '3',
  components: [
    { id: 'extreme_geometry', label: 'Extreme geometry', amount: '0.5' },
    { id: 'extreme_texture', label: 'Extreme texture', amount: '2' },
  ],
};

describe('native provider pricing', () => {
  it('formats exact canonical decimals without converting through Number', () => {
    expect(formatNativeAmount('0.5')).toBe('0.5');
    expect(formatNativeAmount('0.000001')).toBe('0.000001');
    expect(formatNativeAmount('1000000.2')).toBe('1,000,000.2');
    expect(formatNativePrice('0.5', 'Hyper3D credits')).toBe('0.5 Hyper3D credits');
  });

  it.each([0.5, '00.5', '1.0', '1e2', '+1', '-1', '0.0000001', '1000000001'])('rejects non-canonical or invalid amount %s', (value) => {
    expect(formatNativeAmount(value)).toBeNull();
  });

  it('accepts a self-consistent frozen quote and rejects unknown or inconsistent quotes', () => {
    expect(parseNativeQuote(validQuote)).toMatchObject({ version: 1, total_amount: '3', unit: 'hyper3d.credit' });
    expect(parseNativeQuote({ ...validQuote, version: 2 })).toBeNull();
    expect(parseNativeQuote({ ...validQuote, total_amount: '2.99' })).toBeNull();
    expect(parseNativeQuote({ ...validQuote, components: [{ id: 'bad', label: 'Bad', amount: '2' }] })).toBeNull();
    expect(parseNativeQuote({ ...validQuote, components: undefined })).toBeNull();
    expect(parseNativeQuote({ ...validQuote, unit: 'legacy.credits' })).toBeNull();
    expect(parseNativeQuote({ ...validQuote, unit_label: '💰'.repeat(33) })).toBeNull();
    expect(parseNativeQuote({ ...validQuote, base_amount: '1234567890123', extra_amount: '0', total_amount: '1234567890123', components: undefined })).toBeNull();
  });

  it('allows a base-only quote without components', () => {
    expect(parseNativeQuote({ ...validQuote, base_amount: '0.5', extra_amount: '0', total_amount: '0.5', components: undefined }))
      .toMatchObject({ base_amount: '0.5', extra_amount: '0', total_amount: '0.5' });
  });

  it('validates server unit aggregates and formats each without mixing units', () => {
    const totals = parsePriceTotals([
      { unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '3' },
      { unit: 'legacy.credits', unit_label: 'Legacy credits', amount: '7' },
    ]);
    expect(totals?.map(formatPriceTotal)).toEqual(['3 Hyper3D credits', '7 Legacy credits']);
    expect(parsePriceTotals([{ unit: 'legacy.credits', unit_label: 'Legacy credits', amount: '2.5' }])).toBeNull();
    expect(parsePriceTotals([{ unit: 'legacy.credits', unit_label: 'Legacy credits', amount: '-1' }])).toBeNull();
    expect(parsePriceTotals([{ unit: 'same', unit_label: 'One', amount: '1' }, { unit: 'same', unit_label: 'One', amount: '2' }])).toBeNull();
    expect(parsePriceTotals([{ unit: 'same', unit_label: 'One', amount: 2 }])).toBeNull();
    expect(parsePriceTotals([{ unit: 'same', unit_label: '', amount: '2' }])).toBeNull();
    expect(formatPriceTotal({ unit: 'same', unit_label: 'One', amount: '1.0' })).toBeNull();
    expect(formatPriceTotal({ unit: 'large', unit_label: 'units', amount: '9223372036854.775807' }))
      .toBe('9,223,372,036,854.775807 units');
    expect(formatPriceTotal({ unit: 'overflow', unit_label: 'units', amount: '9223372036854.775808' })).toBeNull();
  });
});
