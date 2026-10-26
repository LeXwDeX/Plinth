import type { RetiredGenerationModel } from '@/lib/workspace/retired-models';
import type { WorkspaceDomain } from '@/store/workspace-store';

const DOMAINS: WorkspaceDomain[] = ['image', 'model-3d'];

/**
 * #318: the models to restore when a session is opened. The session read
 * reports the model last sent in each domain; a browser that has no pick of
 * its own for a domain (a new device, cleared site data) takes it, but only
 * while that model is still offered. A local pick is never replaced, and a
 * domain whose choices have not loaded yet is left for a later pass. A recorded model the Schema lists as
 * retired is restored as its successor while that is offered (#329).
 */
export function restorableSessionModels(
  local: Record<WorkspaceDomain, string | null>,
  recorded: Readonly<Record<string, string>> | undefined,
  choices: Partial<Record<WorkspaceDomain, readonly string[] | undefined>>,
  retired?: readonly RetiredGenerationModel[],
): Partial<Record<WorkspaceDomain, string>> {
  const restored: Partial<Record<WorkspaceDomain, string>> = {};
  if (!recorded) return restored;
  for (const domain of DOMAINS) {
    const offered = choices[domain];
    let model: string | undefined = recorded[domain];
    if (model && offered && !offered.includes(model)) {
      const successor = retired?.find((item) => item.model_id === model)?.successor_model_id;
      model = successor && offered.includes(successor) ? successor : undefined;
    }
    if (local[domain] || !model || !offered?.includes(model)) continue;
    restored[domain] = model;
  }
  return restored;
}
