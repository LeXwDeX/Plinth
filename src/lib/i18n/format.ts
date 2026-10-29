/**
 * Locale-aware formatting. Callers pass `activeLocale(schema)` (or a Schema
 * field the server already set to it) so dates and numbers follow the UI
 * language instead of the browser's or a hard-coded one.
 */

function toDate(value: string | number | Date): Date {
  return value instanceof Date ? value : new Date(value);
}

/** Date and time, or '' for a value that is not a date. */
export function formatDateTime(value: string | number | Date, locale: string, options?: Intl.DateTimeFormatOptions): string {
  const date = toDate(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString(locale, options) : '';
}

/** Time of day only, or '' for a value that is not a date. */
export function formatTime(value: string | number | Date, locale: string): string {
  const date = toDate(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleTimeString(locale) : '';
}

export function formatNumber(value: number, locale: string, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale, options).format(value);
}

/** A provider balance in points: grouped by the locale, never rounded. Top bar and administration overview share it. */
export function formatBalanceAmount(amount: number, locale: string): string {
  return formatNumber(amount, locale, { maximumFractionDigits: 20 });
}
