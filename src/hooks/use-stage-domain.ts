import { useWorkspaceStore, type CurrentObject, type WorkspaceDomain } from '@/store/workspace-store';

/** The domain the stage is showing: a model is 3D, anything else (even nothing) is 2D. */
export function stageDomainOf(object: Pick<CurrentObject, 'kind'> | null): WorkspaceDomain {
  return object?.kind === 'model' ? 'model-3d' : 'image';
}

/** Capability- and model-dependent controls follow the object on the stage, not a tab. */
export function useStageDomain(): WorkspaceDomain {
  return useWorkspaceStore((state) => stageDomainOf(state.currentObject));
}
