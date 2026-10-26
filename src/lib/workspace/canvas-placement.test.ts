import { describe, expect, it } from 'vitest';

import { imagesToPlace } from '@/lib/workspace/canvas-placement';
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
