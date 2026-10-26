import { describe, expect, it, vi } from 'vitest';

import { isCurrentLoad, loadProgressOf, resetModelView } from './model-view';

describe('resetModelView', () => {
  it('puts orbit, target and field of view back to auto and jumps the camera', () => {
    const element = { cameraOrbit: '10deg 20deg 3m', cameraTarget: '1m 0m 0m', fieldOfView: '20deg', jumpCameraToGoal: vi.fn(), resetTurntableRotation: vi.fn() };
    expect(resetModelView(element)).toBe(true);
    expect(element.cameraOrbit).toBe('auto auto auto');
    expect(element.cameraTarget).toBe('auto auto auto');
    expect(element.fieldOfView).toBe('auto');
    expect(element.jumpCameraToGoal).toHaveBeenCalledOnce();
    expect(element.resetTurntableRotation).toHaveBeenCalledOnce();
  });

  it('does nothing without an element', () => {
    expect(resetModelView(null)).toBe(false);
  });
});

describe('model-viewer event readers', () => {
  it('reads the load progress and clamps it', () => {
    expect(loadProgressOf(new CustomEvent('progress', { detail: { totalProgress: 0.4 } }))).toBe(0.4);
    expect(loadProgressOf(new CustomEvent('progress', { detail: { totalProgress: 3 } }))).toBe(1);
    expect(loadProgressOf(new CustomEvent('progress', { detail: {} }))).toBeNull();
  });

  it('tells a load of the current source from a late load of the previous one', () => {
    const element = { src: '/models/b.glb' };
    expect(isCurrentLoad(new CustomEvent('load', { detail: { url: '/models/b.glb' } }), element)).toBe(true);
    expect(isCurrentLoad(new CustomEvent('load', { detail: { url: '/models/a.glb' } }), element)).toBe(false);
    expect(isCurrentLoad(new CustomEvent('load', { detail: {} }), element)).toBe(true);
  });
});
