import type { OfferingWire } from '@/lib/agent-offerings';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';

/** A turn gives more pictures than the model takes: only the first `count` of `total` are used (#329 follow-up). */
export interface ReferenceLimit {
  model: string;
  count: number;
  total: number;
}

/**
 * Whether the Offering the next turn runs takes fewer reference pictures than are attached. The limit is its
 * reference-image parameter's Schema `max_items`; the Agent and a direct request both keep the first pictures, so the
 * composer says so before sending. An Offering that preserves its picture sequence refuses instead of cutting.
 */
export function referenceLimit(offering: OfferingWire | undefined, pictures: number): ReferenceLimit | null {
  if (!offering || pictures <= 0 || offering.reference_selection_policy === 'preserve') return null;
  const parameter = offering.parameters.find((wire) => wire.semantic === 'reference-image' && wire.type === 'asset-array');
  const count = parameter?.max_items;
  if (!count || pictures <= count) return null;
  return { model: offering.model_name || offering.model_id, count, total: pictures };
}

/**
 * What the composer says about a reference limit, in the words the server uses for the task API, the approval card and
 * the finished message (#329 note 99647): the Schema's one-picture text when the model takes exactly one picture (in
 * every language: "第 1 张", "only the first is used"), otherwise the general text. Empty when the Schema has none.
 */
export function referenceLimitNote(schema: ProductSchema, limit: ReferenceLimit): string {
  const messages = schema.runtime.generation_messages;
  const template = (limit.count === 1 && messages?.reference_limit_one) || messages?.reference_limit || '';
  return template ? formatSchemaText(template, { model: limit.model, count: limit.count, total: limit.total }) : '';
}
