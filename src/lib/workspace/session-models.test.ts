import { describe, expect, it } from 'vitest';

import { restorableSessionModels } from './session-models';

describe('restorableSessionModels (#318)', () => {
  const choices = { image: ['img-a', 'img-b'], 'model-3d': ['m3-a'] };

  it('fills only domains this browser has not chosen', () => {
    expect(
      restorableSessionModels({ image: null, 'model-3d': 'm3-a' }, { image: 'img-b', 'model-3d': 'm3-old' }, choices),
    ).toEqual({ image: 'img-b' });
  });

  it('ignores models that are no longer offered and unknown domains', () => {
    expect(
      restorableSessionModels({ image: null, 'model-3d': null }, { image: 'retired', 'model-3d': 'm3-a', video: 'v' }, choices),
    ).toEqual({ 'model-3d': 'm3-a' });
  });

  it('waits for a domain whose choices have not loaded', () => {
    expect(
      restorableSessionModels({ image: null, 'model-3d': null }, { image: 'img-a', 'model-3d': 'm3-a' }, { image: undefined, 'model-3d': ['m3-a'] }),
    ).toEqual({ 'model-3d': 'm3-a' });
  });

  it('does nothing for a session without recorded models', () => {
    expect(restorableSessionModels({ image: null, 'model-3d': null }, undefined, choices)).toEqual({});
  });

  it('restores the successor of a retired model the session used (#329)', () => {
    const retired = [{ provider_id: 'meshy', model_id: 'meshy-7', model_name: 'Meshy 7', successor_model_id: 'meshy-7.1' }];
    expect(
      restorableSessionModels({ image: null, 'model-3d': null }, { 'model-3d': 'meshy-7' }, { image: [], 'model-3d': ['meshy-7.1'] }, retired),
    ).toEqual({ 'model-3d': 'meshy-7.1' });
    expect(
      restorableSessionModels({ image: null, 'model-3d': null }, { 'model-3d': 'meshy-7' }, { image: [], 'model-3d': ['meshy-t2.5'] }, retired),
    ).toEqual({});
  });
});
