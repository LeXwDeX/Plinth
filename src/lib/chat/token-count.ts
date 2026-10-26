/**
 * Token counts as the composer shows them (#322): the raw number under 1000, then K and M with one
 * decimal below 100 of the unit ("12.3K", "1.2M") and a rounded integer from 100 on ("356K"). A
 * trailing ".0" is dropped. Integer arithmetic keeps the rounding exact at the boundaries.
 */
export function formatTokenCount(value: number): string {
  const count = Number.isFinite(value) ? Math.max(0, value) : 0;
  if (count < 1_000) return String(Math.round(count));
  if (count < 1_000_000) {
    const tenths = Math.round(count / 100);
    if (tenths < 1_000) return `${tenthsText(tenths)}K`;
    const thousands = Math.round(count / 1_000);
    if (thousands < 1_000) return `${thousands}K`;
  }
  const tenths = Math.round(count / 100_000);
  if (tenths < 1_000) return `${tenthsText(tenths)}M`;
  return `${Math.round(count / 1_000_000)}M`;
}

function tenthsText(tenths: number): string {
  const whole = Math.floor(tenths / 10);
  const fraction = tenths % 10;
  return fraction === 0 ? String(whole) : `${whole}.${fraction}`;
}

/**
 * The session's context usage: "used / window", only "used" when no window is declared, and the
 * unknown mark in place of "used" when the upstream did not report it. Nothing is estimated.
 */
export function formatContextUsage(used: number | null, window: number | null, unknownText: string): string {
  const usedText = used === null ? unknownText : formatTokenCount(used);
  return window === null || window <= 0 ? usedText : `${usedText} / ${formatTokenCount(window)}`;
}
