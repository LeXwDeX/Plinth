// @vitest-environment jsdom

import React from 'react';
import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AssetBoardDetail } from '@/components/studio/asset-board-detail';
import type { AssetListBoardItem } from '@/lib/asset-lists/client';
import type { ProductSchema } from '@/lib/product-schema';

vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({
    previewAssetUrl: (url: string) => url,
    proxyAssetUrl: (url: string) => url,
  }),
}));
vi.mock('@/components/viewer/model-viewer-3d', () => ({
  ModelViewer3D: () => <div data-testid="model-viewer-mock" />,
}));

const raw = defaultCatalog.application;
const zh = raw.content.messages as Record<string, string>;
const en = raw.localization.bundles.en.messages as Record<string, string>;
const chinese = raw as unknown as ProductSchema;
// The English interface: the en bundle over the default words, with the en plural rule.
const english = {
  ...raw,
  content: { ...raw.content, messages: { ...zh, ...en } },
  localization: { ...raw.localization, active_locale: 'en', default_locale: 'en' },
} as unknown as ProductSchema;

function item(overrides: Partial<AssetListBoardItem> = {}): AssetListBoardItem {
  return {
    id: 'item-1',
    name: '木椅',
    kind: 'prop',
    references: [],
    route_id: 'text-to-3d',
    route: [{ capability: 'text-to-3d', available: true }],
    planned_step_count: 1,
    planned_model3d_step_count: 1,
    state: 'review',
    version: 1,
    round: 1,
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    result: {},
    review: {},
    latest_review: null,
    ...overrides,
  } as AssetListBoardItem;
}

const judged = item({
  review: {
    model3d: {
      asset_id: 'model_a',
      checks: [
        { check: 'triangles', status: 'pass', actual: 12000, budget: 'max 30000' },
        { check: 'watertight', status: 'unknown', detail: 'read_error' },
        { check: 'uv', status: 'unknown', detail: 'some_future_code' },
      ],
    },
    image2d: {
      asset_id: 'img_a',
      checks: [
        { check: 'readable', status: 'unknown', detail: 'image_inspection_unavailable' },
        { check: 'short_edge', status: 'fail', actual: '512', detail: '实际 512，低于用途要求的短边 1024 像素。' },
      ],
    },
  },
} as Partial<AssetListBoardItem>);

afterEach(cleanup);

describe('AssetBoardDetail words (#670)', () => {
  it('names every check by the Schema, 3D and 2D alike', () => {
    render(<AssetBoardDetail item={judged} schema={chinese} onClose={() => {}} />);
    const drawer = screen.getByTestId('asset-board-detail');
    const names = within(drawer).getAllByText((_, node) => node?.tagName === 'SPAN' && node.className === 'font-medium')
      .map((node) => node.textContent);
    expect(names).toEqual([
      zh['ui.chat.review.check_names.triangles'],
      zh['ui.chat.review.check_names.watertight'],
      zh['ui.chat.review.check_names.uv'],
      zh['ui.chat.review.check_names.readable'],
      zh['ui.chat.review.check_names.short_edge'],
    ]);
    expect(drawer.textContent).not.toContain('watertight');
    expect(drawer.textContent).not.toContain('readable');
  });

  it('words a not-judged reason by its code, hides a code it has no words for and keeps a hint sentence', () => {
    render(<AssetBoardDetail item={judged} schema={chinese} onClose={() => {}} />);
    const detail = (testId: string) => within(screen.getByTestId(testId)).queryByTestId('asset-board-check-detail')?.textContent ?? null;
    expect(detail('asset-board-check-3d-watertight')).toBe(zh['ui.asset_list.board.check_detail.read_error']);
    expect(detail('asset-board-check-3d-uv')).toBeNull();
    expect(detail('asset-board-check-2d-readable')).toBe(zh['ui.asset_list.board.check_detail.image_inspection_unavailable']);
    expect(detail('asset-board-check-2d-short_edge')).toBe('实际 512，低于用途要求的短边 1024 像素。');
    expect(screen.getByTestId('asset-board-detail').textContent).not.toContain('read_error');
  });

  it('names where a crop came from instead of its source kind', () => {
    const cropped = item({
      planned_model3d_step_count: 0,
      source: { source_input_asset_id: 'in_1', source_kind: 'input_asset', source_width: 800, source_height: 600 },
    });
    render(<AssetBoardDetail item={cropped} schema={chinese} onClose={() => {}} />);
    const crop = screen.getByTestId('asset-board-detail-crop');
    expect(crop.textContent).toContain(zh['ui.asset_list.board.detail_crop_source_kind.input_asset']);
    expect(crop.textContent).not.toContain('input_asset');
  });

  it('says one step in the singular in English', () => {
    render(<AssetBoardDetail item={item({ steps: { used: 1 } } as Partial<AssetListBoardItem>)} schema={english} onClose={() => {}} />);
    expect(screen.getByTestId('asset-board-detail-steps').textContent).toBe('1 step used');
    cleanup();
    render(<AssetBoardDetail item={item({ steps: { used: 1 }, review: { steps: { model3d: 1 } } } as Partial<AssetListBoardItem>)} schema={english} onClose={() => {}} />);
    expect(screen.getByTestId('asset-board-detail').textContent).toContain(en['ui.asset_list.board.steps_3d_one'].replace('{steps}', '1'));
  });
});
