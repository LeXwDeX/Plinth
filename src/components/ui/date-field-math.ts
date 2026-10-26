/** Date helpers for DateField. Everything uses local calendar dates; never Date.parse / toISOString, which shift by UTC offset. */

export interface CalendarDay {
  /** 'YYYY-MM-DD' */
  value: string;
  day: number;
  inMonth: boolean;
}

const pad = (value: number, width = 2) => String(value).padStart(width, '0');

export function formatDate(year: number, month: number, day: number): string {
  return `${pad(year, 4)}-${pad(month)}-${pad(day)}`;
}

export function formatLocalDate(date: Date): string {
  return formatDate(date.getFullYear(), date.getMonth() + 1, date.getDate());
}

/** Parses a strict 'YYYY-MM-DD'; returns null for empty or impossible dates (2026-02-31). */
export function parseDate(value: string): { year: number; month: number; day: number } | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const check = new Date(year, month - 1, day);
  if (check.getFullYear() !== year || check.getMonth() !== month - 1 || check.getDate() !== day) return null;
  return { year, month, day };
}

/** Shifts a (year, month 1-12) pair by whole months. */
export function addMonths(year: number, month: number, delta: number): { year: number; month: number } {
  const index = year * 12 + (month - 1) + delta;
  return { year: Math.floor(index / 12), month: (index % 12 + 12) % 12 + 1 };
}

/** The 6-week grid for a month, starting on `weekStart` (0 = Sunday, 1 = Monday). */
export function buildMonthGrid(year: number, month: number, weekStart = 0): CalendarDay[] {
  const first = new Date(year, month - 1, 1);
  const lead = (first.getDay() - weekStart + 7) % 7;
  const days: CalendarDay[] = [];
  for (let offset = 0; offset < 42; offset += 1) {
    const date = new Date(year, month - 1, 1 - lead + offset);
    days.push({
      value: formatLocalDate(date),
      day: date.getDate(),
      inMonth: date.getMonth() === month - 1,
    });
  }
  return days;
}

/**
 * The bounds a start/end pair of date fields give each other, so a range can never end before
 * it starts. An empty side sets no bound.
 */
export function dateRangeBounds(start: string, end: string): { start: { max?: string }; end: { min?: string } } {
  return { start: { max: end || undefined }, end: { min: start || undefined } };
}

/**
 * The first (or last) moment of a 'YYYY-MM-DD' day in local time, for filters that send
 * timestamps. undefined for an empty or impossible date.
 */
export function localDayBoundary(value: string, endOfDay: boolean): Date | undefined {
  const parsed = parseDate(value);
  if (!parsed) return undefined;
  return endOfDay
    ? new Date(parsed.year, parsed.month - 1, parsed.day, 23, 59, 59, 999)
    : new Date(parsed.year, parsed.month - 1, parsed.day);
}
