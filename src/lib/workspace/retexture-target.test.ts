import { describe, expect, it } from 'vitest';

import { defaultRetextureTarget, retextureTargets, sourceModelOf } from '@/lib/workspace/retexture-target';
import type { VersionTile } from '@/lib/workspace/versions';

function tile(key: string, domain: 'image' | 'model', version: number, parents: string[] = [], status: VersionTile['status'] = 'ready'): VersionTile {
  return {
    key, taskId: key, taskIds: [key], domain, version, status, progress: 0, capability: '', createdAt: '', assets: [], thumbnailUrl: '',
    groupLabel: '', groupComplete: false, slots: [], views: [], failure: '',
    origin: 'task', operation: '', captureId: '', size: null,
    parents: parents.map((parent) => ({ key: parent, known: parent !== '', kind: 'source_task', evidence: 'recorded', link: 'process' })) as VersionTile['parents'],
  };
}

describe('retexture target', () => {
  const tiles = [
    tile('img1', 'image', 1),
    tile('m1', 'model', 1, ['img1']),
    tile('m2', 'model', 2, ['img1']),
    tile('shot', 'image', 2, ['m1']),
    tile('paint', 'image', 3, ['shot']),
    tile('m3', 'model', 3, ['m2'], 'failed'),
    tile('m4', 'model', 4, ['m2']),
  ];

  it('lists finished models only, oldest first', () => {
    expect(retextureTargets(tiles).map((item) => item.key)).toEqual(['m1', 'm2', 'm4']);
  });

  it('finds the model a picture came from through its screenshots and paintings', () => {
    expect(sourceModelOf(tiles, 'shot')?.key).toBe('m1');
    expect(sourceModelOf(tiles, 'paint')?.key).toBe('m1');
    expect(sourceModelOf(tiles, 'img1')).toBeNull();
    expect(sourceModelOf(tiles, null)).toBeNull();
  });

  it('starts from the model the picture came from, else the newest model', () => {
    expect(defaultRetextureTarget(tiles, 'paint')).toMatchObject({ reason: 'from_image', tile: { key: 'm1' } });
    expect(defaultRetextureTarget(tiles, 'img1')).toMatchObject({ reason: 'latest', tile: { key: 'm4' } });
    expect(defaultRetextureTarget([tile('img1', 'image', 1)], 'img1')).toBeNull();
  });

  it('does not loop on a cycle of sources', () => {
    const loop = [tile('a', 'image', 1, ['b']), tile('b', 'image', 2, ['a'])];
    expect(sourceModelOf(loop, 'a')).toBeNull();
  });
});
