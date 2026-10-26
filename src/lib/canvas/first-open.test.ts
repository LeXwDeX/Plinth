import { describe, expect, it } from 'vitest';
import { canRestoreFirstCanvasImage, firstCanvasImage } from './first-open';
import type { CanvasProject } from './browser-client';
import type { CurrentObject } from '@/store/workspace-store';
import type { VersionTile } from '@/lib/workspace/versions';
import { planAutoJoin } from '@/lib/workspace/canvas-auto-join';
import { localBitmapNodeIds, registerLocalBitmaps } from './pending-edits';

const project: CanvasProject = {
  id: 'board', userId: 'owner', sessionId: 's1', name: 'New', viewport: '{}', bgStyle: 'dots',
  revision: 1, createdAt: '', updatedAt: '', nodes: [], frame: null, placedResults: null,
};
const creation = { ...project, created: true };
const selection: CurrentObject = { kind: 'image', taskId: 't1', assetId: 'a1', url: '/a1.png', sessionId: 's1', restored: true };
const tile: VersionTile = {
  key: 't1', taskId: 't1', taskIds: ['t1'], domain: 'image', version: 1, status: 'ready', progress: 1,
  capability: 'text-to-image', createdAt: '2026-10-21T00:00:00Z',
  assets: [{ id: 'a1', role: 'image', kind: 'image', variant: '', mediaType: 'image/png', ordinal: 1, url: '/a1.png' }],
  thumbnailUrl: '', groupLabel: '', groupComplete: false, slots: [], views: [], parents: [], failure: '',
  origin: 'task', operation: '', captureId: '', size: null,
};

describe('first open of a newly created canvas', () => {
  it('restores only an authoritative creation whose fetched empty project is still untouched', () => {
    expect(canRestoreFirstCanvasImage(creation, project, 0, false)).toBe(true);
    for (const changed of [{ created: false }, { created: undefined }, { id: 'other' }, { sessionId: 'other' }, { revision: undefined }]) {
      expect(canRestoreFirstCanvasImage({ ...creation, ...changed }, project, 0, false)).toBe(false);
    }
    expect(canRestoreFirstCanvasImage(creation, { ...project, revision: 2 }, 0, false)).toBe(false);
    expect(canRestoreFirstCanvasImage(creation, { ...project, nodes: undefined }, 0, false)).toBe(false);
    expect(canRestoreFirstCanvasImage(creation, { ...project, nodes: [{} as never] }, 0, false)).toBe(false);
    expect(canRestoreFirstCanvasImage(creation, project, 1, false)).toBe(false);
    expect(canRestoreFirstCanvasImage(creation, project, 0, true)).toBe(false);
  });

  it('selects the current completed task image, without backfilling other historical assets', () => {
    const other = { ...tile, taskId: 'old', assets: [{ ...tile.assets[0], id: 'old', url: '/old.png' }] };
    expect(firstCanvasImage(selection, 's1', [other, tile])).toEqual({ key: 't1#a1', taskId: 't1', url: '/a1.png' });
    expect(firstCanvasImage({ ...selection, assetId: undefined }, 's1', [tile])?.key).toBe('t1#a1');
    expect(firstCanvasImage({ ...selection, url: '/unverified.png', assetId: undefined }, 's1', [tile])).toBeNull();
    expect(firstCanvasImage({ ...selection, assetId: 'unverified' }, 's1', [tile])).toBeNull();
  });

  it('does not place models, non-task results, unfinished work or another session', () => {
    expect(firstCanvasImage(null, 's1', [tile])).toBeNull();
    for (const changed of [{ kind: 'model' as const }, { restored: false }, { sessionId: 's2' }, { taskId: 'canvas:x' }]) {
      expect(firstCanvasImage({ ...selection, ...changed }, 's1', [tile])).toBeNull();
    }
    for (const changed of [{ domain: 'model' as const }, { status: 'pending' as const }, { status: 'failed' as const },
      { origin: 'upload' as const }, { origin: 'capture' as const }, { origin: 'merge' as const }]) {
      expect(firstCanvasImage(selection, 's1', [{ ...tile, ...changed }])).toBeNull();
    }
  });

  it('restores a selected non-leading group slot using its task, keeping the existing group registration key', () => {
    const back = { ...tile.assets[0], id: 'back-asset', url: '/back.png' };
    const grouped = { ...tile, taskId: 'front', taskIds: ['front', 'back'],
      assets: [tile.assets[0], back], slots: [
        { slot: 'front', label: 'Front', taskId: 'front', assetId: 'a1' },
        { slot: 'back', label: 'Back', taskId: 'back', assetId: 'back-asset' },
      ] };
    const picked = { ...selection, taskId: 'back', assetId: 'back-asset', url: '/back.png' };
    expect(firstCanvasImage(picked, 's1', [grouped])).toEqual({ key: 'front#back-asset', taskId: 'back', url: '/back.png' });
    expect(planAutoJoin([grouped], null).placed).toContain('front#back-asset');
    expect(firstCanvasImage({ ...picked, taskId: 'front' }, 's1', [grouped])).toBeNull();
  });

  it('restores the ordinary picture even after the null receipt was seeded with its key', () => {
    // The lineage can arrive (and seed every key) before the delayed restored selection is set.
    const seeded = planAutoJoin([tile], null).placed;
    expect(seeded).toContain('t1#a1');
    expect(firstCanvasImage(selection, 's1', [tile])).toEqual({ key: 't1#a1', taskId: 't1', url: '/a1.png' });
  });

  it('never restores an inpaint raw result, with or without a local job', () => {
    const inpaint = { ...tile, operation: 'inpaint' };
    expect(firstCanvasImage(selection, 's1', [inpaint])).toBeNull();
    // A retry keeps the server operation, so a new task id without a local job is still refused.
    expect(firstCanvasImage({ ...selection, taskId: 'retry', assetId: undefined, url: '/a1.png' }, 's1',
      [{ ...inpaint, taskId: 'retry', key: 'retry' }])).toBeNull();
  });

  it('keeps the deletion/refresh and legacy-history watermark rules after the first placement', () => {
    const seeded = planAutoJoin([tile], null);
    expect(seeded.place).toEqual([]);
    expect(seeded.placed).toContain('t1#a1');
    expect(planAutoJoin([tile], seeded.placed).place).toEqual([]);
    expect(canRestoreFirstCanvasImage({ ...creation, created: false }, { ...project, revision: 3 }, 0, false)).toBe(false);
  });

  it('treats a brush bitmap awaiting upload as a local draft even before nodes change', () => {
    const release = registerLocalBitmaps('first-open-bitmap', () => ['initial-layer']);
    try {
      expect(canRestoreFirstCanvasImage(creation, project, 0, localBitmapNodeIds('first-open-bitmap').size > 0)).toBe(false);
      expect(canRestoreFirstCanvasImage(creation, project, 0, localBitmapNodeIds('other-session').size > 0)).toBe(true);
    } finally { release(); }
  });
});
