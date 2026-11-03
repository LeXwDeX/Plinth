import { describe, expect, it } from 'vitest';

import { parseMultiviewRequest } from '@/lib/chat/browser-client';

// #65: a user message carries the structured multiview request it was sent with, so regenerating sends it again.
describe('parseMultiviewRequest', () => {
  it('reads the stored request back', () => {
    expect(parseMultiviewRequest({ group: 'run1', model: 'mv', views: [
      { view: 'front', task_id: 't1', asset_id: 'x1' }, { view: 'back', asset_id: 'up3' },
    ] })).toEqual({ group: 'run1', model: 'mv', views: [{ view: 'front', taskId: 't1', assetId: 'x1' }, { view: 'back', assetId: 'up3' }] });
    expect(parseMultiviewRequest({ model: 'mv', views: [{ view: 'front', asset_id: 'u1' }] })).toEqual({ model: 'mv', views: [{ view: 'front', assetId: 'u1' }] });
  });

  it('treats anything malformed as no request', () => {
    for (const value of [null, 'x', [], { views: [] }, { model: 'mv', views: [] }, { model: 'mv', views: [{ view: 'front' }] }, { model: 'mv', views: 'no' }]) {
      expect(parseMultiviewRequest(value)).toBeNull();
    }
  });
});
