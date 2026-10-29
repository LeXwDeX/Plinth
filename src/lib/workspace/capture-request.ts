import type { StageModelState } from '@/store/studio-store';

/**
 * What the 3D stage does with a pending 「截图到 2D」 request (#126):
 * - `ignore`: nothing to do (no request, another model's request, or a capture is running);
 * - `drop`: the model failed to load or has incomplete textures, so the request is discarded and never replayed;
 * - `wait`: the model is still loading for the first time, the request stays until it ends;
 * - `run`: the model is ready, take the picture.
 */
export type CaptureStep = 'ignore' | 'drop' | 'wait' | 'run';

export function captureStepOf(
  request: { taskId: string } | null,
  taskId: string,
  state: StageModelState,
  capturing: boolean,
): CaptureStep {
  if (!request || request.taskId !== taskId || capturing) return 'ignore';
  if (state === 'failed' || state === 'incomplete') return 'drop';
  return state === 'ready' ? 'run' : 'wait';
}
