import { describe, expect, it } from 'vitest';

import { objectOfTile } from './version-object';
import type { VersionTile } from './versions';

const asset = (id: string, kind: 'image' | 'model') => ({
  id, role: kind, variant: '', mediaType: kind === 'model' ? 'model/gltf-binary' : '', ordinal: 0, url: `/a/${id}`, kind,
});
const tile = (patch: Partial<VersionTile>): VersionTile => ({
  key: 'n:t1', taskId: 't1', taskIds: ['t1'], domain: 'image', version: 1, status: 'ready', progress: 1,
  capability: 'text-to-image', createdAt: '', assets: [], thumbnailUrl: '', groupLabel: '', groupComplete: true,
  slots: [], views: [], parents: [], failure: '', origin: 'task', operation: '', captureId: '', size: null, ...patch,
});

describe('objectOfTile', () => {
  it('takes the first candidate of an image version, or the one asked for', () => {
    const t = tile({ assets: [asset('a1', 'image'), asset('a2', 'image')] });
    expect(objectOfTile(t, 's')).toMatchObject({ taskId: 't1', kind: 'image', assetId: 'a1', imageUrls: ['/a/a1', '/a/a2'], sessionId: 's' });
    expect(objectOfTile(t, 's', { assetId: 'a2' })).toMatchObject({ assetId: 'a2', url: '/a/a2' });
  });

  it('opens the model file of a 3D version, not its thumbnail', () => {
    const t = tile({ domain: 'model', assets: [asset('m1', 'model')], thumbnailUrl: '/thumb' });
    expect(objectOfTile(t, 's')).toEqual({ taskId: 't1', kind: 'model', url: '/a/m1', assetId: 'm1', sessionId: 's' });
  });

  it('opens the GLB of a multi-format 3D version, never the 3MF listed first', () => {
    const threeMF = { ...asset('m3', 'model'), variant: '3mf', mediaType: 'model/3mf', ordinal: 0 };
    const glb = { ...asset('mg', 'model'), variant: 'glb', ordinal: 2 };
    expect(objectOfTile(tile({ domain: 'model', assets: [threeMF, glb] }), 's')).toMatchObject({ assetId: 'mg' });
    expect(objectOfTile(tile({ domain: 'model', assets: [threeMF] }), 's')).toBeNull();
  });

  it('opens the front slot of a group, or the slot that was clicked', () => {
    const t = tile({
      key: 'g:1', taskId: 't-front', taskIds: ['t-front', 't-side'],
      assets: [asset('f', 'image'), asset('s', 'image')],
      slots: [{ slot: 'front', label: '正面', taskId: 't-front', assetId: 'f' }, { slot: 'side', label: '侧面', taskId: 't-side', assetId: 's' }],
    });
    expect(objectOfTile(t, 'x')).toMatchObject({ taskId: 't-front', assetId: 'f' });
    expect(objectOfTile(t, 'x', { taskId: 't-side' })).toMatchObject({ taskId: 't-side', assetId: 's' });
  });

  it('has nothing to show for work that is not finished', () => {
    expect(objectOfTile(tile({ status: 'running' }), 's')).toBeNull();
    expect(objectOfTile(tile({ status: 'failed' }), 's')).toBeNull();
    expect(objectOfTile(tile({ assets: [] }), 's')).toBeNull();
  });
});

it('keeps a model upload lineage node separate from a Task-backed CurrentObject', () => {
  const uploaded = tile({ origin: 'upload', domain: 'model', taskId: 'upload:owned-model', assets: [{ id: 'owned-model', role: 'model', kind: 'model', variant: 'glb', mediaType: 'model/gltf-binary', url: '/api/v1/assets/owned-model', ordinal: 0 }] });
  expect(objectOfTile(uploaded, 's')).toBeNull();
});
