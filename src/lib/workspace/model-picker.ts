import type { OfferingWire } from '@/lib/agent-offerings';
import { formatNativeAmount, parseNativeQuote } from '@/lib/pricing';
import { formatSchemaText, parameterConditionMatches } from '@/lib/product-schema';
import type { CanvasPixels } from '@/lib/canvas/artboard';
import { canvasSizeChoice, canvasSizeParameters } from '@/lib/workspace/canvas-size';
import { effectiveOfferingValues } from '@/lib/workspace/generation-params';
import type { WorkspaceDomain } from '@/store/workspace-store';

/**
 * 模型选择小表格的行：每个"供应商 + 模型"一行，列出它在这个域里能做什么、
 * 多少积分。数据只来自目录里的 Offering（可用性、能力、参数、估算积分），
 * 不按模型名推断；没有可用 Offering 的模型保留一行但不可选。
 */

export const IMAGE_TO_IMAGE = 'image-to-image';
export const TEXT_TO_IMAGE = 'text-to-image';
export const TEXT_TO_3D = 'text-to-3d';
export const IMAGE_TO_3D = 'image-to-3d';
export const RETEXTURE = 'retexture';

/** Inpaint is usable only when all three inputs have explicit, compatible semantics. */
export function inpaintParameters(offering: OfferingWire) {
  const prompt = offering.parameters.find((parameter) => parameter.semantic === 'prompt' && parameter.type === 'text');
  const reference = offering.parameters.find((parameter) => parameter.semantic === 'reference-image' && ['asset', 'asset-array'].includes(parameter.type));
  const mask = offering.parameters.find((parameter) => parameter.semantic === 'mask' && parameter.type === 'asset');
  return prompt && reference && mask ? { prompt, reference, mask } : null;
}

/**
 * The inpaint inputs plus the explicit output size when the canvas has one (#308). Without a size the
 * Offering falls back to its own default (GPT Image is 2048x2048), which cannot be put back on a
 * differently shaped canvas without stretching. The rule is the same one a canvas turn uses: every
 * output-size parameter a person may choose gets the value closest to the canvas, then the Offering's
 * own forced size applies. A ratio-only model sends the ratio; its pixels are read from the Schema tables.
 */
export function buildInpaintInput(
  offering: OfferingWire,
  instruction: string,
  referenceId: string,
  maskId: string,
  canvas?: CanvasPixels | null,
): Record<string, unknown> {
  const parameters = inpaintParameters(offering);
  if (!parameters) throw new Error('inpaint semantics unavailable');
  const input: Record<string, unknown> = {
    [parameters.prompt.name]: instruction.trim(),
    [parameters.reference.name]: parameters.reference.type === 'asset-array' ? [referenceId] : referenceId,
    [parameters.mask.name]: maskId,
  };
  if (!canvas || canvas.width <= 0 || canvas.height <= 0) return input;
  const sizeParameters = canvasSizeParameters(offering);
  for (const parameter of sizeParameters) {
    const choice = canvasSizeChoice(parameter, canvas);
    if (choice !== null) input[parameter.name] = choice;
  }
  const sizeNames = new Set(sizeParameters.map((parameter) => parameter.name));
  // The Offering's defaults decide conditions the same way the server's normalization does; the size
  // chosen for the canvas above still wins over its default.
  const conditions = { ...effectiveOfferingValues(offering, {}), ...input };
  for (const wire of offering.parameters ?? []) {
    if (!sizeNames.has(wire.name)) continue;
    for (const rule of wire.forced_when ?? []) {
      if (parameterConditionMatches(rule.when, conditions)) { input[wire.name] = rule.value; break; }
    }
  }
  return input;
}

export type PickerCellValue =
  | { kind: 'tick'; on: boolean }
  | { kind: 'credits'; credits: number | null; metered: boolean }
  | { kind: 'native-price'; price: string | null };

export interface ModelPickerRow {
  modelId: string;
  providerId: string;
  name: string;
  provider: string;
  /** The vendor short name from the Schema ("GPT"); empty when the Offering declares none. */
  shortLabel: string;
  /** Schema `provider_order`: the vendor's rank in the table (0 when the Offering declares none). */
  providerOrder: number;
  enabled: boolean;
  recommended: boolean;
  cells: PickerCellValue[];
}

export interface ModelPickerTable {
  rows: ModelPickerRow[];
  /** 声明了蒙版参数的模型名（仅 2D）。 */
  maskModels: string[];
  /** 提供换贴图的供应商名（仅 3D）。 */
  retextureProviders: string[];
}

/**
 * 一列积分单元格：是否计积分由 Schema 的 credits_metered 声明；未声明则 credits 恒为 null，
 * 界面显示"/"。已计积分但没有估算的显示"—"。
 */
function creditsCell(
  columnOfferings: readonly OfferingWire[],
  metered: boolean,
): PickerCellValue {
  return { kind: 'credits', credits: metered ? leastCredits(columnOfferings) : null, metered };
}

function priceCell(offerings: readonly OfferingWire[], legacyMetered: boolean): PickerCellValue {
  const native = offerings.filter((offering) => offering.price_mode === 'native');
  if (native.length === 0) return creditsCell(offerings, legacyMetered);
  const quotes = native.map((offering) => parseNativeQuote(offering.default_native_quote));
  if (quotes.some((quote) => quote === null)) return { kind: 'native-price', price: null };
  const first = quotes[0]!;
  if (quotes.some((quote) => quote!.unit !== first.unit || quote!.unit_label !== first.unit_label || quote!.total_amount !== first.total_amount)) {
    return { kind: 'native-price', price: null };
  }
  // 列表只显示数值：单位名称会把窄列截断成 "40 M…"（#235），金额本身不变。
  return { kind: 'native-price', price: formatNativeAmount(first.total_amount) };
}

function leastCredits(offerings: readonly OfferingWire[]): number | null {
  const values = offerings.flatMap((offering) =>
    typeof offering.estimated_credits === 'number' ? [offering.estimated_credits] : []);
  return values.length > 0 ? Math.min(...values) : null;
}

/**
 * @param offerings 该域各能力的 Offering（含不可用的）。
 * @param retexture 换贴图能力的 Offering；只用于 3D 的"换贴图"列，为空时该列全为"—"。
 */
export function buildModelPickerTable(
  domain: WorkspaceDomain,
  offerings: Record<string, OfferingWire[]> | null | undefined,
  retexture: readonly OfferingWire[] | null | undefined,
): ModelPickerTable {
  const grouped = new Map<string, OfferingWire[]>();
  const order: string[] = [];
  for (const list of Object.values(offerings ?? {})) {
    for (const offering of list) {
      const key = JSON.stringify([offering.provider_id, offering.model_id]);
      if (!grouped.has(key)) { grouped.set(key, []); order.push(key); }
      const bucket = grouped.get(key)!;
      if (!bucket.some((entry) => entry.id === offering.id)) bucket.push(offering);
    }
  }
  const retextureProviders = new Set(
    (retexture ?? []).filter((offering) => offering.available).map((offering) => offering.provider_id),
  );
  const providerNames = new Map((retexture ?? []).map((offering) => [offering.provider_id, offering.provider_name]));

  const rows: ModelPickerRow[] = order.map((key) => {
    const group = grouped.get(key)!;
    const first = group[0];
    const live = group.filter((offering) => offering.available);
    const ofCapability = (capability: string) => live.filter((offering) => offering.capability === capability);
    const masked = ofCapability(IMAGE_TO_IMAGE).some((offering) => inpaintParameters(offering) !== null);
    const meteredSource = live.length > 0 ? live : group;
    const metered = meteredSource.some((offering) => offering.credits_metered === true);
    const cells: PickerCellValue[] = domain === 'image'
      ? [
          { kind: 'tick', on: ofCapability(IMAGE_TO_IMAGE).length > 0 },
          { kind: 'tick', on: masked },
          priceCell([...ofCapability(TEXT_TO_IMAGE), ...ofCapability(IMAGE_TO_IMAGE)], metered),
        ]
      : [
          priceCell(ofCapability(TEXT_TO_3D), metered),
          priceCell(ofCapability(IMAGE_TO_3D), metered),
          { kind: 'tick', on: live.length > 0 && retextureProviders.has(first.provider_id) },
        ];
    return {
      modelId: first.model_id, providerId: first.provider_id, name: first.model_name, provider: first.provider_name,
      shortLabel: first.provider_short_label ?? '',
      providerOrder: first.provider_order ?? 0,
      enabled: live.length > 0, recommended: live.some((offering) => offering.recommended === true), cells,
    };
  });

  // Grouped by vendor in the Schema's `provider_order` (undeclared vendors last, in catalog order); inside
  // a vendor the available models come first and the catalog order is kept, so dimmed rows sink.
  const rank = (row: ModelPickerRow) => (row.providerOrder > 0 ? row.providerOrder : Number.MAX_SAFE_INTEGER);
  const sorted = rows
    .map((row, index) => ({ row, index }))
    .sort((left, right) => rank(left.row) - rank(right.row)
      || Number(right.row.enabled) - Number(left.row.enabled)
      || left.index - right.index)
    .map((entry) => entry.row);
  const maskModels = domain === 'image' ? sorted.filter((row) => row.enabled && row.cells[1].kind === 'tick' && row.cells[1].on).map((row) => row.name) : [];
  const retextureNames = domain === 'model-3d'
    ? [...retextureProviders].map((id) => providerNames.get(id) ?? id)
    : [];
  return { rows: sorted, maskModels, retextureProviders: retextureNames };
}

export interface PickerTriggerTemplates {
  /** Schema `ui.studio.picker.short_with_provider`: the model name led by the vendor short name. */
  shortWithProvider: string;
  /** Schema `ui.studio.picker.full_with_provider`: the model name and its provider, for the hover. */
  fullWithProvider: string;
}

/**
 * What the picker trigger shows for the chosen row (#324): the model's own name, so two models of one
 * vendor do not both read "MESHY". The vendor short name joins it only when another row of the same table
 * has that model name; the hover always names model and provider. The trigger cuts a long name itself.
 */
export function pickerTriggerNames(
  row: ModelPickerRow,
  rows: readonly ModelPickerRow[],
  templates: PickerTriggerTemplates,
): { short: string; full: string } {
  const shared = rows.some((other) => other !== row && other.name === row.name);
  const vendor = row.shortLabel || row.provider;
  return {
    short: shared && vendor ? formatSchemaText(templates.shortWithProvider, { provider: vendor, model: row.name }) : row.name,
    full: row.provider ? formatSchemaText(templates.fullWithProvider, { provider: row.provider, model: row.name }) : row.name,
  };
}
