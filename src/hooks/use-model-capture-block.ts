import { useStudioStore } from '@/store/studio-store';

/** The Schema message saying why the model of this task cannot be captured to 2D right now. */
export const CAPTURE_BLOCK_TEXT = {
  notReady: 'ui.studio.stage.action.capture_unavailable',
  texturesIncomplete: 'ui.studio.stage.action.capture_textures_incomplete',
} as const;

/** Why the model on the 3D stage cannot be captured (#126), or null when it can or is not the one on the stage. */
export function captureBlockKeyOf(taskId: string | undefined, state: { taskId: string; state: string } | null): string | null {
  if (!taskId || state?.taskId !== taskId || state.state === 'ready') return null;
  return state.state === 'incomplete' ? CAPTURE_BLOCK_TEXT.texturesIncomplete : CAPTURE_BLOCK_TEXT.notReady;
}

/**
 * Entries that capture a model (result card, 舞台头, version bar) grey out with this reason while the model of
 * that task is on the stage and still loading, failed to load or has incomplete textures. A model that is not on
 * the stage has no known state; capturing it opens it first.
 */
export function useModelCaptureBlock(taskId: string | undefined): string | null {
  return useStudioStore((state) => captureBlockKeyOf(taskId, state.stageModelState));
}
