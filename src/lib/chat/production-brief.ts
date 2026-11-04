import type { ChatModel3DBriefFieldSchema, ProductSchema } from '@/lib/product-schema';

/**
 * The session's production brief (#103): one object whose members are declared
 * by Product Schema (`chat.model3d_usage.brief.fields`). The server validates a
 * whole replacement; the page edits a draft and sends the whole object back.
 */
export type ProductionBrief = Record<string, unknown>;

export interface ProductionBriefField {
  name: string;
  /** 'profile' (a model3d profile id), 'text', 'boolean' or 'integer'. */
  type: string;
  label: string;
  maximumCharacters?: number;
  /** A long text field; absent means a single line. */
  multiline?: boolean;
  /** The hint shown inside an empty text field. */
  placeholder?: string;
  minimum?: number;
  maximum?: number;
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

export function briefRoute(schema: ProductSchema, sessionId: string): string | null {
  const template = schema.runtime.http.routes.chat_session_brief;
  return template ? template.replace('{sessionID}', encodeURIComponent(sessionId)) : null;
}
