import { describe, expect, it } from 'vitest';

import type { AssetListSummary } from '@/lib/asset-lists/client';
import { assetListPlacement } from './asset-list-placement';

function summary(overrides: Partial<AssetListSummary> = {}): AssetListSummary {
  return {
    id: 'list_1',
    project_id: 'prj_1',
    title: 'List',
    status: 'draft',
    effective_status: 'draft',
    item_count: 1,
    planned_step_count: 1,
    planned_model3d_step_count: 0,
    expires_at: '2026-11-25T00:00:00Z',
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    ...overrides,
  };
}

describe('asset list placement', () => {
  it('follows the message named as the origin when it is on the page', () => {
    const placement = assetListPlacement([summary({ id: 'a', origin_message_id: 'm1' })], ['m1', 'm2']);
    expect(placement.byMessage.get('m1')?.map((list) => list.id)).toEqual(['a']);
    expect(placement.loose).toEqual([]);
  });

  it('puts a list whose origin message is not shown at the end', () => {
    const placement = assetListPlacement([summary({ id: 'a', origin_message_id: 'older' })], ['m1']);
    expect(placement.byMessage.size).toBe(0);
    expect(placement.loose.map((list) => list.id)).toEqual(['a']);
  });

  it('puts a list with no origin message at the end', () => {
    const placement = assetListPlacement([summary({ id: 'a', origin_message_id: undefined })], ['m1']);
    expect(placement.byMessage.size).toBe(0);
    expect(placement.loose.map((list) => list.id)).toEqual(['a']);
  });

  it('keeps a decided list visible at the end when its origin is gone', () => {
    const placement = assetListPlacement([
      summary({ id: 'done', status: 'confirmed', effective_status: 'confirmed', origin_message_id: 'older' }),
    ], []);
    expect(placement.loose.map((list) => list.id)).toEqual(['done']);
  });

  it('orders lists by creation time, both at the end and inside one message', () => {
    const placement = assetListPlacement([
      summary({ id: 'late', created_at: '2026-11-18T03:00:00Z' }),
      summary({ id: 'early', created_at: '2026-11-18T01:00:00Z', origin_message_id: 'm1' }),
      summary({ id: 'middle', created_at: '2026-11-18T02:00:00Z', origin_message_id: 'm1' }),
    ], ['m1']);
    expect(placement.byMessage.get('m1')?.map((list) => list.id)).toEqual(['early', 'middle']);
    expect(placement.loose.map((list) => list.id)).toEqual(['late']);
  });
});