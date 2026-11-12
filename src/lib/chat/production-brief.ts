import type { ChatModel3DBriefFieldSchema, ProductSchema } from '@/lib/product-schema';

/**
 * The session's production brief (#103): one object whose members are declared
 * by Product Schema (`chat.model3d_usage.brief.fields`). The server validates a
 * whole replacement; the page edits a draft and sends the whole object back.
 */
export type ProductionBrief = Record<string, unknown>;

/** One choice of a select field (#286): the brief stores `id`, people read `label`. */
export interface ProductionBriefOption {
  id: string;
  label: string;
}

export interface ProductionBriefField {
  name: string;
  /** 'profile' (a model3d profile id), 'image_profile', 'select' (one of `options`), 'text', 'boolean' or 'integer'. */
  type: string;
  label: string;
  maximumCharacters?: number;
  /** A long text field; absent means a single line. */
  multiline?: boolean;
  /** The hint shown inside an empty text field. */
  placeholder?: string;
  minimum?: number;
  maximum?: number;
  /** The choices of a select field, in display order. */
  options?: ProductionBriefOption[];
}

const record = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};

export function productionBriefFields(schema: ProductSchema): ProductionBriefField[] {
  return (schema.chat.model3d_usage?.brief?.fields ?? []).map((field: ChatModel3DBriefFieldSchema) => ({
    name: field.name,
    type: field.type,
    label: field.label,
    ...(typeof field.maximum_characters === 'number' ? { maximumCharacters: field.maximum_characters } : {}),
    ...(field.multiline === true ? { multiline: true } : {}),
    ...(typeof field.placeholder === 'string' && field.placeholder !== '' ? { placeholder: field.placeholder } : {}),
    ...(typeof field.minimum === 'number' ? { minimum: field.minimum } : {}),
    ...(typeof field.maximum === 'number' ? { maximum: field.maximum } : {}),
    ...(Array.isArray(field.options) ? { options: field.options.map(({ id, label }) => ({ id, label })) } : {}),
  }));
}

/** A stored brief as an object; a malformed or absent value is the empty brief. */
export function parseProductionBrief(value: unknown): ProductionBrief {
  return record(value);
}

/** The brief to send: only declared members with a non-empty value. */
export function briefPayload(fields: readonly ProductionBriefField[], draft: ProductionBrief): ProductionBrief {
  const payload: ProductionBrief = {};
  for (const field of fields) {
    const value = draft[field.name];
    if (value === undefined || value === null || value === '') continue;
    if (field.type === 'boolean') {
      payload[field.name] = value === true;
      continue;
    }
    if (field.type === 'integer') {
      const number = typeof value === 'number' ? value : Number(value);
      if (Number.isFinite(number)) payload[field.name] = number;
      continue;
    }
    if (typeof value === 'string') {
      const trimmed = value.trim();
      if (trimmed !== '') payload[field.name] = trimmed;
    }
  }
  return payload;
}

/** Whether the brief has anything worth showing. */
export function briefIsEmpty(brief: ProductionBrief): boolean {
  return Object.values(brief).every((value) => value === undefined || value === null || value === '');
}

/** How a stored value reads in the panel: booleans as yes/no, everything else as text. */
export function briefValueText(value: unknown, yes: string, no: string): string {
  if (typeof value === 'boolean') return value ? yes : no;
  if (typeof value === 'number') return String(value);
  return typeof value === 'string' ? value : '';
}

/**
 * A select value as people read it (#286): the option's label. A stored value
 * that is no option (text written before the field became a select) reads as
 * it was written, so an old brief is never shown blank.
 */
export function briefSelectText(field: Pick<ProductionBriefField, 'options'>, value: unknown): string {
  if (typeof value !== 'string' || value === '') return '';
  return field.options?.find((option) => option.id === value)?.label ?? value;
}

export function briefRoute(schema: ProductSchema, sessionId: string): string | null {
  const template = schema.runtime.http.routes.chat_session_brief;
  return template ? template.replace('{sessionID}', encodeURIComponent(sessionId)) : null;
}
