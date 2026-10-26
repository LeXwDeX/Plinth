import type { OfferingWire } from '@/lib/agent-offerings';

export type WorkspaceModelChoice = OfferingWire & {
  offerings: OfferingWire[];
  capabilities: string[];
};

/** Group available Offering records into one choice per provider model. */
export function groupWorkspaceModels(
  offeringsByCapability: Record<string, OfferingWire[]> | null | undefined,
): WorkspaceModelChoice[] {
  const choices = new Map<string, WorkspaceModelChoice>();
  const seenOfferingIDs = new Map<string, Set<string>>();
  const seenCapabilities = new Map<string, Set<string>>();

  for (const offerings of Object.values(offeringsByCapability ?? {})) {
    for (const offering of offerings) {
      if (!offering.available) continue;

      const key = JSON.stringify([offering.provider_id, offering.model_id]);
      let choice = choices.get(key);
      if (!choice) {
        choice = { ...offering, offerings: [], capabilities: [] };
        choices.set(key, choice);
        seenOfferingIDs.set(key, new Set());
        seenCapabilities.set(key, new Set());
      }

      const offeringIDs = seenOfferingIDs.get(key)!;
      if (!offeringIDs.has(offering.id)) {
        offeringIDs.add(offering.id);
        choice.offerings.push(offering);
      }

      const capabilities = seenCapabilities.get(key)!;
      if (!capabilities.has(offering.capability)) {
        capabilities.add(offering.capability);
        choice.capabilities.push(offering.capability);
      }
    }
  }

  return [...choices.values()];
}
