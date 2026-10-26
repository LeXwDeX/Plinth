/**
 * Custom colour entry (P3): a hexadecimal field and red / green / blue channels, limited to the Schema's
 * rgb_minimum..rgb_maximum range. Pure parsing and formatting.
 */
export interface Rgb {
  r: number;
  g: number;
  b: number;
}

export interface RgbRange {
  rgb_minimum: number;
  rgb_maximum: number;
}

/** "#f80", "f80", "#ff8800" or "FF8800" as "#ff8800"; null when it is not a colour. */
export function normalizeHex(input: string): string | null {
  const text = input.trim().replace(/^#/, '');
  if (/^[0-9a-fA-F]{3}$/.test(text)) {
    return `#${text.split('').map((digit) => digit + digit).join('').toLowerCase()}`;
  }
  return /^[0-9a-fA-F]{6}$/.test(text) ? `#${text.toLowerCase()}` : null;
}

export function hexToRgb(hex: string): Rgb | null {
  const normal = normalizeHex(hex);
  if (!normal) return null;
  return { r: parseInt(normal.slice(1, 3), 16), g: parseInt(normal.slice(3, 5), 16), b: parseInt(normal.slice(5, 7), 16) };
}

export function clampChannel(value: number, range: RgbRange): number {
  if (!Number.isFinite(value)) return range.rgb_minimum;
  return Math.min(range.rgb_maximum, Math.max(range.rgb_minimum, Math.round(value)));
}

/** The hex of channel values, each clamped to the Schema range (the range is 0..255 for a hex colour). */
export function rgbToHex(rgb: Rgb, range: RgbRange): string {
  const part = (value: number) => Math.min(255, clampChannel(value, range)).toString(16).padStart(2, '0');
  return `#${part(rgb.r)}${part(rgb.g)}${part(rgb.b)}`;
}
