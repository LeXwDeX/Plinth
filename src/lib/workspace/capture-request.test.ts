import { beforeEach, describe, expect, it } from 'vitest';
import { captureBlockKeyOf } from '@/hooks/use-model-capture-block';
import { useStudioStore } from '@/store/studio-store';
import { captureStepOf } from './capture-request';

describe('capture request of the 3D stage (#126)', () => {
  beforeEach(() => useStudioStore.setState({ captureRequest: null, stageModelState: null }));

  it('runs a request only for a ready model of its own task', () => {
    const request = { taskId: 't1' };
    expect(captureStepOf(request, 't1', 'ready', false)).toBe('run');
    expect(captureStepOf(request, 't1', 'ready', true)).toBe('ignore');
    expect(captureStepOf(request, 't2', 'ready', false)).toBe('ignore');
    expect(captureStepOf(null, 't1', 'ready', false)).toBe('ignore');
  });

  it('drops a request of a failed or texture-incomplete model and keeps one for a model that is still loading', () => {
    expect(captureStepOf({ taskId: 't1' }, 't1', 'failed', false)).toBe('drop');
    expect(captureStepOf({ taskId: 't1' }, 't1', 'incomplete', false)).toBe('drop');
    expect(captureStepOf({ taskId: 't1' }, 't1', 'loading', false)).toBe('wait');
  });

  it('does not replay a click made while loading failed after Retry succeeds', () => {
    const store = useStudioStore.getState();
    store.requestCapture('t1');
    // The load fails: the stage drops the request instead of leaving it behind.
    if (captureStepOf(useStudioStore.getState().captureRequest, 't1', 'failed', false) === 'drop') useStudioStore.getState().clearCaptureRequest();
    expect(useStudioStore.getState().captureRequest).toBeNull();
    // Retry loads the model: nothing is left to run.
    expect(captureStepOf(useStudioStore.getState().captureRequest, 't1', 'ready', false)).toBe('ignore');
  });

  it('names why a model on the stage cannot be captured, per state', () => {
    expect(captureBlockKeyOf('t1', { taskId: 't1', state: 'failed' })).toBe('ui.studio.stage.action.capture_unavailable');
    expect(captureBlockKeyOf('t1', { taskId: 't1', state: 'loading' })).toBe('ui.studio.stage.action.capture_unavailable');
    expect(captureBlockKeyOf('t1', { taskId: 't1', state: 'incomplete' })).toBe('ui.studio.stage.action.capture_textures_incomplete');
    expect(captureBlockKeyOf('t1', { taskId: 't1', state: 'ready' })).toBeNull();
    // The model on the stage is another one, or none: no known state, so nothing is greyed.
    expect(captureBlockKeyOf('t1', { taskId: 't2', state: 'failed' })).toBeNull();
    expect(captureBlockKeyOf('t1', null)).toBeNull();
    expect(captureBlockKeyOf(undefined, { taskId: 't1', state: 'failed' })).toBeNull();
  });
});
