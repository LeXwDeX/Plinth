import { describe, expect, it } from 'vitest';
import { addMonths, buildMonthGrid, dateRangeBounds, formatDate, formatLocalDate, localDayBoundary, parseDate } from './date-field-math';

describe('date-field-math', () => {
  it('turns a day into its local first and last moment, not UTC midnight', () => {
    expect(localDayBoundary('2026-10-10', false)).toEqual(new Date(2026, 9, 10, 0, 0, 0, 0));
    expect(localDayBoundary('2026-10-10', true)).toEqual(new Date(2026, 9, 10, 23, 59, 59, 999));
    expect(localDayBoundary('', false)).toBeUndefined();
    expect(localDayBoundary('2026-02-31', true)).toBeUndefined();
  });

  it('bounds a start/end pair by each other, and an empty side sets no bound', () => {
    expect(dateRangeBounds('2026-10-03', '2026-10-10')).toEqual({ start: { max: '2026-10-10' }, end: { min: '2026-10-03' } });
    expect(dateRangeBounds('', '2026-10-10')).toEqual({ start: { max: '2026-10-10' }, end: { min: undefined } });
    expect(dateRangeBounds('2026-10-03', '')).toEqual({ start: { max: undefined }, end: { min: '2026-10-03' } });
  });

  it('parses only real calendar dates', () => {
    expect(parseDate('2026-10-10')).toEqual({ year: 2026, month: 10, day: 10 });
    expect(parseDate('2026-02-31')).toBeNull();
    expect(parseDate('')).toBeNull();
    expect(parseDate('2026-1-1')).toBeNull();
  });

  it('formats local dates without UTC shifting', () => {
    expect(formatLocalDate(new Date(2026, 0, 1, 0, 30))).toBe('2026-01-01');
    expect(formatDate(2026, 3, 5)).toBe('2026-03-05');
  });

  it('shifts months across year boundaries', () => {
    expect(addMonths(2026, 12, 1)).toEqual({ year: 2027, month: 1 });
    expect(addMonths(2026, 1, -1)).toEqual({ year: 2025, month: 12 });
    expect(addMonths(2026, 6, -18)).toEqual({ year: 2024, month: 12 });
  });

  it('builds a six week grid aligned to the week start', () => {
    // 2026-10-01 is a Thursday.
    const sunday = buildMonthGrid(2026, 10, 0);
    expect(sunday).toHaveLength(42);
    expect(sunday[0].value).toBe('2026-09-27');
    expect(sunday[4]).toMatchObject({ value: '2026-10-01', inMonth: true });
    const monday = buildMonthGrid(2026, 10, 1);
    expect(monday[0].value).toBe('2026-09-28');
    expect(monday.filter((day) => day.inMonth)).toHaveLength(31);
  });

  it('handles leap-day months', () => {
    const grid = buildMonthGrid(2028, 2, 1);
    expect(grid.filter((day) => day.inMonth)).toHaveLength(29);
  });
});
