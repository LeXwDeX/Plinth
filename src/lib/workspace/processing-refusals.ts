import { activeLocale } from '@/lib/i18n/locale';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { groupedCount } from '@/lib/workspace/model-stats';
import type { PrerequisiteRefusal, SourceRefusal } from '@/lib/workspace/processing-source';
import { sizeText } from '@/lib/size-text';
import { unavailableLabel } from '@/lib/unavailable';
import type { UnavailableNoticeContent } from '@/components/ui/unavailable-notice';

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
    if (refusal.kind === 'face_limit') {
      return formatSchemaText(schemaText(schema, 'ui.workspace.processing.source_refused_face_limit'), {
        service, limit: groupedCount(refusal.limit, activeLocale(schema)), faces: groupedCount(refusal.faces, activeLocale(schema)) });
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

/** The few words that say why a service does not take the model, for the notice's inline line (#93). */
export function unavailableReason(schema: ProductSchema, refusal: SourceRefusal): string {
  const text = (kind: string) => schemaText(schema, `ui.workspace.processing.unavailable_reason.${kind}`);
  switch (refusal.kind) {
    case 'too_large':
      return formatSchemaText(text('too_large'), { limit: sizeText(refusal.limit, false) });
    case 'face_limit':
      return formatSchemaText(text('face_limit'), { limit: groupedCount(refusal.limit, activeLocale(schema)) });
    case 'media':
      return text('media');
    case 'expired':
      return text('expired');
    default:
      return formatSchemaText(text('needs_task'), { provider: refusal.offering.provider_name });
  }
}

/**
 * Why a processing step cannot run, in the shape of the shared notice (#93): the inline line names the first reason
 * in a few words; the hint carries every service's reason with its limit and what to do next (Remesh first, a
 * prerequisite step), or that the step before it cannot run. Without a known model there is no detail to give.
 */
export function processingUnavailableNotice(schema: ProductSchema, options: {
  blocked?: boolean; refusals?: readonly SourceRefusal[]; prerequisite?: PrerequisiteRefusal | null; size?: number; step: string;
}): UnavailableNoticeContent {
  const { blocked, refusals = [], prerequisite = null, size = 0, step } = options;
  if (blocked) {
    return { label: unavailableLabel(schema, schemaText(schema, 'ui.workspace.processing.unavailable_reason.blocked')),
      detail: schemaText(schema, 'ui.workspace.processing.blocked_by_previous') };
  }
  const first = refusals[0] ?? prerequisite?.refusals[0];
  const reason = first ? unavailableReason(schema, first) : schemaText(schema, 'ui.workspace.processing.unavailable_reason.unavailable');
  const detail = [sourceRefusalText(schema, refusals, size), prerequisiteRefusalText(schema, prerequisite, step)].filter(Boolean).join('');
  return { label: unavailableLabel(schema, reason), detail: detail || undefined };
}
