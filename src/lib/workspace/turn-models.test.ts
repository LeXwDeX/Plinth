import { describe, expect, it } from 'vitest';
import { turnModels } from './turn-models';

describe('#678: the models a turn carries', () => {
  it('sends the stage domain pick as the model and the other picks as domain models', () => {
    expect(turnModels('image', { image: 'gpt-image', 'model-3d': 'meshy' })).toEqual({
      model: 'gpt-image', domainModels: { 'model-3d': 'meshy' },
    });
  });

  it('sends a one-turn 3D model as a domain model while the stage shows a picture', () => {
    expect(turnModels('image', { image: 'gpt-image', 'model-3d': null }, { 'model-3d': 'tripo' })).toEqual({
      model: 'gpt-image', domainModels: { 'model-3d': 'tripo' },
    });
  });

  it('sends a one-turn 3D model as the model while the stage shows a 3D model and nothing is picked', () => {
    // 只用正面转 3D / 舞台头转 3D 的替换正面：舞台仍是 3D 模型，确认框选的模型不能被舞台域过滤掉。
    expect(turnModels('model-3d', { image: 'gpt-image', 'model-3d': null }, { 'model-3d': 'tripo' })).toEqual({
      model: 'tripo', domainModels: { image: 'gpt-image' },
    });
  });

  it('keeps the person\'s own pick for the stage domain over a one-turn model', () => {
    expect(turnModels('model-3d', { image: null, 'model-3d': 'meshy' }, { 'model-3d': 'tripo' })).toEqual({
      model: 'meshy', domainModels: {},
    });
  });

  it('sends no model when nothing is picked or confirmed', () => {
    expect(turnModels('model-3d', { image: null, 'model-3d': null })).toEqual({ model: undefined, domainModels: {} });
  });
});
