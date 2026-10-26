/** Exact, display-only parsing for server-frozen provider-native prices. */
export interface NativeQuoteComponent {
  id: string;
  label: string;
  amount: string;
}

export interface NativeQuote {
  version: 1;
  unit: string;
  unit_label: string;
  base_amount: string;
  extra_amount: string;
  total_amount: string;
  components?: NativeQuoteComponent[];
}

export interface PriceTotal {
  unit: string;
  unit_label: string;
  amount: string;
}

const SCALE = BigInt(1_000_000);
const MAX_QUOTE_AMOUNT = BigInt(1_000_000_000) * SCALE;
const MAX_AGGREGATE_AMOUNT = BigInt('9223372036854775807');
const CANONICAL_DECIMAL = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,6}))?$/;

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function amountUnits(value: unknown, maximum = MAX_AGGREGATE_AMOUNT): bigint | null {
  if (typeof value !== 'string') return null;
  const match = CANONICAL_DECIMAL.exec(value);
  if (!match) return null;
  if (match[1].length > 13 || value.length > 20) return null;
  const whole = BigInt(match[1]);
  const fraction = BigInt((match[2] ?? '').padEnd(6, '0') || '0');
  const units = whole * SCALE + fraction;
  if (units > maximum || canonicalUnits(units) !== value) return null;
  return units;
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

function canonicalUnits(units: bigint): string {
  const whole = (units / SCALE).toString();
  const fraction = (units % SCALE).toString().padStart(6, '0').replace(/0+$/, '');
  return fraction ? `${whole}.${fraction}` : whole;
}

function formatUnits(units: bigint): string {
  const [whole, fraction] = canonicalUnits(units).split('.');
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return fraction ? `${grouped}.${fraction}` : grouped;
}

/** Returns null for numbers, non-canonical strings, and values outside the quote bound. */
export function formatNativeAmount(value: unknown): string | null {
  const units = amountUnits(value, MAX_QUOTE_AMOUNT);
  return units === null ? null : formatUnits(units);
}

export function formatNativePrice(value: unknown, unitLabel: unknown): string | null {
  const amount = formatNativeAmount(value);
  if (amount === null || typeof unitLabel !== 'string' || unitLabel.trim() === '') return null;
  return `${amount} ${unitLabel}`;
}

/** Validate the complete immutable quote, including its checked sums and line items. */
export function parseNativeQuote(value: unknown): NativeQuote | null {
  const wire = record(value);
  if (!wire || wire.version !== 1 || typeof wire.unit !== 'string' || !/^[a-z][a-z0-9._-]{0,63}$/.test(wire.unit)
    || wire.unit === 'legacy.credits'
    || typeof wire.unit_label !== 'string' || !wire.unit_label.trim() || utf8Length(wire.unit_label) > 128) return null;

  const base = amountUnits(wire.base_amount, MAX_QUOTE_AMOUNT);
  const extra = amountUnits(wire.extra_amount, MAX_QUOTE_AMOUNT);
  const total = amountUnits(wire.total_amount, MAX_QUOTE_AMOUNT);
  if (base === null || extra === null || total === null || base + extra !== total) return null;

  const components: NativeQuoteComponent[] = [];
  if (wire.components !== undefined) {
    if (!Array.isArray(wire.components) || wire.components.length > 8) return null;
    let previousId = '';
    let sum = BigInt(0);
    for (const raw of wire.components) {
      const item = record(raw);
      if (!item || typeof item.id !== 'string' || !/^[a-z][a-z0-9_-]{0,63}$/.test(item.id) || item.id <= previousId
        || typeof item.label !== 'string' || !item.label.trim() || utf8Length(item.label) > 256) return null;
      const amount = amountUnits(item.amount, MAX_QUOTE_AMOUNT);
      if (amount === null || amount === BigInt(0)) return null;
      previousId = item.id;
      sum += amount;
      components.push({ id: item.id, label: item.label, amount: item.amount as string });
    }
    if (sum !== extra) return null;
  } else if (extra !== BigInt(0)) {
    return null;
  }

  return {
    version: 1,
    unit: wire.unit,
    unit_label: wire.unit_label,
    base_amount: wire.base_amount as string,
    extra_amount: wire.extra_amount as string,
    total_amount: wire.total_amount as string,
    ...(components.length ? { components } : {}),
  };
}

/** Validates server-provided unit aggregates without recomputing prices from tasks. */
export function parsePriceTotals(value: unknown): PriceTotal[] | null {
  if (!Array.isArray(value)) return null;
  const seen = new Set<string>();
  const totals: PriceTotal[] = [];
  for (const raw of value) {
    const item = record(raw);
    if (!item || typeof item.unit !== 'string' || !/^[a-z][a-z0-9._-]{0,63}$/.test(item.unit)
      || seen.has(item.unit) || typeof item.unit_label !== 'string' || !item.unit_label.trim() || utf8Length(item.unit_label) > 128
      || (item.unit === 'legacy.credits' && typeof item.amount === 'string' && !/^(0|[1-9][0-9]*)$/.test(item.amount))
      || amountUnits(item.amount) === null) return null;
    seen.add(item.unit);
    totals.push({ unit: item.unit, unit_label: item.unit_label, amount: item.amount as string });
  }
  return totals;
}

export function formatPriceTotal(total: PriceTotal): string | null {
  const amount = amountUnits(total.amount);
  return amount === null ? null : `${formatUnits(amount)} ${total.unit_label}`;
}

