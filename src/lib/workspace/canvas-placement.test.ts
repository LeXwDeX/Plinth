import { describe, expect, it } from 'vitest';

import { imagesToPlace, placementReceiptKey } from '@/lib/workspace/canvas-placement';
import { blankTile, type VersionTile } from '@/lib/workspace/versions';
import type { CurrentObject } from '@/store/workspace-store';

const result = (extra: Partial<CurrentObject> = {}): CurrentObject => ({
  taskId: 'task_a', kind: 'image', url: '/a.png', sessionId: 's1', ...extra,
});

describe('imagesToPlace (#239)', () => {
  it('places a new current object once, however often the board re-renders', () => {
    const placed = new WeakSet<CurrentObject>();
    const selection = result({ imageUrls: ['/a.png', '/b.png'] });
    expect(imagesToPlace(selection, 's1', placed)).toEqual(['/a.png', '/b.png']);
    // Re-render, autosave, a user delete: same selection object, nothing comes back.
    expect(imagesToPlace(selection, 's1', placed)).toEqual([]);
    expect(imagesToPlace(selection, 's1', placed)).toEqual([]);
  });

  it('does not auto-insert an inpaint raw result, but an explicit choice still can', () => {
    const suppressed = new Set(['task_a']);
    const quiet = result({ quiet: true });
    const explicit = result();
    expect(imagesToPlace(quiet, 's1', new WeakSet(), suppressed)).toEqual([]);
    expect(imagesToPlace(explicit, 's1', new WeakSet(), suppressed)).toEqual(['/a.png']);
  });

  it('keeps suppressing a quiet inpaint result after its job is gone once the receipt is saved', () => {
    const placed = new WeakSet<CurrentObject>();
    const receipt = ['since:2026-10-19T00:00:00Z', 'task_a#asset_a'];
    const quiet = result({ quiet: true, assetId: 'asset_a' });
    expect(imagesToPlace(quiet, 's1', placed, undefined, receipt)).toEqual([]);
    // The suppressed quiet event is spent; a re-render does not reconsider the same object.
    expect(imagesToPlace(quiet, 's1', placed, undefined, receipt)).toEqual([]);
    // A person's explicit choice (a new, non-quiet object) is still allowed.
    expect(imagesToPlace(result({ assetId: 'asset_a' }), 's1', placed, undefined, receipt)).toEqual(['/a.png']);
  });

  it('does not let a quiet result escape when only its task is suppressed and the receipt has no asset id match', () => {
    const placed = new WeakSet<CurrentObject>();
    const quiet = result({ quiet: true });
    expect(imagesToPlace(quiet, 's1', placed, new Set(['task_a']), null)).toEqual([]);
    expect(imagesToPlace(quiet, 's1', placed, new Set(['task_a']), null)).toEqual([]);
  });

  it('places again only when the user chooses the result again (a new selection)', () => {
    const placed = new WeakSet<CurrentObject>();
    imagesToPlace(result(), 's1', placed);
    expect(imagesToPlace(result(), 's1', placed)).toEqual(['/a.png']);
  });

  it('never places a selection restored when the session opens', () => {
    expect(imagesToPlace(result({ restored: true }), 's1', new WeakSet())).toEqual([]);
  });

  it('ignores models, merged canvas objects and other sessions', () => {
    const placed = new WeakSet<CurrentObject>();
    expect(imagesToPlace(result({ kind: 'model' }), 's1', placed)).toEqual([]);
    expect(imagesToPlace(result({ taskId: 'canvas:merged-1' }), 's1', placed)).toEqual([]);
    expect(imagesToPlace(result({ sessionId: 's2' }), 's1', placed)).toEqual([]);
    expect(imagesToPlace(null, 's1', placed)).toEqual([]);
  });
});

describe('looking at a version and inserting it (#559)', () => {
  const view = (extra: Partial<CurrentObject> = {}) => result({ placement: 'view', assetId: 'asset_a', ...extra });

  it('does not put a looked-at picture on the board, so a deleted layer does not come back', () => {
    const placed = new WeakSet<CurrentObject>();
    // The person deleted the layer earlier; the board still holds other content.
    expect(imagesToPlace(view(), 's1', placed, undefined, ['since:x', 'task_a#asset_a'], false)).toEqual([]);
    // Looking again is a new object, still not placed.
    expect(imagesToPlace(view(), 's1', placed, undefined, ['since:x', 'task_a#asset_a'], false)).toEqual([]);
  });

  it('shows the one looked-at picture when the board is empty, never a whole candidate group', () => {
    const selection = view({ imageUrls: ['/a.png', '/b.png'] });
    const placed = new WeakSet<CurrentObject>();
    expect(imagesToPlace(selection, 's1', placed, undefined, null, true)).toEqual(['/a.png']);
    // The event is spent: the board emptying again later does not bring it back.
    expect(imagesToPlace(selection, 's1', placed, undefined, null, true)).toEqual([]);
  });

  it('spends a looked-at picture on a busy board, so the board emptying later does not place it', () => {
    const selection = view();
    const placed = new WeakSet<CurrentObject>();
    expect(imagesToPlace(selection, 's1', placed, undefined, null, false)).toEqual([]);
    expect(imagesToPlace(selection, 's1', placed, undefined, null, true)).toEqual([]);
  });

  it('inserts exactly the chosen picture when the person asks for it, even after it was deleted', () => {
    const insert = result({ placement: 'insert', assetId: 'asset_a', imageUrls: ['/a.png', '/b.png'] });
    expect(imagesToPlace(insert, 's1', new WeakSet(), undefined, ['since:x', 'task_a#asset_a'], false)).toEqual(['/a.png']);
  });

  it('keeps the earlier rules for a choice (选用) and for models', () => {
    expect(imagesToPlace(result({ imageUrls: ['/a.png', '/b.png'] }), 's1', new WeakSet(), undefined, null, false)).toEqual(['/a.png', '/b.png']);
    expect(imagesToPlace(result({ kind: 'model', placement: 'insert' }), 's1', new WeakSet())).toEqual([]);
    expect(imagesToPlace(result({ kind: 'model', placement: 'view' }), 's1', new WeakSet(), undefined, null, true)).toEqual([]);
    expect(imagesToPlace(view({ restored: true }), 's1', new WeakSet(), undefined, null, true)).toEqual([]);
  });
});

describe('placementReceiptKey (#559)', () => {
  const picture = (patch: Partial<VersionTile>) => blankTile({ key: 'n:t', taskId: 't', taskIds: ['t'], domain: 'image', status: 'ready',
    assets: [{ id: 'asset_a', kind: 'image', url: '/a.png', role: 'image', variant: '', mediaType: 'image/png', ordinal: 0 }], ...patch });

  it('names the result as the automatic join registers it, under the version task', () => {
    expect(placementReceiptKey([picture({ taskId: 'front', taskIds: ['front', 'side'] })], { assetId: 'asset_a' })).toBe('front#asset_a');
  });

  it('needs no receipt for what is never joined automatically', () => {
    expect(placementReceiptKey([picture({ origin: 'upload' })], { assetId: 'asset_a' })).toBeNull();
    expect(placementReceiptKey([picture({ origin: 'capture' })], { assetId: 'asset_a' })).toBeNull();
    expect(placementReceiptKey([picture({})], {})).toBeNull();
    expect(placementReceiptKey([picture({})], { assetId: 'other' })).toBeNull();
  });
});
