import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { PrerequisiteRefusal, SourceRefusal } from '@/lib/workspace/processing-source';
import { sizeText } from '@/lib/size-text';

/** A byte count as the server's size refusals show it: the shared decimal-MB text (#327). */
export { sizeText };

function capabilityLabel(schema: ProductSchema, id: string): string {
  return schema.capabilities.find((item) => item.id === id)?.label_cn || id;
}

function refusalReasons(schema: ProductSchema, refusals: readonly SourceRefusal[]): string {
  const label = (id: string) => capabilityLabel(schema, id);
  return refusals.map((refusal) => {
    const service = `${refusal.offering.provider_name} · ${refusal.offering.model_name}`;
    if (refusal.kind === 'too_large') {
      return formatSchemaText(schemaText(schema, 'ui.workspace.processing.source_refused_too_large'), { service, limit: sizeText(refusal.limit, false) });
    }
    if (refusal.kind === 'expired') {
      return formatSchemaText(schemaText(schema, 'ui.workspace.processing.source_refused_expired'), { service, provider: refusal.offering.provider_name });
    }
    if (refusal.kind === 'media') {
      return formatSchemaText(schemaText(schema, 'ui.workspace.processing.source_refused_media'), { service });
    }
    return formatSchemaText(schemaText(schema, 'ui.workspace.processing.source_refused_task'), {
      service, provider: refusal.offering.provider_name, capabilities: refusal.capabilities.map(label).join(schemaText(schema, 'ui.studio.list_separator')),
    });
  }).join(schemaText(schema, 'ui.workspace.processing.source_refused_separator'));
}

/**
 * Why the current model cannot go into a step, in the Schema's words (#321 E): each available
 * service with its reason, and what the person can do.
 */
export function sourceRefusalText(schema: ProductSchema, refusals: readonly SourceRefusal[], size: number): string {
  if (refusals.length === 0) return '';
  return formatSchemaText(schemaText(schema, 'ui.workspace.processing.source_refused'), { size: sizeText(size, true), reasons: refusalReasons(schema, refusals) });
}

/**
 * Why the step that would come first is not brought in, and what works (#326): its services' reasons, then doing
 * that step on a model it accepts (the version before it, say) and this step after it.
 */
export function prerequisiteRefusalText(schema: ProductSchema, refusal: PrerequisiteRefusal | null, step: string): string {
  if (!refusal || refusal.refusals.length === 0) return '';
  return formatSchemaText(schemaText(schema, 'ui.workspace.processing.prerequisite_refused'), {
    prerequisite: capabilityLabel(schema, refusal.capability), step: capabilityLabel(schema, step), reasons: refusalReasons(schema, refusal.refusals),
  });
}
