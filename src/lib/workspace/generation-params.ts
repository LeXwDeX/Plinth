/**
 * What the composer's 参数 popover shows and what a turn is estimated to cost,
 * both read from the selected model's Offerings. Nothing here names a model or a
 * parameter: the size controls are the parameters with the output-size semantic,
 * and the rest are listed as declared.
 *
 * There is no image count control (#586): how many images a turn makes follows
 * what the person says, and the Agent sets the Offering's count parameter (an
 * advised, read-only parameter in the Schema) or submits several calls. The
 * estimate still multiplies by the count the request will carry
 * (estimated_credits_unit_parameter).
 */

import { offeringParamSchemas, offeringUsesNativePricing, type OfferingParameterWire, type OfferingWire } from '@/lib/agent-offerings';
import { formatSchemaText, parameterConditionMatches } from '@/lib/product-schema';
import type { ParamSchema } from '@/lib/providers/types';
import type { NativeQuote } from '@/lib/pricing';

/** Parameters the conversation or the stage fills; they never appear as settings. */
const CONVERSATION_SEMANTICS: ReadonlySet<string> = new Set(['prompt', 'reference-image', 'mask']);
const MAXIMUM_COUNT = 1000;

export interface GenerationParamsModel {
  /** Every parameter shown anywhere in the popover: defaults come from these. */
  paramSchemas: ParamSchema[];
  readonlyParams: OfferingParameterWire[];
  /** Aspect ratio and size controls (the output-size semantic). */
  sizeParams: ParamSchema[];
  /** Every other declared setting, listed under "more". */
  moreParams: ParamSchema[];
}

const EMPTY_MODEL: GenerationParamsModel = { readonlyParams: [], paramSchemas: [], sizeParams: [], moreParams: [] };

function semanticOf(offering: OfferingWire, name: string): string {
  return offering.parameters.find((item) => item.name === name)?.semantic ?? '';
}

/** Settings belong to one exact Offering; capability differences never form an intersection. */
export function buildGenerationParams(offerings: readonly OfferingWire[]): GenerationParamsModel {
  const first = offerings[0];
  if (!first) return EMPTY_MODEL;
  // The consent flag is not a setting: a step with extra credits asks on its approval card in the chat.
  const consentNames = new Set(offerings.map((offering) => offering.extra_credit_consent_parameter ?? '').filter(Boolean));
  const common = offeringParamSchemas(first).filter((param) =>
    !consentNames.has(param.name)
    && !CONVERSATION_SEMANTICS.has(semanticOf(first, param.name))
    && param.type !== 'asset');
  const sizeParams = common.filter((param) => semanticOf(first, param.name) === 'output-size');
  const placed = new Set(sizeParams.map((param) => param.name));
  return {
    paramSchemas: common,
    readonlyParams: first.parameters.filter((wire) => wire.presentation?.surface === 'readonly' && (wire.fixed === true || wire.adjustment_policy === 'advised') && wire.default !== undefined && !consentNames.has(wire.name) && !CONVERSATION_SEMANTICS.has(wire.semantic ?? '') && wire.type !== 'task' && wire.type !== 'asset' && wire.type !== 'asset-array'),
    sizeParams,
    moreParams: common.filter((param) => !placed.has(param.name)),
  };
}

/** The Offering a turn would run: one that takes a reference picture when there is one. */
export function offeringForTurn(offerings: readonly OfferingWire[], hasReference: boolean): OfferingWire | undefined {
  const takesReference = (offering: OfferingWire) => offering.parameters.some((wire) => wire.semantic === 'reference-image');
  return offerings.find((offering) => takesReference(offering) === hasReference) ?? offerings[0];
}

/** Applicable defaults and saved choices, with fixed and forced values enforced for display parity. */
export function effectiveOfferingValues(offering: OfferingWire | undefined, entered: Record<string, unknown>): Record<string, unknown> {
  if (!offering) return {};
  const result: Record<string, unknown> = {};
  for (const wire of offering.parameters) {
    if (wire.default !== undefined) result[wire.name] = wire.default;
    const value = entered[wire.name];
    if (!wire.fixed && value !== undefined && value !== null && value !== '') {
      const allowed = (entry: unknown) => !wire.options?.length || wire.options.some((option) => option.value === String(entry));
      if (Array.isArray(value) ? value.every(allowed) : allowed(value)) result[wire.name] = value;
    }
  }
  for (const wire of offering.parameters) {
    for (const rule of wire.forced_when ?? []) {
      if (parameterConditionMatches(rule.when, result)) { result[wire.name] = rule.value; break; }
    }
  }
  return result;
}

export function readonlyParameterText(wire: OfferingParameterWire, values: Record<string, unknown>, separator = ', '): string {
  const value = values[wire.name] ?? wire.default;
  const label = (entry: unknown) => wire.options?.find((option) => option.value === String(entry))?.label ?? String(entry ?? '');
  return Array.isArray(value) ? value.map(label).join(separator) : label(value);
}

export interface CreditEstimate {
  /** Price of one result, null when the Offering declares none or is not credits_metered. */
  unit: number | null;
  /** How many results the estimate multiplies by (1 when the model has no count). */
  count: number;
  /** The estimate shown on the send button, null when unknown. */
  total: number | null;
  /** The Schema marks the Offering credits_metered; without it no credits are shown at all. */
  metered: boolean;
  /**
   * Native prices are opaque server-provided previews, never derived from entered values here: the server's quote
   * of the saved parameters (#722, so a switch such as texture moves it) or else the Offering's default estimate.
   */
  nativeDefaultQuote?: OfferingWire['default_native_quote'];
  nativeUpperQuote?: OfferingWire['upper_native_quote'];
}

function valueOf(wire: OfferingParameterWire, values: Record<string, unknown>): unknown {
  return values[wire.name] ?? wire.default;
}

/**
 * Same rule as the server's estimate: the value (>= 1, at most 1000) times the unit price.
 * Only Offerings the Schema marks credits_metered show a price; others have no unit and total null.
 */
export function estimateCredits(offering: OfferingWire | undefined, entered: Record<string, unknown>, savedNativeQuote?: NativeQuote): CreditEstimate {
  if (offeringUsesNativePricing(offering)) {
    return { unit: null, count: 1, total: null, metered: false,
      nativeDefaultQuote: savedNativeQuote ?? offering?.default_native_quote, nativeUpperQuote: offering?.upper_native_quote };
  }
  const metered = offering?.credits_metered === true;
  const unit = metered && offering?.estimated_credits !== undefined && offering.estimated_credits > 0 ? offering.estimated_credits : null;
  if (!offering || unit === null) return { unit: null, count: 1, total: null, metered };
  const wire = offering.parameters.find((item) => item.name === offering.estimated_credits_unit_parameter);
  // What the request will carry: declared defaults under what the user entered.
  const values: Record<string, unknown> = { ...Object.fromEntries(offering.parameters.filter((item) => item.default !== undefined).map((item) => [item.name, item.default])) };
  for (const [name, value] of Object.entries(entered)) if (value !== undefined && value !== '') values[name] = value;
  if (!wire) return { unit, count: 1, total: unit, metered };
  // A count the request leaves out (omit_when) is a count of one.
  if ((wire.omit_when ?? []).some((condition) => parameterConditionMatches(condition, values))) return { unit, count: 1, total: unit, metered };
  const raw = Number(valueOf(wire, values));
  if (!Number.isFinite(raw) || raw < 1) return { unit, count: 1, total: unit, metered };
  const ceiling = Math.min(MAXIMUM_COUNT, wire.maximum ?? MAXIMUM_COUNT);
  const count = Math.min(ceiling, Math.floor(raw));
  return { unit, count, total: unit * count, metered };
}

/** The Skill's recommended size becomes the default when the model offers it as a size option. */
export function skillSizeDefault(sizeParams: readonly ParamSchema[], recommendedSize: string): { name: string; value: string } | null {
  if (!recommendedSize) return null;
  const match = sizeParams.find((param) => param.options?.some((option) => option.value === recommendedSize));
  return match ? { name: match.name, value: recommendedSize } : null;
}

function optionLabel(param: ParamSchema, value: unknown): string {
  const text = String(value ?? '');
  return param.options?.find((option) => option.value === text)?.label ?? text;
}

function gcd(left: number, right: number): number {
  return right === 0 ? left : gcd(right, left % right);
}

/** Ratios an artist names; a pixel size close to one of them reads as that ratio. */
const NAMED_RATIOS: ReadonlyArray<readonly [number, number]> = [[1, 1], [4, 3], [3, 4], [3, 2], [2, 3], [16, 9], [9, 16], [21, 9], [9, 21], [5, 4], [4, 5]];

/** "1024x1536" -> "2:3"; null when the text is not a pixel size. */
export function ratioOfSize(text: string): string | null {
  const match = /^\s*(\d+)\s*[x×]\s*(\d+)\s*$/i.exec(text);
  if (!match) return null;
  const width = Number(match[1]);
  const height = Number(match[2]);
  if (!(width > 0) || !(height > 0)) return null;
  // A size a little off a named ratio (1920x1088) is that ratio; anything else is reduced as it is.
  const near = NAMED_RATIOS.find(([w, h]) => Math.abs(width / height - w / h) / (w / h) < 0.02);
  if (near) return `${near[0]}:${near[1]}`;
  const divisor = gcd(width, height);
  return `${width / divisor}:${height / divisor}`;
}

/**
 * How a size option reads in the list: a pixel size also says its ratio, through the Schema
 * template ("{width}×{height} · {ratio}" -> "3840×2160 · 16:9"). "auto", a ratio ("16:9") or a
 * named size ("2K") keeps its own label, and so does an option the Offering words differently.
 */
export function sizeOptionText(option: { value: string; label: string }, template: string): string {
  const match = /^\s*(\d+)\s*[x×]\s*(\d+)\s*$/i.exec(option.value);
  const ratio = ratioOfSize(option.value);
  if (!match || !ratio || !template) return option.label;
  const plain = option.label.replace(/\s+/g, '').toLowerCase().replace('×', 'x') === `${match[1]}x${match[2]}`;
  if (!plain) return option.label;
  return formatSchemaText(template, { width: match[1], height: match[2], ratio });
}

export interface DeliverySize {
  /** The ratio of the picture the model is asked for, such as "3:2"; null when no size says one. */
  ratio: string | null;
  /** The exact pixels, when a size names them (1536x1024); null for a ratio or "auto". */
  pixels: { width: number; height: number } | null;
}

/**
 * What the chosen output-size settings ask the model for. A value without a ratio of its own
 * ("auto", "2K") falls back to the parameter's default; when that has none either, the next
 * size parameter decides, and with none at all both fields are null.
 */
export function deliverySize(sizeParams: readonly ParamSchema[], values: Record<string, unknown>): DeliverySize {
  let ratio: string | null = null;
  let pixels: DeliverySize['pixels'] = null;
  for (const param of sizeParams) {
    for (const candidate of [values[param.name], param.default]) {
      const text = String(candidate ?? '').trim();
      const found = ratioOfOption(text);
      if (!found) continue;
      ratio ??= found;
      const match = /^(\d+)\s*[x×]\s*(\d+)$/i.exec(text);
      if (match && !pixels) pixels = { width: Number(match[1]), height: Number(match[2]) };
      break;
    }
  }
  return { ratio, pixels };
}

/**
 * Size values the selected model does not offer, with what replaces each. The session keeps one
 * value per parameter name, so a size chosen for another model ("size": "1024x1024" for GPT
 * Image) can be stale for this one (Seedream offers 2K, 2048x2048, ...). The replacement keeps
 * the ratio when an option has it, else it is the parameter's default.
 */
export function staleSizeValues(sizeParams: readonly ParamSchema[], values: Record<string, unknown>): Record<string, unknown> {
  const replacements: Record<string, unknown> = {};
  for (const param of sizeParams) {
    const options = param.options ?? [];
    const value = values[param.name];
    if (options.length === 0 || value === undefined || value === null || value === '') continue;
    if (options.some((option) => option.value === String(value))) continue;
    const ratio = ratioOfOption(String(value));
    const sameRatio = ratio ? options.find((option) => ratioOfOption(option.value) === ratio) : undefined;
    // A default outside the options would be stale too and loop; fall back to the first option.
    const fallback = options.some((option) => option.value === String(param.default)) ? param.default : options[0].value;
    replacements[param.name] = sameRatio?.value ?? fallback;
  }
  return replacements;
}

/** The ratio an option stands for: a ratio option is itself, a pixel size is derived. */
function ratioOfOption(value: string): string | null {
  return /^\d+:\d+$/.test(value) ? value : ratioOfSize(value);
}

/** A ratio chosen on the 参数 chip, such as "1:1", or a size as the Offering words it. */
function sizeChipText(param: ParamSchema, value: unknown): string {
  const label = optionLabel(param, value);
  const ratio = ratioOfOption(String(value ?? ''));
  // "auto" and named sizes ("2K") have no ratio of their own.
  return ratio && ratio !== label ? `${ratio} · ${label}` : label;
}

/**
 * What the 参数 chip says on its second line: every size as a ratio (with the pixel size
 * when the model words it that way).
 */
export function paramsChipText(model: Pick<GenerationParamsModel, 'sizeParams'>, values: Record<string, unknown>): string {
  return model.sizeParams.map((param) => sizeChipText(param, values[param.name] ?? param.default)).filter(Boolean).join(' · ');
}

/**
 * What the compact 参数 button says: the image ratio ("1:1"). A size that has no ratio of its own
 * ("auto", "2K") falls back to the option's label; empty when the model has no size parameter.
 */
export function paramsRatioText(
  model: Pick<GenerationParamsModel, 'sizeParams'>,
  values: Record<string, unknown>,
): string {
  const entries = model.sizeParams.map((param) => ({ param, value: values[param.name] ?? param.default }));
  for (const { value } of entries) {
    const ratio = ratioOfOption(String(value ?? ''));
    if (ratio) return ratio;
  }
  return entries.length > 0 ? optionLabel(entries[0].param, entries[0].value) : '';
}
