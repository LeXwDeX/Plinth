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
 * What the composer says before sending about a reference limit. It states the
 * model's capacity and how many pictures are attached, never which picture will
 * be used: a three-view view may be replaced by its group's front view and a
 * duplicate may be dropped before the limit applies (#80). The actual source is
 * stated only after sending, in the result's reference note. Empty when the
 * Schema has no capacity text.
 */
export function referenceLimitNote(schema: ProductSchema, limit: ReferenceLimit): string {
  const template = schema.runtime.generation_messages?.reference_capacity || '';
  return template ? formatSchemaText(template, { model: limit.model, count: limit.count, total: limit.total }) : '';
}
