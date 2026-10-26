import { useMemo } from 'react';
import useSWR from 'swr';

import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { groupWorkspaceModels } from '@/lib/workspace/model-choices';
import { domainCapabilities, type WorkspaceDomain } from '@/store/workspace-store';

export function workspaceOfferingsKey(schema: ProductSchema, domain: WorkspaceDomain) {
  const capabilities = domainCapabilities(schema, domain);
  return capabilities.length > 0
    ? ['workspace-offerings', schema.revision, schema.runtime.http.routes.offerings, domain, capabilities.map(({ id }) => id)] as const
    : null;
}

/** One human model choice spans the Agent's internal text/image capabilities. */
export function useWorkspaceOfferings(schema: ProductSchema, domain: WorkspaceDomain) {
  const capabilities = useMemo(() => domainCapabilities(schema, domain), [schema, domain]);
  const { data: offeringsByCapability, isLoading, error } = useSWR<Record<string, OfferingWire[]>>(
    workspaceOfferingsKey(schema, domain),
    async () => Object.fromEntries(await Promise.all(
      capabilities.map(async (capability) => [
        capability.id,
        await fetchCapabilityOfferings(schema, capability.id),
      ]),
    )),
    { revalidateOnFocus: false },
  );
  const modelChoices = useMemo(
    () => groupWorkspaceModels(offeringsByCapability),
    [offeringsByCapability],
  );
  return { offeringsByCapability, modelChoices, isLoading, error };
}
