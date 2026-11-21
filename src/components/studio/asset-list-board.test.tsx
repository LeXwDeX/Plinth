// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AssetListBoard } from '@/components/studio/asset-list-board';
import { AssetListError, type AssetListBoard as AssetListBoardData, type AssetListBoardItem, type AssetListClient } from '@/lib/asset-lists/client';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { useInlineModelPreviewStore } from '@/store/inline-model-preview-store';
import { useStudioStore } from '@/store/studio-store';

vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({
    previewAssetUrl: (url: string) => url,
    proxyAssetUrl: (url: string) => url,
  }),
}));

// The browser save is a DOM side effect; the board's contract is the archive
// bytes and filename it hands over.
const saved = vi.hoisted(() => ({ calls: [] as Array<{ blob: Blob; filename: string }> }));
vi.mock('@/lib/workspace/batch-export', () => ({
  saveArchive: (blob: Blob, filename: string) => { saved.calls.push({ blob, filename }); },
}));

// The real viewer needs WebGL; the board's contract is only which cell holds
// the one mounted viewer.
vi.mock('@/components/viewer/model-viewer-3d', () => ({
  ModelViewer3D: ({ src, alt }: { src: string; alt?: string }) => (
    <div data-testid="model-viewer-mock" data-src={src} aria-label={alt} />
  ),
}));

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const text = (key: string) => schemaText(schema, key);

function boardItem(overrides: Partial<AssetListBoardItem> & Pick<AssetListBoardItem, 'id' | 'name'>): AssetListBoardItem {
  return {
    kind: 'character',
    references: [],
    route_id: 'text-to-3d',
    route: [{ capability: 'text-to-3d', available: true }],
    planned_step_count: 2,
    planned_model3d_step_count: 2,
    state: 'review',
    version: 1,
    round: 1,
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    result: {},
    review: {},
    latest_review: null,
    ...overrides,
  };
}

function board(items: AssetListBoardItem[]): AssetListBoardData {
  return {
    list: {
      id: 'listid',
      project_id: 'proj',
      title: '英雄套装',
      status: 'confirmed',
      effective_status: 'confirmed',
      item_count: items.length,
      planned_step_count: 8,
      planned_model3d_step_count: 4,
      expires_at: '2026-12-01T00:00:00Z',
      created_at: '2026-11-18T00:00:00Z',
      updated_at: '2026-11-18T00:00:00Z',
      version: 3,
    },
    items,
  };
}

function boardFixture(): AssetListBoardData {
  return board([
    boardItem({
      id: 'item-2d',
      name: '图标',
      kind: 'image',
      state: 'review',
      representative_asset_id: 'img_2d',
      result: { task_id: 'task_2d', assets: [{ id: 'img_2d', role: 'image', name: 'image' }] },
      review: {
        captured_at: '2026-11-18T00:00:00Z',
        round: 1,
        task_id: 'task_2d',
        cover_asset_id: 'img_2d',
        artifacts: [{ asset_id: 'img_2d', role: 'image', media_type: 'image/png', size: 2048 }],
        image2d: {
          asset_id: 'img_2d',
          width: 1024,
          height: 1024,
          checks: [
            { check: 'short_edge', status: 'fail', actual: 512, budget: 'min 1024', detail: 'too_small' },
            { check: 'alpha', status: 'pass', actual: true },
          ],
        },
        self_check: { status: 'needs_work', summary: '边缘需要修', remaining: ['补背景'] },
        steps: { used: 3, model3d: 0 },
      },
    }),
    boardItem({
      id: 'item-model-a',
      name: '主角',
      state: 'accepted',
      round: 2,
      representative_asset_id: 'model_a',
      result: { task_id: 'task_3d_a', assets: [{ id: 'model_a', role: 'model', name: 'model' }, { id: 'cover_a', role: 'image', name: 'image' }] },
      review: {
        round: 2,
        task_id: 'task_3d_a',
        cover_asset_id: 'cover_a',
        artifacts: [
          { asset_id: 'model_a', role: 'model', media_type: 'model/gltf-binary', size: 4096 },
          { asset_id: 'cover_a', role: 'image', media_type: 'image/png', size: 1024 },
        ],
        model3d: { asset_id: 'model_a', checks: [{ check: 'triangles', status: 'pass', actual: 12000, budget: 'max 30000' }] },
        steps: { used: 6, model3d: 4 },
      },
      latest_review: {
        id: 'rev_a', round: 2, verdict: 'approved', note: '可以', created_at: '2026-11-18T01:00:00Z',
      },
    }),
    boardItem({
      id: 'item-model-b',
      name: '长剑',
      state: 'review',
      representative_asset_id: 'model_b',
      result: { task_id: 'task_3d_b', assets: [{ id: 'model_b', role: 'model' }] },
      review: {
        round: 1,
        task_id: 'task_3d_b',
        artifacts: [{ asset_id: 'model_b', role: 'model', media_type: 'model/gltf-binary' }],
        model3d: {
          asset_id: 'model_b',
          checks: [{ check: 'textures', status: 'unknown', detail: 'no_glb' }],
        },
        steps: { used: 12, model3d: 12, cap: 20, mode: 'cap' },
      },
    }),
    boardItem({
      id: 'item-returned',
      name: '盾牌',
      state: 'returned',
      representative_asset_id: 'img_d',
      result: { task_id: 'task_d', assets: [{ id: 'img_d', role: 'image' }] },
      reference_asset_id: 'input_1',
      source: { source_input_asset_id: 'input_1', source_kind: 'upload', source_width: 2048, source_height: 1024 },
      region: { x: 0.25, y: 0.125, width: 0.5, height: 0.25, pixel_rect: { x0: 512, y0: 128, x1: 1536, y1: 384 } },
      review: {
        round: 1,
        task_id: 'task_d',
        cover_asset_id: 'img_d',
        artifacts: [{ asset_id: 'img_d', role: 'image', media_type: 'image/png', size: 512 }],
        image2d: { checks: [{ check: 'subject_ratio', status: 'pass', actual: 0.6 }] },
        steps: { used: 2, model3d: 0 },
      },
      latest_review: {
        id: 'rev_d', round: 1, verdict: 'returned', reasons: ['style', 'details'], note: '参考图偏冷色', created_at: '2026-11-18T02:00:00Z',
      },
    }),
    boardItem({
      id: 'item-skipped',
      name: '备用',
      kind: 'image',
      state: 'skipped',
      planned_step_count: 1,
      planned_model3d_step_count: 0,
      review: {},
    }),
  ]);
}

function clientWith(
  getBoard: ReturnType<typeof vi.fn>,
  overrides: Partial<AssetListClient> = {},
): AssetListClient {
  return { getBoard, ...overrides } as unknown as AssetListClient;
}

function renderBoard(
  value: AssetListBoardData | Error,
  overrides: { getBoard?: ReturnType<typeof vi.fn>; client?: Partial<AssetListClient>; pollIntervalMs?: number } = {},
) {
  const getBoard = overrides.getBoard ?? vi.fn(async () => {
    if (value instanceof Error) throw value;
    return value;
  });
  render(
    <AssetListBoard
      target={{ projectId: 'proj', listId: 'listid', title: '英雄套装' }}
      schema={schema}
      client={clientWith(getBoard, overrides.client)}
      pollIntervalMs={overrides.pollIntervalMs}
    />,
  );
  return { getBoard };
}

beforeEach(() => {
  saved.calls = [];
  useInlineModelPreviewStore.setState({ taskId: null });
  useStudioStore.setState({ assetBoard: null });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('AssetListBoard (#329)', () => {
  it('renders every item with its name, state, check summary, steps and round', async () => {
    renderBoard(boardFixture());
    await waitFor(() => expect(screen.getByTestId('asset-board-grid')).toBeTruthy());

    expect(screen.getByTestId('asset-board-title').textContent).toBe('英雄套装');
    expect(screen.getByTestId('asset-board-status').textContent).toBe('已确认');
    expect(screen.getByTestId('asset-board-count').textContent).toBe('5 项');
    expect(screen.getByTestId('asset-board-grid').children).toHaveLength(5);

    const review = screen.getByTestId('asset-board-cell-item-2d');
    expect(review.textContent).toContain('图标');
    expect(review.textContent).toContain('待验收');
    expect(review.textContent).toContain(messages['ui.asset_list.board.checks_summary_fail'].replace('{fail}', '1'));
    expect(review.textContent).toContain(messages['ui.asset_list.board.steps'].replace('{steps}', '3'));
    expect(review.textContent).toContain(messages['ui.asset_list.board.round'].replace('{round}', '1'));
    // The Agent's own self-check conclusion is summarized on the cell too.
    expect(screen.getByTestId('asset-board-self-check-item-2d').textContent).toContain('边缘需要修');

    // A cap turns the step line into used / cap (#330 wording).
    expect(screen.getByTestId('asset-board-steps-item-model-b').textContent)
      .toBe(messages['ui.asset_list.board.steps_capped'].replace('{used}', '12').replace('{cap}', '20'));
    // Unknown is shown as its own bucket, never as a pass.
    expect(screen.getByTestId('asset-board-checks-item-model-b').textContent)
      .toContain(messages['ui.asset_list.board.check_unknown']);
  });

  it('filters by item state and keeps the counts on the buttons', async () => {
    renderBoard(boardFixture());
    await waitFor(() => expect(screen.getByTestId('asset-board-filter-all')).toBeTruthy());

    expect(screen.getByTestId('asset-board-filter-all').textContent).toContain('5');
    expect(screen.getByTestId('asset-board-filter-review').textContent).toContain('2');
    expect(screen.getByTestId('asset-board-filter-accepted').textContent).toContain('1');
    expect(screen.getByTestId('asset-board-filter-failed').textContent).toContain('0');

    fireEvent.click(screen.getByTestId('asset-board-filter-review'));
    expect(screen.getByTestId('asset-board-grid').children).toHaveLength(2);
    expect(screen.getByTestId('asset-board-cell-item-2d')).toBeTruthy();
    expect(screen.getByTestId('asset-board-cell-item-model-b')).toBeTruthy();
    expect(screen.queryByTestId('asset-board-cell-item-model-a')).toBeNull();

    fireEvent.click(screen.getByTestId('asset-board-filter-returned'));
    expect(screen.getByTestId('asset-board-grid').children).toHaveLength(1);
    expect(screen.getByTestId('asset-board-cell-item-returned')).toBeTruthy();

    fireEvent.click(screen.getByTestId('asset-board-filter-skipped'));
    expect(screen.getByTestId('asset-board-grid').children).toHaveLength(1);
    expect(screen.getByTestId('asset-board-cell-item-skipped')).toBeTruthy();

    fireEvent.click(screen.getByTestId('asset-board-filter-failed'));
    expect(screen.getByTestId('asset-board-empty-filter').textContent).toBe(text('ui.asset_list.board.empty_filter'));

    fireEvent.click(screen.getByTestId('asset-board-filter-all'));
    expect(screen.getByTestId('asset-board-grid').children).toHaveLength(5);
  });

  it('keeps one 3D viewer alive: opening another cell stops the previous one', async () => {
    renderBoard(boardFixture());
    await waitFor(() => expect(screen.getByTestId('asset-board-3d-item-model-a')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-3d-item-model-a'));
    expect(within(screen.getByTestId('asset-board-item-item-model-a')).getAllByTestId('model-viewer-mock')).toHaveLength(1);
    expect(screen.getAllByTestId('model-viewer-mock')).toHaveLength(1);
    expect(within(screen.getByTestId('asset-board-preview-item-model-a')).getByTestId('model-viewer-mock').getAttribute('data-src'))
      .toBe('/api/v1/tasks/task_3d_a/assets/model_a');

    fireEvent.click(screen.getByTestId('asset-board-3d-item-model-b'));
    expect(screen.getAllByTestId('model-viewer-mock')).toHaveLength(1);
    expect(within(screen.getByTestId('asset-board-item-item-model-b')).getAllByTestId('model-viewer-mock')).toHaveLength(1);
    expect(screen.queryByTestId('asset-board-preview-item-model-a')).toBeNull();
    expect(screen.getByTestId('asset-board-3d-item-model-b').getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByTestId('asset-board-3d-item-model-a').getAttribute('aria-pressed')).toBe('false');
  });

  it('opens the detail drawer with checks, artifacts, crop provenance and the previous decision', async () => {
    renderBoard(boardFixture());
    await waitFor(() => expect(screen.getByTestId('asset-board-cell-item-returned')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-cell-item-returned'));
    const drawer = await waitFor(() => screen.getByTestId('asset-board-detail'));
    expect(within(drawer).getByText('盾牌')).toBeTruthy();
    // Acceptance criteria with actual value.
    expect(within(drawer).getByTestId('asset-board-check-2d-subject_ratio').textContent)
      .toContain(messages['ui.asset_list.board.detail_actual'].replace('{actual}', '0.6'));
    // Artifact and its download.
    expect(within(drawer).getByTestId('asset-board-artifact-img_d').textContent).toContain('image/png');
    expect(within(drawer).getByTestId('asset-board-artifact-download-img_d').getAttribute('href'))
      .toBe('/api/v1/tasks/task_d/assets/img_d');
    // Crop source and region.
    expect(within(drawer).getByTestId('asset-board-detail-crop-source')).toBeTruthy();
    expect(within(drawer).getByTestId('asset-board-detail-crop-region-text').textContent)
      .toContain(messages['ui.asset_list.board.detail_crop_region']
        .replace('{x}', '0.25').replace('{y}', '0.125').replace('{width}', '0.5').replace('{height}', '0.25'));
    // The previous round's reasons come from the declared labels.
    expect(within(drawer).getByTestId('asset-board-detail-decision').textContent).toContain('风格不对');
    expect(within(drawer).getByTestId('asset-board-detail-decision').textContent).toContain('细节崩坏');
    expect(within(drawer).getByTestId('asset-board-detail-decision').textContent).toContain('参考图偏冷色');

    fireEvent.click(within(drawer).getByLabelText(text('ui.asset_list.board.detail_close')));
    expect(screen.queryByTestId('asset-board-detail')).toBeNull();
  });

  it('shows a text empty state for a list without items', async () => {
    renderBoard(board([]));
    await waitFor(() => expect(screen.getByTestId('asset-board-empty')).toBeTruthy());
    expect(screen.getByTestId('asset-board-empty').textContent).toBe(text('ui.asset_list.board.empty'));
    expect(screen.queryByTestId('asset-board-grid')).toBeNull();
  });

  it('shows a text error with a retry that reads again', async () => {
    const getBoard = vi.fn()
      .mockRejectedValueOnce(new AssetListError('conflict', 409, { code: 'conflict', detail: 'error.conflict' }))
      .mockResolvedValueOnce(boardFixture());
    renderBoard(boardFixture(), { getBoard });

    await waitFor(() => expect(screen.getByTestId('asset-board-error')).toBeTruthy());
    expect(screen.getByTestId('asset-board-error').textContent).toContain(messages['error.conflict']);
    fireEvent.click(screen.getByTestId('asset-board-retry'));
    await waitFor(() => expect(screen.getByTestId('asset-board-grid')).toBeTruthy());
    expect(getBoard).toHaveBeenCalledTimes(2);
  });

  it('keeps every cell reachable by keyboard: the cell is a tabbable button', async () => {
    renderBoard(boardFixture());
    await waitFor(() => expect(screen.getByTestId('asset-board-cell-item-2d')).toBeTruthy());

    const cell = screen.getByTestId('asset-board-cell-item-2d') as HTMLButtonElement;
    expect(cell.tagName).toBe('BUTTON');
    expect(cell.tabIndex).toBe(0);
    cell.focus();
    expect(document.activeElement).toBe(cell);
    // Opening through the button's own click is what Enter and Space fire on a native button.
    fireEvent.click(cell);
    expect(screen.getByTestId('asset-board-detail')).toBeTruthy();
  });
});

function withRun(value: AssetListBoardData, status: string): AssetListBoardData {
  return {
    ...value,
    list: {
      ...value.list,
      run: {
        status,
        version: '2026-11-18T01:00:00Z',
        item_counts: { todo: 0, working: 0, review: 2, accepted: 1, returned: 0, failed: 0, skipped: 0 },
      },
    },
  };
}

function compareBoard(): AssetListBoardData {
  const source = { source_input_asset_id: 'input_1', source_kind: 'upload', source_width: 2048, source_height: 1024 };
  return board([
    boardItem({
      id: 'crop-accepted',
      name: '主角',
      state: 'accepted',
      source,
      region: { x: 0.1, y: 0.1, width: 0.3, height: 0.3 },
      review: {
        task_id: 'task_c1',
        cover_asset_id: 'cover_c1',
        artifacts: [{ asset_id: 'cover_c1', role: 'image', media_type: 'image/png' }],
      },
    }),
    boardItem({
      id: 'crop-review',
      name: '长剑',
      state: 'review',
      source,
      region: { x: 0.5, y: 0.2, width: 0.2, height: 0.4 },
      review: {
        task_id: 'task_c2',
        cover_asset_id: 'cover_c2',
        artifacts: [{ asset_id: 'cover_c2', role: 'image', media_type: 'image/png' }],
      },
    }),
    boardItem({ id: 'plain', name: '图标', kind: 'image', state: 'failed' }),
  ]);
}

describe('AssetListBoard board actions (#329d)', () => {
  it('accepts the selected reviewed items in one batch with the list version', async () => {
    const value = boardFixture();
    const review = vi.fn(async () => ({
      list: { ...value.list, version: 4, brief: {}, items: [] },
      items: [],
    }));
    renderBoard(value, { client: { review } as Partial<AssetListClient> });
    await waitFor(() => expect(screen.getByTestId('asset-board-accept-selected')).toBeTruthy());

    const accept = screen.getByTestId('asset-board-accept-selected') as HTMLButtonElement;
    expect(accept.disabled).toBe(true);

    fireEvent.click(screen.getByTestId('asset-board-select-item-2d'));
    fireEvent.click(screen.getByTestId('asset-board-select-item-model-b'));
    expect(accept.disabled).toBe(false);
    expect(accept.textContent).toContain('2');
    expect(screen.getByTestId('asset-board-selected-count').textContent)
      .toBe(messages['ui.asset_list.board.selected_count'].replace('{count}', '2'));

    fireEvent.click(accept);
    await waitFor(() => expect(review).toHaveBeenCalledTimes(1));
    expect(review).toHaveBeenCalledWith('proj', 'listid', {
      decision: 'accept',
      item_ids: ['item-2d', 'item-model-b'],
      expected_version: 3,
    });
    await waitFor(() => expect(screen.getByTestId('asset-board-selected-count').textContent)
      .toBe(messages['ui.asset_list.board.selected_count'].replace('{count}', '0')));
  });

  // A stale list version (409) is not a per-item refusal: the board re-reads
  // and says so, and the person decides again on the refreshed board.
  it('re-reads the board and explains a stale 409', async () => {
    const value = boardFixture();
    const getBoard = vi.fn(async () => value);
    const review = vi.fn(async () => {
      throw new AssetListError('conflict', 409, { code: 'conflict', detail: '当前状态不允许执行此操作' });
    });
    renderBoard(value, { getBoard, client: { review } as Partial<AssetListClient> });
    await waitFor(() => expect(screen.getByTestId('asset-board-select-item-2d')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-select-item-2d'));
    fireEvent.click(screen.getByTestId('asset-board-accept-selected'));

    const alert = await waitFor(() => screen.getByTestId('asset-board-action-error'));
    expect(alert.textContent).toContain(messages['ui.asset_list.board.stale']);
    expect(alert.textContent).toContain('当前状态不允许执行此操作');
    await waitFor(() => expect(getBoard).toHaveBeenCalledTimes(2));
    // The selection survives the reload; nothing was decided.
    expect(screen.getByTestId('asset-board-selected-count').textContent)
      .toBe(messages['ui.asset_list.board.selected_count'].replace('{count}', '1'));
  });

  it('keeps the other accepts when one item is refused by the server', async () => {
    const value = boardFixture();
    const review = vi.fn(async () => ({
      list: { ...value.list, version: 4, brief: {}, items: [] },
      items: [
        { item_id: 'item-2d', verdict: 'approved', project_asset_id: 'pa_2d' },
        { item_id: 'item-model-b', error: '项目资产已达上限 200 个' },
      ],
    }));
    renderBoard(value, { client: { review } as Partial<AssetListClient> });
    await waitFor(() => expect(screen.getByTestId('asset-board-select-item-2d')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-select-item-2d'));
    fireEvent.click(screen.getByTestId('asset-board-select-item-model-b'));
    fireEvent.click(screen.getByTestId('asset-board-accept-selected'));

    const alert = await waitFor(() => screen.getByTestId('asset-board-action-error'));
    expect(alert.textContent).toContain(messages['ui.asset_list.board.accept_partial']);
    expect(alert.textContent).toContain('长剑');
    expect(alert.textContent).toContain('项目资产已达上限 200 个');
    // Only the refused item stays selected for the retry.
    expect(screen.getByTestId('asset-board-selected-count').textContent)
      .toBe(messages['ui.asset_list.board.selected_count'].replace('{count}', '1'));
  });

  it('returns one item only after a reason is picked and sends it with the note', async () => {
    const value = boardFixture();
    const review = vi.fn(async () => ({
      list: { ...value.list, version: 4, brief: {}, items: [] },
      items: [{ item_id: 'item-2d', verdict: 'returned' }],
    }));
    renderBoard(value, { client: { review } as Partial<AssetListClient> });
    await waitFor(() => expect(screen.getByTestId('asset-board-return-item-2d')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-return-item-2d'));
    const dialog = await waitFor(() => screen.getByTestId('asset-board-return-dialog'));
    expect(dialog.textContent).toContain('打回「图标」');
    // The image kind sees the general reasons, never the 3D-only ones.
    for (const id of ['style', 'proportion', 'details', 'reference', 'other']) {
      expect(within(dialog).getByTestId(`asset-board-return-reason-${id}`)).toBeTruthy();
    }
    for (const id of ['topology', 'texture', 'rig']) {
      expect(within(dialog).queryByTestId(`asset-board-return-reason-${id}`)).toBeNull();
    }

    const submit = within(dialog).getByTestId('asset-board-return-submit') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    fireEvent.click(within(dialog).getByTestId('asset-board-return-reason-style'));
    expect(submit.disabled).toBe(false);
    fireEvent.change(within(dialog).getByTestId('asset-board-return-note'), { target: { value: '参考图偏冷' } });
    fireEvent.click(submit);

    await waitFor(() => expect(review).toHaveBeenCalledWith('proj', 'listid', {
      decision: 'return',
      item_ids: ['item-2d'],
      reason_ids: ['style'],
      note: '参考图偏冷',
      expected_version: 3,
    }));
    await waitFor(() => expect(screen.queryByTestId('asset-board-return-dialog')).toBeNull());
  });

  it('shows the 3D-only reasons for a character item', async () => {
    renderBoard(boardFixture());
    await waitFor(() => expect(screen.getByTestId('asset-board-return-item-model-b')).toBeTruthy());
    fireEvent.click(screen.getByTestId('asset-board-return-item-model-b'));
    const dialog = await waitFor(() => screen.getByTestId('asset-board-return-dialog'));
    for (const id of ['topology', 'texture', 'rig']) {
      expect(within(dialog).getByTestId(`asset-board-return-reason-${id}`)).toBeTruthy();
    }
  });

  it('downloads the accepted batch through the existing save trigger', async () => {
    const value = boardFixture();
    const downloadArchive = vi.fn(async () => ({
      blob: new Blob(['zip-bytes'], { type: 'application/zip' }),
      filename: 'asset-list-listid.zip',
    }));
    renderBoard(value, { client: { downloadArchive } as Partial<AssetListClient> });
    await waitFor(() => expect(screen.getByTestId('asset-board-download')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-download'));
    await waitFor(() => expect(downloadArchive).toHaveBeenCalledWith('proj', 'listid', []));
    await waitFor(() => expect(saved.calls).toHaveLength(1));
    expect(saved.calls[0].filename).toBe('asset-list-listid.zip');
  });

  it('offers a narrowed retry after a 413 and downloads only the selection', async () => {
    // Two accepted items, so a one-item selection really narrows the batch.
    const value = board([
      ...boardFixture().items,
      boardItem({ id: 'item-model-c', name: '盾牌', state: 'accepted' }),
    ]);
    const downloadArchive = vi.fn()
      .mockRejectedValueOnce(new AssetListError('too large', 413, {
        code: 'asset_list_archive_too_large',
        detail: '已通过项超出整批下载上限；请用 item_ids 分批下载。',
      }))
      .mockResolvedValueOnce({ blob: new Blob(['zip']), filename: 'batch.zip' });
    renderBoard(value, { client: { downloadArchive } as Partial<AssetListClient> });
    await waitFor(() => expect(screen.getByTestId('asset-board-download')).toBeTruthy());

    fireEvent.click(screen.getByTestId('asset-board-download'));
    const alert = await waitFor(() => screen.getByTestId('asset-board-download-error'));
    expect(alert.textContent).toContain('已通过项超出整批下载上限');
    // Without a filter or a selection the scope is still every accepted item.
    expect(screen.queryByTestId('asset-board-download-batch')).toBeNull();
    expect(alert.textContent).toContain(messages['ui.asset_list.board.download_scope_hint']);

    fireEvent.click(screen.getByTestId('asset-board-select-item-model-a'));
    const batch = await waitFor(() => screen.getByTestId('asset-board-download-batch'));
    fireEvent.click(batch);
    await waitFor(() => expect(downloadArchive).toHaveBeenLastCalledWith('proj', 'listid', ['item-model-a']));
    await waitFor(() => expect(saved.calls).toHaveLength(1));
    expect(saved.calls[0].filename).toBe('batch.zip');
  });

  it('draws the crop boxes over the original and links them to the results', async () => {
    renderBoard(compareBoard());
    await waitFor(() => expect(screen.getByTestId('asset-board-compare-toggle')).toBeTruthy());
    fireEvent.click(screen.getByTestId('asset-board-compare-toggle'));

    expect(await waitFor(() => screen.getByTestId('asset-board-compare'))).toBeTruthy();
    // The text legend names every state that appears; color is never alone.
    expect(screen.getByTestId('asset-board-compare-legend-accepted').textContent).toContain('已验收');
    expect(screen.getByTestId('asset-board-compare-legend-review').textContent).toContain('待验收');

    const box = screen.getByTestId('asset-board-compare-box-crop-accepted');
    expect(box.getAttribute('aria-label')).toBe('第 1 项「主角」，已验收');
    // The absolute placement belongs to the hint wrapper; the button fills it.
    const boxWrapper = box.parentElement;
    expect(boxWrapper?.className).toContain('absolute');
    expect(boxWrapper?.style.left).toBe('10%');
    expect(boxWrapper?.style.top).toBe('10%');
    expect(boxWrapper?.style.width).toBe('30%');
    expect(boxWrapper?.style.height).toBe('30%');

    // Clicking a box focuses its result; clicking a result highlights its box.
    fireEvent.click(screen.getByTestId('asset-board-compare-box-crop-review'));
    expect(screen.getByTestId('asset-board-compare-box-crop-review').getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByTestId('asset-board-compare-cell-crop-review').getAttribute('data-focused')).toBe('true');
    expect(screen.getByTestId('asset-board-compare-cell-crop-review').getAttribute('aria-pressed')).toBe('true');

    fireEvent.click(screen.getByTestId('asset-board-compare-cell-crop-accepted'));
    expect(screen.getByTestId('asset-board-compare-box-crop-accepted').getAttribute('data-focused')).toBe('true');
    expect(screen.getByTestId('asset-board-compare-cell-crop-review').getAttribute('data-focused')).toBeNull();

    fireEvent.click(screen.getByTestId('asset-board-compare-toggle'));
    expect(screen.getByTestId('asset-board-grid')).toBeTruthy();
  });

  it('hides the comparison entry for a list without crop provenance', async () => {
    renderBoard(board([boardItem({ id: 'plain', name: '图标', kind: 'image', state: 'failed' })]));
    await waitFor(() => expect(screen.getByTestId('asset-board-cell-plain')).toBeTruthy());
    expect(screen.queryByTestId('asset-board-compare-toggle')).toBeNull();
  });

  it('polls while the list produces, pauses while hidden and stops when done', async () => {
    vi.useFakeTimers();
    let visibility: DocumentVisibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
    const running = withRun(boardFixture(), 'running');
    // Every item terminal: the run is complete and nothing still needs polling.
    const done = withRun(board([boardItem({ id: 'item-2d', name: '图标', kind: 'image', state: 'accepted' })]), 'completed');
    const getBoard = vi.fn()
      .mockResolvedValueOnce(running)
      .mockResolvedValueOnce(running)
      .mockResolvedValue(done);
    renderBoard(running, { getBoard, pollIntervalMs: 5000 });

    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(getBoard).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(getBoard).toHaveBeenCalledTimes(2);

    visibility = 'hidden';
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(getBoard).toHaveBeenCalledTimes(2);

    visibility = 'visible';
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(getBoard).toHaveBeenCalledTimes(3);

    // The last read returned a completed run, so the interval is gone.
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(getBoard).toHaveBeenCalledTimes(3);
  });
});