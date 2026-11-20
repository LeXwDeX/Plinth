import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import {
  acceptedBoardItems,
  boardCropGroups,
  boardCropItems,
  boardDownloadScope,
  boardPollingActive,
  returnReasonsForKind,
} from '@/lib/asset-lists/board';
import type { AssetListBoard, AssetListBoardItem } from '@/lib/asset-lists/client';
import type { ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;

function item(id: string, overrides: Partial<AssetListBoardItem> = {}): AssetListBoardItem {
  return {
    id,
    kind: 'character',
    name: id,
    references: [],
    route_id: 'text-to-3d',
    route: [],
    planned_step_count: 1,
    planned_model3d_step_count: 1,
    state: 'review',
    version: 1,
    round: 1,
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    ...overrides,
  };
}

function board(items: AssetListBoardItem[], runStatus?: string): AssetListBoard {
  return {
    list: {
      id: 'al_1',
      project_id: 'prj_1',
      title: 'Hero set',
      status: 'confirmed',
      effective_status: 'confirmed',
      item_count: items.length,
      planned_step_count: 1,
      planned_model3d_step_count: 1,
      expires_at: '2026-12-01T00:00:00Z',
      created_at: '2026-11-18T00:00:00Z',
      updated_at: '2026-11-18T00:00:00Z',
      version: 3,
      run: runStatus
        ? {
          status: runStatus,
          version: '2026-11-18T00:00:00Z',
          item_counts: { todo: 0, working: 0, review: 0, accepted: 0, returned: 0, failed: 0, skipped: 0 },
        }
        : null,
    },
    items,
  };
}

describe('asset list board helpers (#329d)', () => {
  it('filters the return reasons by the item kind and keeps the general ones', () => {
    const image = returnReasonsForKind(schema, 'image').map((reason) => reason.id);
    expect(image).toEqual(['style', 'proportion', 'details', 'reference', 'other']);

    const character = returnReasonsForKind(schema, 'character').map((reason) => reason.id);
    expect(character).toContain('topology');
    expect(character).toContain('texture');
    expect(character).toContain('rig');
  });

  it('groups crop items by their source picture and skips items without both parts', () => {
    const crop = (id: string, source: string, x: number) => item(id, {
      source: { source_input_asset_id: source, source_width: 1024, source_height: 512 },
      region: { x, y: 0.1, width: 0.2, height: 0.2 },
    });
    const items = [crop('a', 'src_1', 0), crop('b', 'src_1', 0.3), crop('c', 'src_2', 0.1), item('plain')];

    expect(boardCropItems(items).map((entry) => entry.id)).toEqual(['a', 'b', 'c']);
    const groups = boardCropGroups(items);
    expect(groups.map((group) => [group.sourceAssetID, group.items.map((entry) => entry.id)])).toEqual([
      ['src_1', ['a', 'b']],
      ['src_2', ['c']],
    ]);
  });

  it('polls while the run produces or an item is still working or waiting', () => {
    expect(boardPollingActive(board([item('a', { state: 'working' })]))).toBe(true);
    expect(boardPollingActive(board([item('a', { state: 'review' })]))).toBe(true);
    expect(boardPollingActive(board([item('a', { state: 'accepted' })], 'running'))).toBe(true);
    expect(boardPollingActive(board([item('a', { state: 'accepted' })], 'completed'))).toBe(false);
    expect(boardPollingActive(board([item('a', { state: 'failed' })], 'stopped'))).toBe(false);
    expect(boardPollingActive(null)).toBe(false);
  });

  it('narrows a batch download to the selected accepted items, then to the filter', () => {
    const items = [
      item('accepted_a', { state: 'accepted' }),
      item('accepted_b', { state: 'accepted' }),
      item('reviewed', { state: 'review' }),
    ];
    expect(acceptedBoardItems(items).map((entry) => entry.id)).toEqual(['accepted_a', 'accepted_b']);
    expect(boardDownloadScope(items, 'all', new Set(['accepted_b', 'reviewed']))).toEqual(['accepted_b']);
    expect(boardDownloadScope(items, 'accepted', new Set())).toEqual(['accepted_a', 'accepted_b']);
    expect(boardDownloadScope(items, 'review', new Set())).toEqual([]);
  });
});