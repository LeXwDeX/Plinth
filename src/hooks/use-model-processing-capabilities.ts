import { useMemo } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';

type Capability = ProductSchema['capabilities'][number];

function hasModelOutput(offering: OfferingWire): boolean {
  return offering.available && Boolean(offering.outputs?.some((output) => output !== null && typeof output === 'object' && 'role' in output && output.role === 'model'));
}

/**
 * The model-processing functions offered for a 3D model: the Schema's
 * runtime.frontend.model_processing_capabilities that have at least one available
 * Offering producing a model. The right panel's 加工 tab and the stage
 * toolbar both read this, so a function is offered in both or in neither.
 */
export function useModelProcessingCapabilities() {
  const { schema } = useProductSchema();
  const capabilities = useMemo(() => schema.runtime.frontend.model_processing_capabilities
    .map((id) => schema.capabilities.find((capability) => capability.id === id && capability.domain === 'processing'))
    .filter((capability): capability is Capability => capability !== undefined), [schema]);
  const { data: offerings, error, mutate } = useSWR<Record<string, OfferingWire[]>>(['processing-offerings', schema.revision],
    async () => Object.fromEntries(await Promise.all(capabilities.map(async (capability) => [capability.id, await fetchCapabilityOfferings(schema, capability.id)]))),
    { revalidateOnFocus: false });
  const available = useMemo(() => capabilities.filter((capability) => offerings?.[capability.id]?.some(hasModelOutput)), [capabilities, offerings]);
  return { capabilities, available, offerings, error, refresh: mutate };
}
