/**
 * 原生 Offering 目录线协议（GET /api/v1/offerings，路由由 Product Schema
 * runtime.http.routes.offerings 声明）。harness 面板据此渲染所选 Offering
 * 的请求参数，并映射为 SchemaParameters 可消费的 ParamSchema。
 */

import type { ModelInputLimits, ProductSchema } from './product-schema';
import type { NativeQuote } from './pricing';
import type {
  ParamCondition,
  ParamForcedValue,
  ParamNotice,
  ParamOption,
  ParamSchema,
} from './providers/types';

/** Schema `image_size`: the bounds of a custom width×height output size (internal/schema ImageSizeConstraint). */
export interface ImageSizeConstraintWire {
  min_pixels: number;
  max_pixels: number;
  max_edge?: number;
  multiple?: number;
  max_ratio: number;
}

export interface OfferingParameterWire {
  name: string;
  label: string;
  type: string;
  required?: boolean;
  default?: unknown;
  options?: ParamOption[];
  minimum?: number;
  maximum?: number;
  max_length?: number;
  min_items?: number;
  max_items?: number;
  media_types?: string[];
  extensions?: string[];
  max_bytes?: number;
  uploaded_model?: { native_only?: boolean; profile_id: string; profile_revision?: string; profile_limits?: ModelInputLimits; require_textures?: boolean;
    require_single_base_color_texture?: boolean; requirements?: string };
  task_capabilities?: string[];
  task_source_kind?: 'upstream_job' | 'assets';
  /** Schema: past this age (seconds since the source Task was created) the Provider no longer keeps the referenced job (#325). */
  upstream_job_max_age_seconds?: number;
  task_offerings?: string[];
  same_provider?: boolean;
  integer?: boolean;
  /** The custom pixel sizes the parameter accepts besides its options (#272: the canvas output-size rule reads it). */
  image_size?: ImageSizeConstraintWire;
  description?: string;
  omit_when?: ParamCondition[];
  forced_when?: ParamForcedValue[];
  presentation?: {
    surface?: string;
    control?: string;
    step?: number;
    disabled_when?: ParamCondition[];
    visible_when?: ParamCondition[];
    notices?: ParamNotice[];
  };
  semantic?: string;
  /** Schema: the backend always sets this parameter to its default; no one chooses it. */
  fixed?: boolean;
  adjustment_policy?: 'advised';
  adjustment_reason?: string;
}

export interface OfferingWire {
  id: string;
  revision: string;
  provider_id: string;
  provider_name: string;
  /** The vendor's short name declared by the Product Schema (such as "GPT"), for compact controls. */
  provider_short_label?: string;
  /** Schema `provider_order`: the vendor's rank in the model tables (ascending); absent when undeclared. */
  provider_order?: number;
  model_id: string;
  model_name: string;
  capability: string;
  available: boolean;
  /** Native provider pricing is a separate contract; legacy credit fields are never used for it. */
  price_mode?: 'legacy' | 'native';
  default_native_quote?: NativeQuote;
  upper_native_quote?: NativeQuote;
  recommended?: boolean;
  priority?: number;
  estimated_credits?: number;
  /** 该 Offering 的 Provider 按积分计费（Product Schema 声明）；缺省则不向用户展示积分。 */
  credits_metered?: boolean;
  /** 按该数值参数的值乘算预计积分（如生成数量 n）；未声明则按次计价。 */
  estimated_credits_unit_parameter?: string;
  /** Empty/omitted preserves legacy selection; preserve forbids view rewriting and truncation. */
  reference_selection_policy?: 'preserve';
  /** 存在可选计费选项时服务端派生的最高估算；不含任何成本声明。 */
  maximum_estimated_credits?: number;
  /** 承载"同意额外积分消耗"的布尔参数名；只公开参数名，不公开金额。 */
  extra_credit_consent_parameter?: string;
  parameters: OfferingParameterWire[];
  input_rules?: Array<{ parameters: string[]; minimum: number; maximum: number }>;
  outputs?: unknown[];
}

/** What stands where credits would be for an Offering whose provider does not bill in credits. */
export const NO_CREDITS = '/';

/** The Schema marks this Offering's provider as billing in credits; without it no credits are shown. */
export function offeringShowsCredits(offering: Pick<OfferingWire, 'credits_metered'>): boolean {
  return offering.credits_metered === true;
}

export function offeringUsesNativePricing(offering: Pick<OfferingWire, 'price_mode'> | undefined): boolean {
  return offering?.price_mode === 'native';
}

const RENDERABLE_TYPES = new Set<ParamSchema['type']>([
  'select',
  'text',
  'boolean',
  'number',
  'slider',
  'asset',
  'string-array',
  'multi-select',
]);

function mapParameterType(wire: OfferingParameterWire): ParamSchema['type'] {
  if (wire.type === 'asset-array') return 'asset';
  return RENDERABLE_TYPES.has(wire.type as ParamSchema['type'])
    ? (wire.type as ParamSchema['type'])
    : 'text';
}

function buildValidation(
  wire: OfferingParameterWire,
): ParamSchema['validation'] {
  const validation: NonNullable<ParamSchema['validation']> = {
    type: wire.type,
  };
  if (wire.max_length !== undefined) validation.max_length = wire.max_length;
  if (wire.max_bytes !== undefined) validation.max_bytes = wire.max_bytes;
  if (wire.media_types !== undefined) validation.media_types = wire.media_types;
  if (wire.min_items !== undefined) validation.min_items = wire.min_items;
  if (wire.max_items !== undefined) validation.max_items = wire.max_items;
  if (wire.integer !== undefined) validation.integer = wire.integer;
  return validation;
}

/**
 * A parameter nobody chooses: only the Schema `fixed` flag decides it.
 * Returns its fixed value, or undefined when the parameter is a real choice.
 */
export function fixedParameterValue(wire: OfferingParameterWire): { value: unknown } | undefined {
  if (wire.fixed !== true || wire.default === undefined) return undefined;
  return { value: wire.default };
}

/** Values of the parameters no form shows; every request still carries them. */
export function offeringFixedValues(offering: Pick<OfferingWire, 'parameters'>): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const wire of offering.parameters ?? []) {
    const fixed = fixedParameterValue(wire);
    if (fixed) result[wire.name] = fixed.value;
  }
  return result;
}

/**
 * The options a person can actually pick: an option the parameter's own forced_when always replaces (when the
 * parameter equals that option) never takes effect, so it is not offered. Seedream 4.5 keeps its low tiers in
 * the Schema and forces each to the same ratio's highest one (#267); listing them made the picker show
 * 2048x2048 while the canvas said the model outputs 4096x4096. The Schema and server keep every declaration.
 */
export function selectableOptions(wire: OfferingParameterWire): NonNullable<OfferingParameterWire['options']> {
  const forcedAway = new Set((wire.forced_when ?? [])
    .filter((rule) => rule.when.parameter === wire.name && rule.when.operator === 'equals' && String(rule.when.value) !== String(rule.value))
    .map((rule) => String(rule.when.value)));
  return (wire.options ?? []).filter((option) => !forcedAway.has(String(option.value)));
}

/**
 * Offering 线参数 → 前端渲染 ParamSchema。
 * presentation.surface=hidden、task 引用参数与固定值参数（见
 * fixedParameterValue）不进入表单（由服务端 / Agent 工具裁决）。
 */
export function offeringParamSchemas(
  offering: OfferingWire,
): ParamSchema[] {
  const result: ParamSchema[] = [];
  for (const wire of offering.parameters ?? []) {
    const surface = wire.presentation?.surface ?? 'provider';
    if (surface === 'hidden' || surface === 'readonly' || wire.type === 'task' || fixedParameterValue(wire)) continue;
    const schema: ParamSchema = {
      name: wire.name,
      label: wire.label,
      type: mapParameterType(wire),
      validation: buildValidation(wire),
    };
    if (surface === 'canvas') schema.surface = 'canvas';
    if (wire.options !== undefined) schema.options = selectableOptions(wire);
    if (wire.default !== undefined) schema.default = wire.default;
    if (wire.required !== undefined) schema.required = wire.required;
    if (wire.minimum !== undefined) schema.min = wire.minimum;
    if (wire.maximum !== undefined) schema.max = wire.maximum;
    if (wire.presentation?.step !== undefined) {
      schema.step = wire.presentation.step;
    }
    if (wire.description !== undefined) schema.description = wire.description;
    if (wire.presentation?.disabled_when !== undefined) {
      schema.disabled_when = wire.presentation.disabled_when;
    }
    if (wire.presentation?.visible_when !== undefined) {
      schema.visible_when = wire.presentation.visible_when;
    }
    if (wire.presentation?.notices !== undefined) {
      schema.notices = wire.presentation.notices;
    }
    if (wire.forced_when !== undefined) schema.forced_when = wire.forced_when;
    result.push(schema);
  }
  return result;
}

/** 默认选中：优先推荐的可用 Offering，其次第一个可用 Offering。 */
export function pickDefaultOffering(
  offerings: OfferingWire[],
): OfferingWire | null {
  const available = offerings.filter((item) => item.available);
  if (available.length === 0) return null;
  return available.find((item) => item.recommended) ?? available[0];
}

export async function fetchCapabilityOfferings(
  schema: ProductSchema,
  capability: string,
  fetcher: typeof fetch = fetch,
  signal?: AbortSignal,
): Promise<OfferingWire[]> {
  const route = schema.runtime.http.routes.offerings;
  const url = `${route}?capability=${encodeURIComponent(capability)}`;
  const response = await fetcher(url, { signal });
  if (!response.ok) {
    throw new Error(`offerings ${response.status}`);
  }
  const payload: unknown = await response.json();
  const offerings =
    payload !== null && typeof payload === 'object' && !Array.isArray(payload)
      ? (payload as Record<string, unknown>).offerings
      : undefined;
  return Array.isArray(offerings) ? (offerings as OfferingWire[]) : [];
}
