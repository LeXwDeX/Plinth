import { describe, expect, it } from 'vitest';

import { groupedCount, modelStatsFromGltf } from './model-stats';

describe('modelStatsFromGltf', () => {
  it('counts triangles and vertices from indexed and non-indexed primitives', () => {
    const stats = modelStatsFromGltf({
      accessors: [{ count: 100 }, { count: 300 }, { count: 30 }],
      meshes: [
        { primitives: [{ attributes: { POSITION: 0 }, indices: 1 }] },
        { primitives: [{ attributes: { POSITION: 2 } }] },
      ],
      materials: [{}, {}],
      textures: [{}],
      animations: [],
    });
    expect(stats).toEqual({ triangles: 110, vertices: 130, meshes: 2, materials: 2, textures: 1, animations: 0 });
  });

  it('reads strips and ignores point and line primitives', () => {
    const stats = modelStatsFromGltf({
      accessors: [{ count: 10 }],
      meshes: [{ primitives: [
        { attributes: { POSITION: 0 }, mode: 5 },
        { attributes: { POSITION: 0 }, mode: 1 },
      ] }],
    });
    expect(stats?.triangles).toBe(8);
  });

  it('returns null rather than zeros when the file carries nothing usable', () => {
    expect(modelStatsFromGltf(null)).toBeNull();
    expect(modelStatsFromGltf({})).toBeNull();
    expect(modelStatsFromGltf({ accessors: [{}], meshes: [{ primitives: [{ attributes: { POSITION: 0 } }] }] })).toBeNull();
  });
});

describe('groupedCount', () => {
  it('writes the whole number with grouping', () => {
    expect(groupedCount(4608, 'en')).toBe('4,608');
    expect(groupedCount(48210, 'zh-CN')).toBe('48,210');
    expect(groupedCount(12, 'en')).toBe('12');
  });
});
