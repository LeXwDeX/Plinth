/**
 * The one way a byte count is written (#327), mirroring Go `schema.SizeText`: decimal MB (1 MB = 1,000,000 bytes;
 * KB of 1,000 bytes below 1 MB), at most two decimals, no trailing zeros, always with the unit. A size rounds up and a
 * limit down, so a file over its limit never reads as equal to it. Schema limits, the page and the Agent's tool
 * results use it, so the Agent quotes the same text the page shows.
 */
export function sizeText(bytes: number, roundUp: boolean): string {
  const value = Number.isFinite(bytes) && bytes > 0 ? Math.floor(bytes) : 0;
  const [unit, scale] = value < 1_000_000 ? [' KB', 1_000] : [' MB', 1_000_000];
  let hundredths = Math.floor((value * 100) / scale);
  if (roundUp && (value * 100) % scale !== 0) hundredths += 1;
  const whole = Math.floor(hundredths / 100);
  const fraction = hundredths % 100;
  return `${whole}${fraction ? `.${String(fraction).padStart(2, '0').replace(/0+$/, '')}` : ''}${unit}`;
}

/** A file's size as listed: rounded up, like a size in a refusal. */
export function formatFileSize(bytes: number): string {
  return sizeText(bytes, true);
}

/** A limit as stated: rounded down. */
export function formatSizeLimit(bytes: number): string {
  return sizeText(bytes, false);
}
