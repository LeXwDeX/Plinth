import { describe, expect, it } from 'vitest';

import { isRestorable, readLastObject, restoreTarget, saveLastObject } from './last-object';
import type { VersionTile } from './versions';

function memory(): Pick<Storage, 'getItem' | 'setItem'> & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return { data, getItem: (key) => data.get(key) ?? null, setItem: (key, value) => { data.set(key, value); } };
}

const asset = (id: string, kind: 'image' | 'model') => ({ id, role: kind, variant: '', mediaType: '', ordinal: 0, url: `/a/${id}`, kind });
const tile = (key: string, taskId: string, patch: Partial<VersionTile> = {}): VersionTile => ({
  key, taskId, taskIds: [taskId], domain: 'image', version: 1, status: 'ready', progress: 1, capability: 'text-to-image',
  createdAt: '', assets: [asset(`${taskId}-a`, 'image'), asset(`${taskId}-b`, 'image')], thumbnailUrl: '', groupLabel: '', groupComplete: true,
  slots: [], views: [], parents: [], failure: '', origin: 'task', operation: '', captureId: '', size: null, ...patch,
});

describe('remembering the object on the stage', () => {
  it('round-trips per session', () => {
    const store = memory();
    saveLastObject('s1', { kind: 'image', taskId: 't1', assetId: 'a' }, store);
    saveLastObject('s2', { kind: 'model', taskId: 't9' }, store);
    expect(readLastObject('s1', store)).toEqual({ kind: 'image', taskId: 't1', assetId: 'a' });
    expect(readLastObject('s2', store)).toEqual({ kind: 'model', taskId: 't9' });
    expect(readLastObject('s3', store)).toBeNull();
  });

  it('does not remember the board composition but does remember a merged version', () => {
    expect(isRestorable({ taskId: 'canvas:s1' })).toBe(false);
    expect(isRestorable({ taskId: 'canvas:merged:n1' })).toBe(true);
    expect(isRestorable({ taskId: 't1' })).toBe(true);
    const store = memory();
    saveLastObject('s1', { kind: 'image', taskId: 'canvas:s1' }, store);
    expect(store.data.size).toBe(0);
  });

  it('survives blocked or broken storage', () => {
    const broken = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('blocked'); } };
    expect(() => saveLastObject('s1', { kind: 'image', taskId: 't1' }, broken)).not.toThrow();
    expect(readLastObject('s1', broken)).toBeNull();
    expect(readLastObject('s1', null)).toBeNull();
    const garbage = memory();
    garbage.setItem('studio.last-object.s1', '{nope');
    expect(readLastObject('s1', garbage)).toBeNull();
    garbage.setItem('studio.last-object.s1', JSON.stringify({ kind: 'sound', taskId: 't' }));
    expect(readLastObject('s1', garbage)).toBeNull();
  });
});

describe('restoreTarget', () => {
  const tiles = [tile('n:t1', 't1'), tile('n:t2', 't2'), tile('n:t3', 't3', { status: 'running' })];

  it('goes back to the remembered object and the candidate that was on the stage', () => {
    const target = restoreTarget(tiles, { saved: { kind: 'image', taskId: 't1', assetId: 't1-b' }, picked: { taskId: 't2' } });
    expect(target).toMatchObject({ source: 'saved', pick: { taskId: 't1', assetId: 't1-b' } });
    expect(target?.tile.key).toBe('n:t1');
  });

  it('falls back to the server pick when nothing is remembered or it is gone', () => {
    expect(restoreTarget(tiles, { saved: null, picked: { taskId: 't1', assetId: 't1-a' } })).toMatchObject({ source: 'picked', pick: { taskId: 't1', assetId: 't1-a' } });
    expect(restoreTarget(tiles, { saved: { kind: 'image', taskId: 'gone' }, picked: { taskId: 't2' } })?.source).toBe('picked');
  });

  it('falls back to the newest finished version, and ignores a remembered kind that no longer matches', () => {
    expect(restoreTarget(tiles, { saved: null, picked: null })).toMatchObject({ source: 'newest' });
    expect(restoreTarget(tiles, { saved: null, picked: null })?.tile.key).toBe('n:t2');
    expect(restoreTarget(tiles, { saved: { kind: 'model', taskId: 't1' }, picked: null })?.source).toBe('newest');
    expect(restoreTarget([tile('n:t3', 't3', { status: 'failed' })], { saved: null, picked: null })).toBeNull();
  });

  it('drops a remembered candidate the version no longer has', () => {
    expect(restoreTarget(tiles, { saved: { kind: 'image', taskId: 't1', assetId: 'missing' }, picked: null })?.pick).toEqual({ taskId: 't1' });
  });
});
