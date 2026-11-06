import { describe, expect, it } from 'vitest';

import type { OfferingWire } from '@/lib/agent-offerings';
import { styleLockCapability } from './style-lock-capability';

function offering(parameters: OfferingWire['parameters'], policy?: OfferingWire['reference_selection_policy']): OfferingWire {
  return {
    id: 'o1', revision: 'r1', provider_id: 'p', provider_name: 'P', model_id: 'm', model_name: 'M',
    capability: 'text-to-image', available: true, parameters, ...(policy ? { reference_selection_policy: policy } : {}),
  };
}

const referenceParameter = (maxItems?: number) => ({
  name: 'images', label: 'Images', type: 'asset-array', semantic: 'reference-image', ...(maxItems === undefined ? {} : { max_items: maxItems }),
});

describe('styleLockCapability', () => {
  it('says nothing without an Offering or style-lock references', () => {
    expect(styleLockCapability(offering([referenceParameter(3)]), 0)).toBeNull();
    expect(styleLockCapability(undefined, 2)).toBeNull();
  });

  it('reports a model whose Offering does not take reference pictures', () => {
    expect(styleLockCapability(offering([{ name: 'prompt', label: 'Prompt', type: 'text' }]), 1)).toBe('model_no_reference');
  });

  it('reports trimming only above the declared capacity', () => {
    expect(styleLockCapability(offering([referenceParameter(3)]), 3)).toBeNull();
    expect(styleLockCapability(offering([referenceParameter(3)]), 4)).toBe('references_trimmed');
  });

  it('never calls a preserve Offering trimmed: it refuses instead', () => {
    expect(styleLockCapability(offering([referenceParameter(1)], 'preserve'), 2)).toBeNull();
  });

  it('does not guess a capacity the Schema does not declare', () => {
    expect(styleLockCapability(offering([referenceParameter()]), 10)).toBeNull();
  });
});