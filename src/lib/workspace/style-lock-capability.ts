import type { OfferingWire } from '@/lib/agent-offerings';

/** What the composer says about style-lock references before sending (#154). */
export type StyleLockCapability = 'model_no_reference' | 'references_trimmed';

/**
 * Whether the Offering the next 2D turn would run takes reference pictures, and whether the
 * style-lock references plus this turn's pictures exceed its declared capacity. A `preserve`
 * Offering refuses an overflow instead of trimming, and an Offering without `max_items` has no
 * known capacity, so neither reports `references_trimmed`.
 */
export function styleLockCapability(offering: OfferingWire | undefined, referenceCount: number): StyleLockCapability | null {
  if (!offering || referenceCount <= 0) return null;
  const parameter = offering.parameters.find(
    (candidate) => candidate.semantic === 'reference-image' && candidate.type === 'asset-array',
  );
  if (!parameter) return 'model_no_reference';
  if (offering.reference_selection_policy === 'preserve' || typeof parameter.max_items !== 'number') return null;
  return referenceCount > parameter.max_items ? 'references_trimmed' : null;
}