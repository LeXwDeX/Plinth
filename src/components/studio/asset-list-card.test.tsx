// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AssetListCard, MIN_CROP_SHORT_EDGE } from '@/components/studio/asset-list-card';
import { SideDrawer } from '@/components/ui/side-drawer';
import {
  AssetListError,
  type AssetListClient,
  type AssetListDetail,
  type AssetListCrop,
  type AssetListItem,
  type AssetListRun,
  type CropInstruction,
} from '@/lib/asset-lists/client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { useStudioStore } from '@/store/studio-store';

vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }),
}));

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const text = (key: string) => schemaText(schema, key);

// jsdom has no PointerEvent; the drag handlers read clientX/clientY and
// pointerType from it, so the card's tests run on a MouseEvent subclass.
class TestPointerEvent extends MouseEvent {
  readonly pointerId: number;

  readonly pointerType: string;

  readonly isPrimary: boolean;

  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init);
    this.pointerId = init.pointerId ?? 1;
    this.pointerType = init.pointerType ?? 'mouse';
    this.isPrimary = init.isPrimary ?? true;
  }
}

beforeAll(() => {
  Object.defineProperty(window, 'PointerEvent', {
    configurable: true,
    writable: true,
    value: TestPointerEvent,
  });
});

afterEach(() => {
  cleanup();
  useStudioStore.setState({ assetBoard: null });
});

function item(overrides: Partial<AssetListItem> = {}): AssetListItem {
  return {
    id: 'item_1',
    kind: 'character',
    name: '主角',
    references: [],
    route_id: 'text-to-3d',
    route: [{ capability: 'text-to-3d', available: true }],
    planned_step_count: 1,
    planned_model3d_step_count: 1,
    state: 'todo',
    version: 1,
    round: 1,
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    ...overrides,
  };
}

function detail(overrides: Partial<AssetListDetail> = {}): AssetListDetail {
  return {
    id: 'listid',
    project_id: 'proj',
    title: '英雄套装',
    status: 'draft',
    effective_status: 'draft',
    item_count: 2,
    planned_step_count: 3,
    planned_model3d_step_count: 2,
    expires_at: '2026-11-25T00:00:00Z',
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    version: 1,
    brief: { purpose: '游戏角色', image_transparent: true, triangle_budget: 30000 },
    items: [
      item(),
      item({
        id: 'item_2',
        kind: 'weapon',
        name: '长剑',
        state: 'working',
        route: [{ capability: 'text-to-3d', available: false, reason: 'offline' }],
        planned_step_count: 2,
      }),
    ],
    ...overrides,
  };
}

const runVersion = '2026-11-18T01:00:00.123456789Z';

function run(overrides: Partial<AssetListRun> = {}): AssetListRun {
  return {
    status: 'running',
    pause_reason: '',
    version: runVersion,
    item_counts: { todo: 1, working: 1, review: 0, accepted: 0, returned: 0, failed: 0, skipped: 0 },
    ...overrides,
  };
}

function clientWith(
  decide: ReturnType<typeof vi.fn>,
  controls: {
    controlRun?: ReturnType<typeof vi.fn>;
    skipItem?: ReturnType<typeof vi.fn>;
    edit?: ReturnType<typeof vi.fn>;
  } = {},
): AssetListClient {
  return {
    listProjectLists: vi.fn(async () => []),
    getList: vi.fn(async () => detail()),
    getBoard: vi.fn(async () => ({ list: { ...detail(), version: 1 }, items: [] })),
    review: vi.fn(async () => ({ list: detail(), items: [] })),
    downloadArchive: vi.fn(async () => ({ blob: new Blob(), filename: 'asset-list.zip' })),
    decide: decide as unknown as AssetListClient['decide'],
    controlRun: (controls.controlRun ?? vi.fn(async () => run())) as unknown as AssetListClient['controlRun'],
    skipItem: (controls.skipItem ?? vi.fn(async () => item({ state: 'skipped' }))) as unknown as AssetListClient['skipItem'],
    addItemSteps: vi.fn(async () => item()) as unknown as AssetListClient['addItemSteps'],
    listSessionLists: vi.fn(async () => []),
    presence: vi.fn(async () => {}),
    edit: (controls.edit ?? vi.fn(async () => detail())) as unknown as AssetListClient['edit'],
  };
}

function renderCard(options: {
  detail?: AssetListDetail;
  schema?: ProductSchema;
  decide?: ReturnType<typeof vi.fn>;
  controlRun?: ReturnType<typeof vi.fn>;
  skipItem?: ReturnType<typeof vi.fn>;
  edit?: ReturnType<typeof vi.fn>;
  onDecided?: () => void;
  onRunChanged?: () => void;
  onEdited?: (updated: AssetListDetail) => void;
  onStale?: () => void;
} = {}) {
  const decide = options.decide ?? vi.fn(async () => detail({ status: 'confirmed', effective_status: 'confirmed' }));
  const controlRun = options.controlRun ?? vi.fn(async () => run());
  const skipItem = options.skipItem ?? vi.fn(async () => item({ state: 'skipped' }));
  const edit = options.edit ?? vi.fn(async () => detail());
  const onDecided = options.onDecided ?? vi.fn();
  const onRunChanged = options.onRunChanged ?? vi.fn();
  const onEdited = options.onEdited ?? vi.fn();
  const onStale = options.onStale ?? vi.fn();
  const card = (detailValue: AssetListDetail) => (
    <AssetListCard
      detail={detailValue}
      schema={options.schema ?? schema}
      projectId="proj"
      client={clientWith(decide, { controlRun, skipItem, edit })}
      onDecided={onDecided}
      onRunChanged={onRunChanged}
      onEdited={onEdited}
      onStale={onStale}
    />
  );
  const view = render(card(options.detail ?? detail()));
  const rerender = (next: AssetListDetail) => view.rerender(card(next));
  return { decide, controlRun, skipItem, edit, onDecided, onRunChanged, onEdited, onStale, view, rerender };
}

describe('AssetListCard (#327)', () => {
  it('renders the list, its counts, brief, items and availability warning with the confirm/reject decision', () => {
    renderCard();

    expect(screen.getByText('英雄套装')).toBeTruthy();
    expect(screen.getByTestId('asset-list-status').textContent).toBe('草稿');
    expect(screen.getByText(formatSchemaText(messages['ui.asset_list.items_count'], { count: 2 }))).toBeTruthy();
    expect(screen.getByText(formatSchemaText(messages['ui.asset_list.planned_steps'], { steps: 3 }))).toBeTruthy();
    expect(screen.getByText(formatSchemaText(messages['ui.asset_list.planned_model3d_steps'], { steps: 2 }))).toBeTruthy();

    expect(screen.getByTestId('asset-list-brief').textContent).toContain(messages['ui.asset_list.brief_summary']);
    // Labels come from the Schema brief fields, values from the list brief.
    expect(screen.getByText('用途')).toBeTruthy();
    expect(screen.getByText('游戏角色')).toBeTruthy();
    expect(screen.getByText('透明背景')).toBeTruthy();
    expect(screen.getByText('true')).toBeTruthy();
    expect(screen.getByText('三角面预算')).toBeTruthy();
    expect(screen.getByText('30000')).toBeTruthy();

    expect(screen.getByTestId('asset-list-items').children).toHaveLength(2);
    expect(screen.getByTestId('asset-list-item-0').textContent).toContain('主角');
    expect(screen.getByTestId('asset-list-item-0').textContent).toContain('角色');
    expect(screen.getByTestId('asset-list-item-0').textContent).toContain('待开始');
    expect(screen.getByTestId('asset-list-item-0').textContent).toContain(
      formatSchemaText(messages['ui.asset_list.planned_steps'], { steps: 1 }),
    );
    expect(screen.getByTestId('asset-list-item-1').textContent).toContain('长剑');

    expect(screen.getByTestId('asset-list-availability-warning').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.availability_warning'], { count: 1 }),
    );
    expect(screen.getByText(messages['ui.asset_list.pending_hint'])).toBeTruthy();
    expect(screen.getByTestId('asset-list-confirm').textContent).toBe(messages['ui.asset_list.confirm']);
    expect(screen.getByTestId('asset-list-reject').textContent).toBe(messages['ui.asset_list.reject']);

    // #327: a production list never quotes a price or a budget.
    expect(screen.getByTestId('asset-list-card').textContent).not.toMatch(/价格|积分|price|credit/i);
  });

  it('offers the acceptance board only on a confirmed list (#329)', () => {
    renderCard();
    // A draft has nothing to review yet.
    expect(screen.queryByTestId('asset-list-open-board')).toBeNull();
    cleanup();

    renderCard({ detail: detail({ status: 'confirmed', effective_status: 'confirmed' }) });
    fireEvent.click(screen.getByTestId('asset-list-open-board'));
    expect(useStudioStore.getState().assetBoard).toEqual({
      projectId: 'proj', listId: 'listid', title: '英雄套装',
    });
  });

  it('confirms through the client and tells the parent to refresh', async () => {
    const decided = detail({ status: 'confirmed', effective_status: 'confirmed' });
    const decide = vi.fn(async () => decided);
    const onDecided = vi.fn();
    renderCard({ decide, onDecided });

    fireEvent.click(screen.getByTestId('asset-list-confirm'));

    await waitFor(() => expect(decide).toHaveBeenCalledWith('proj', 'listid', 'confirm'));
    await waitFor(() => expect(onDecided).toHaveBeenCalledTimes(1));
  });

  it('turns a 409 active-list conflict into the replace decision', async () => {
    const decide = vi.fn()
      .mockRejectedValueOnce(new AssetListError('conflict', 409, {
        code: 'asset_list_active_conflict',
        detail: 'asset_list_active_error',
        activeListId: 'other_list',
        activeListTitle: '旧清单',
      }))
      .mockResolvedValueOnce(detail({ status: 'confirmed', effective_status: 'confirmed' }));
    const onDecided = vi.fn();
    renderCard({ decide, onDecided });

    fireEvent.click(screen.getByTestId('asset-list-confirm'));

    await waitFor(() => expect(screen.getByTestId('asset-list-conflict')).toBeTruthy());
    expect(screen.getByTestId('asset-list-conflict').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.active_conflict'], { title: '旧清单' }),
    );
    expect(screen.getByTestId('asset-list-replace').textContent).toBe(messages['ui.asset_list.replace']);

    fireEvent.click(screen.getByTestId('asset-list-replace'));

    await waitFor(() => expect(decide).toHaveBeenLastCalledWith('proj', 'listid', 'replace'));
    await waitFor(() => expect(onDecided).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByTestId('asset-list-conflict')).toBeNull());
  });

  it('shows the Schema translation of a refused decision, else the card failure copy', async () => {
    const translated = vi.fn().mockRejectedValue(
      new AssetListError('refused', 500, { detail: 'ui.asset_list.load_failed' }),
    );
    renderCard({ decide: translated });
    fireEvent.click(screen.getByTestId('asset-list-confirm'));
    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(
      messages['ui.asset_list.load_failed'],
    ));
    expect(screen.getByRole('alert')).toBe(screen.getByTestId('asset-list-error'));

    cleanup();
    const untranslated = vi.fn().mockRejectedValue(new Error('boom'));
    renderCard({ decide: untranslated });
    fireEvent.click(screen.getByTestId('asset-list-confirm'));
    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(
      messages['ui.asset_list.decide_failed'],
    ));
  });

  it('shows the hint and no decision buttons once confirmed or rejected, and on an expired draft', () => {
    renderCard({ detail: detail({ status: 'confirmed', effective_status: 'confirmed' }) });
    expect(screen.getByText(messages['ui.asset_list.confirmed_hint'])).toBeTruthy();
    expect(screen.queryByText(messages['ui.asset_list.pending_hint'])).toBeNull();
    expect(screen.queryByTestId('asset-list-confirm')).toBeNull();
    expect(screen.queryByTestId('asset-list-reject')).toBeNull();

    cleanup();
    renderCard({ detail: detail({ status: 'rejected', effective_status: 'rejected' }) });
    expect(screen.getByText(messages['ui.asset_list.rejected_hint'])).toBeTruthy();
    expect(screen.queryByTestId('asset-list-confirm')).toBeNull();
    expect(screen.queryByTestId('asset-list-reject')).toBeNull();

    cleanup();
    renderCard({ detail: detail({ status: 'draft', effective_status: 'expired', expires_at: '2026-11-01T00:00:00Z' }) });
    expect(screen.getByTestId('asset-list-status').textContent).toBe(messages['ui.asset_list.expired']);
    expect(screen.getByTestId('asset-list-actions').textContent).toContain(messages['ui.asset_list.expired']);
    expect(screen.queryByTestId('asset-list-confirm')).toBeNull();
    expect(screen.queryByTestId('asset-list-reject')).toBeNull();
  });

  it('hides the 3D step line for an all-2D list', () => {
    renderCard({
      detail: detail({
        planned_model3d_step_count: 0,
        items: [item({ planned_model3d_step_count: 0 })],
      }),
    });

    expect(screen.queryByText(/其中 3D/)).toBeNull();
    expect(screen.getByTestId('asset-list-counts').textContent).toContain(
      formatSchemaText(messages['ui.asset_list.planned_steps'], { steps: 3 }),
    );
  });

  it('marks an item whose route is unavailable', () => {
    renderCard();

    expect(screen.getByTestId('asset-list-item-1').textContent).toContain(messages['ui.asset_list.item_unavailable']);
    expect(screen.getByTestId('asset-list-item-0').textContent).not.toContain(messages['ui.asset_list.item_unavailable']);
  });

  it('renders safely with no items, an empty brief and empty routes', () => {
    renderCard({
      detail: detail({
        items: [item({ route: [], planned_step_count: 0, planned_model3d_step_count: 0 })],
        item_count: 0,
        brief: {},
        planned_step_count: 0,
        planned_model3d_step_count: 0,
      }),
    });

    expect(screen.getByText(formatSchemaText(messages['ui.asset_list.items_count'], { count: 0 }))).toBeTruthy();
    expect(screen.queryByTestId('asset-list-brief')).toBeNull();
    expect(screen.queryByTestId('asset-list-availability-warning')).toBeNull();
    expect(screen.queryByTestId('asset-list-item-unavailable')).toBeNull();
    expect(screen.getByTestId('asset-list-item-0')).toBeTruthy();

    cleanup();
    renderCard({ detail: detail({ items: [], item_count: 0, brief: {} }) });
    expect(screen.queryByTestId('asset-list-items')).toBeNull();
  });
});

describe('AssetListCard draft editing (#331)', () => {
  const originOne: CropInstruction = {
    source_input_asset_id: 'scene_1',
    region: { x: 0.25, y: 0.25, width: 0.25, height: 0.25 },
  };
  const originTwo: CropInstruction = {
    source_input_asset_id: 'scene_1',
    region: { x: 0.5, y: 0.5, width: 0.25, height: 0.25 },
  };

  interface EditPayloadItem {
    kind: string;
    name: string;
    description?: string;
    route_id?: string;
    references?: Array<{ input_asset_id?: string }>;
    crop?: CropInstruction;
  }

  interface EditPayload {
    title: string;
    expected_version: number;
    brief: Record<string, unknown>;
    items: EditPayloadItem[];
  }

  function editCalls(edit: ReturnType<typeof vi.fn>): Array<[string, string, EditPayload]> {
    return edit.mock.calls as unknown as Array<[string, string, EditPayload]>;
  }

  function lastEditPayload(edit: ReturnType<typeof vi.fn>): EditPayload {
    const calls = editCalls(edit);
    return calls[calls.length - 1][2];
  }

  function editable(overrides: Partial<AssetListDetail> = {}): AssetListDetail {
    return detail({
      items: [
        item({ id: 'item_1', references: [{ input_asset_id: 'crop_1' }] }),
        item({ id: 'item_2', kind: 'weapon', name: '长剑', references: [{ input_asset_id: 'crop_2' }] }),
      ],
      ...overrides,
    });
  }

  it('shows a reference thumbnail for every draft item that has one', () => {
    renderCard({ detail: editable() });
    expect(screen.getByTestId('asset-list-item-thumb-0').getAttribute('src')).toBe('/api/v1/assets/crop_1');
    expect(screen.getByTestId('asset-list-item-thumb-1').getAttribute('src')).toBe('/api/v1/assets/crop_2');

    cleanup();
    renderCard({ detail: detail({ items: [item({ references: [] })] }) });
    // No reference picture: no empty placeholder either.
    expect(screen.queryByTestId('asset-list-item-thumb-0')).toBeNull();
  });

  it('uses the last input reference as the thumbnail', () => {
    renderCard({
      detail: detail({
        items: [item({
          references: [
            { input_asset_id: 'crop_first' },
            { task_id: 'task_1' },
            { input_asset_id: 'crop_last' },
          ],
        })],
      }),
    });

    expect(screen.getByTestId('asset-list-item-thumb-0').getAttribute('src')).toBe('/api/v1/assets/crop_last');

    cleanup();
    // Only task-output references: no input picture, so no tile.
    renderCard({ detail: detail({ items: [item({ references: [{ task_id: 'task_1' }] })] }) });
    expect(screen.queryByTestId('asset-list-item-thumb-0')).toBeNull();
  });

  it('keeps a confirmed list read-only', () => {
    renderCard({ detail: detail({ status: 'confirmed', effective_status: 'confirmed' }) });

    expect(screen.queryByTestId('asset-list-edit')).toBeNull();
    expect(screen.queryByTestId('asset-list-item-name-0')).toBeNull();
    expect(screen.queryByTestId('asset-list-item-delete-0')).toBeNull();
    expect(screen.queryByTestId('asset-list-item-thumb-0')).toBeNull();
  });

  it('renames an item and saves the whole list with its version and brief', async () => {
    const updated = editable({ version: 2 });
    const edit = vi.fn(async () => updated);
    const onEdited = vi.fn();
    renderCard({ detail: editable(), edit, onEdited });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    expect(screen.queryByTestId('asset-list-confirm')).toBeNull();
    fireEvent.change(screen.getByTestId('asset-list-item-name-0'), { target: { value: '新主角' } });
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    const [projectID, listID, payload] = editCalls(edit)[0];
    expect(projectID).toBe('proj');
    expect(listID).toBe('listid');
    expect(payload).toMatchObject({
      title: '英雄套装',
      expected_version: 1,
      brief: { purpose: '游戏角色', image_transparent: true, triangle_budget: 30000 },
    });
    expect(payload.items).toHaveLength(2);
    expect(payload.items[0]).toMatchObject({
      kind: 'character',
      name: '新主角',
      route_id: 'text-to-3d',
      references: [{ input_asset_id: 'crop_1' }],
    });
    expect(payload.items[1]).toMatchObject({ name: '长剑' });
    await waitFor(() => expect(onEdited).toHaveBeenCalledWith(updated));
    // The editor closes on the stored list.
    await waitFor(() => expect(screen.queryByTestId('asset-list-save')).toBeNull());
    expect(screen.getByTestId('asset-list-edit')).toBeTruthy();
  });

  it('deletes an item before saving, keeping at least one', async () => {
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({ detail: editable(), edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-delete-1'));
    expect(screen.queryByTestId('asset-list-item-1')).toBeNull();
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    const payload = lastEditPayload(edit);
    expect(payload.items).toHaveLength(1);
    expect(payload.items[0]).toMatchObject({ name: '主角', kind: 'character' });
  });

  it('disables deleting the last remaining item', () => {
    renderCard({ detail: detail({ items: [item({ references: [{ input_asset_id: 'crop_1' }] })] }) });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    expect((screen.getByTestId('asset-list-item-delete-0') as HTMLButtonElement).disabled).toBe(true);
  });

  it('merges two same-source items into one crop over their united region', async () => {
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({
      detail: editable({
        items: [
          item({ id: 'item_1', references: [{ input_asset_id: 'crop_1' }], crop: originOne }),
          item({
            id: 'item_2', kind: 'weapon', name: '长剑',
            references: [{ input_asset_id: 'crop_2' }], crop: originTwo,
          }),
        ],
      }),
      edit,
    });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-0'));
    expect(screen.getByTestId('asset-list-merge-panel').textContent).toContain(
      messages['ui.asset_list.edit_merge_hint'],
    );
    fireEvent.click(screen.getByTestId('asset-list-item-merge-1'));
    fireEvent.click(screen.getByTestId('asset-list-merge'));

    // The second item left the list; the first kept its identity.
    expect(screen.queryByTestId('asset-list-item-1')).toBeNull();
    expect((screen.getByTestId('asset-list-item-name-0') as HTMLInputElement).value).toBe('主角');

    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    const payload = lastEditPayload(edit);
    expect(payload.items).toHaveLength(1);
    expect(payload.items[0]).toMatchObject({
      name: '主角',
      description: '',
      references: [{ input_asset_id: 'crop_1' }],
    });
    expect(payload.items[0].crop).toEqual({
      source_input_asset_id: 'scene_1',
      region: { x: 0.25, y: 0.25, width: 0.5, height: 0.5 },
    });
  });

  it('keeps the first item\'s fields and folds the second item\'s plain references in', async () => {
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({
      detail: editable({
        items: [
          item({
            id: 'item_1',
            name: '主角',
            description: '第一项描述',
            references: [{ input_asset_id: 'crop_1' }, { task_id: 'task_dup' }],
            crop: originOne,
          }),
          item({
            id: 'item_2', kind: 'weapon', name: '长剑', description: '第二项描述',
            route_id: 'image-to-3d',
            references: [
              { input_asset_id: 'crop_2' },
              { task_id: 'task_dup' },
              { task_id: 'task_new' },
            ],
            crop: originTwo,
          }),
        ],
      }),
      edit,
    });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-0'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-1'));
    expect(screen.getByTestId('asset-list-merge-panel').textContent).toContain(
      messages['ui.asset_list.edit_merge_hint'],
    );
    fireEvent.click(screen.getByTestId('asset-list-merge'));

    expect(screen.queryByTestId('asset-list-item-1')).toBeNull();
    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    const payload = lastEditPayload(edit);
    expect(payload.items).toHaveLength(1);
    expect(payload.items[0]).toMatchObject({
      kind: 'character',
      name: '主角',
      description: '第一项描述',
      route_id: 'text-to-3d',
      // The second item's crop picture is left out; its plain references join
      // the first item, de-duplicated.
      references: [
        { input_asset_id: 'crop_1' },
        { task_id: 'task_dup' },
        { task_id: 'task_new' },
      ],
    });
  });

  it('refuses to merge two items of different source pictures', () => {
    renderCard({
      detail: editable({
        items: [
          item({ id: 'item_1', references: [{ input_asset_id: 'crop_1' }], crop: originOne }),
          item({
            id: 'item_2', kind: 'weapon', name: '长剑',
            references: [{ input_asset_id: 'crop_2' }],
            crop: { ...originTwo, source_input_asset_id: 'scene_2' },
          }),
        ],
      }),
    });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-0'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-1'));

    expect(screen.getByTestId('asset-list-merge-mismatch').textContent).toBe(
      messages['ui.asset_list.edit_merge_source_mismatch'],
    );
    expect((screen.getByTestId('asset-list-merge') as HTMLButtonElement).disabled).toBe(true);
  });

  it('offers merge only for an item with a reference picture and a resolved origin', () => {
    renderCard({
      detail: detail({
        items: [
          item({ id: 'item_1', references: [{ input_asset_id: 'crop_1' }], crop: originOne }),
          // A crop origin but no reference picture still cannot merge.
          item({ id: 'item_2', name: '长剑', references: [], crop: originTwo }),
        ],
      }),
    });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    expect(screen.getByTestId('asset-list-item-merge-0')).toBeTruthy();
    expect(screen.queryByTestId('asset-list-item-merge-1')).toBeNull();

    cleanup();
    // No origin at all: the draft has no merge entry.
    renderCard({ detail: editable() });
    fireEvent.click(screen.getByTestId('asset-list-edit'));
    expect(screen.queryByTestId('asset-list-item-merge-0')).toBeNull();
  });

  it('reports a version conflict, re-reads the list and keeps the draft', async () => {
    const edit = vi.fn().mockRejectedValue(
      new AssetListError('conflict', 409, { code: 'conflict', detail: 'error.conflict' }),
    );
    const onStale = vi.fn();
    renderCard({ detail: editable(), edit, onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.change(screen.getByTestId('asset-list-item-name-0'), { target: { value: '改过的名字' } });
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(
      messages['ui.asset_list.edit_conflict'],
    ));
    expect(onStale).toHaveBeenCalledTimes(1);
    // The local draft is not overwritten.
    expect((screen.getByTestId('asset-list-item-name-0') as HTMLInputElement).value).toBe('改过的名字');
    expect(screen.getByTestId('asset-list-save')).toBeTruthy();
  });

  it('shows the server copy of another edit refusal', async () => {
    const refused = new AssetListError('refused', 400, {
      code: 'asset_list_region_too_small',
      detail: '拆解区域太小：最短边至少 64 像素，请把这块区域框得更大一些。',
    });
    const onStale = vi.fn();
    renderCard({ detail: editable(), edit: vi.fn().mockRejectedValue(refused), onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(refused.detail));
    // A non-conflict refusal keeps the draft and does not claim a reload.
    expect(onStale).not.toHaveBeenCalled();
    expect(screen.getByTestId('asset-list-save')).toBeTruthy();
  });

  it('shows the retry copy on a network failure and keeps the draft', async () => {
    const edit = vi.fn().mockRejectedValue(new TypeError('fetch failed'));
    renderCard({ detail: editable(), edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(
      messages['ui.asset_list.edit_save_failed'],
    ));
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(false);
  });

  it('shows the server expiry copy and leaves the editor', async () => {
    const expired = new AssetListError('expired', 409, {
      code: 'asset_list_expired',
      detail: '制作清单已过期，请让 Agent 重新提案。',
    });
    const edit = vi.fn().mockRejectedValue(expired);
    const onStale = vi.fn();
    renderCard({ detail: editable(), edit, onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(expired.detail));
    expect(screen.queryByTestId('asset-list-save')).toBeNull();
    expect(onStale).toHaveBeenCalledTimes(1);
  });

  it('locks saving when the shared detail advances under the draft and reloads only on demand', async () => {
    const latest = editable({
      version: 2,
      items: [
        item({ id: 'item_1', name: '服务器上的名字', references: [{ input_asset_id: 'crop_1' }] }),
        item({ id: 'item_2', kind: 'weapon', name: '长剑', references: [{ input_asset_id: 'crop_2' }] }),
      ],
    });
    const edit = vi.fn(async () => latest);
    const onStale = vi.fn();
    const { rerender } = renderCard({ detail: editable(), edit, onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.change(screen.getByTestId('asset-list-item-name-0'), { target: { value: '本地草稿' } });
    rerender(latest);

    // The draft survives the refresh, but its baseline version is stale, so it
    // is not sent with the live version.
    expect((screen.getByTestId('asset-list-item-name-0') as HTMLInputElement).value).toBe('本地草稿');
    expect(screen.getByTestId('asset-list-edit-stale').textContent).toContain(
      messages['ui.asset_list.edit_stale'],
    );
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId('asset-list-save'));
    expect(edit).not.toHaveBeenCalled();

    // Only the explicit reload discards the draft; it rebuilds from the latest
    // detail and asks the parent to re-read.
    fireEvent.click(screen.getByTestId('asset-list-edit-reload'));
    expect((screen.getByTestId('asset-list-item-name-0') as HTMLInputElement).value).toBe('服务器上的名字');
    expect(screen.queryByTestId('asset-list-edit-stale')).toBeNull();
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(false);
    expect(onStale).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    expect(lastEditPayload(edit).expected_version).toBe(2);
    expect(lastEditPayload(edit).title).toBe('英雄套装');
  });

  it('leaves the editor and tells the parent when the list is decided elsewhere', () => {
    const onStale = vi.fn();
    const { rerender } = renderCard({ detail: editable(), onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.change(screen.getByTestId('asset-list-item-name-0'), { target: { value: '本地草稿' } });
    rerender(editable({ status: 'confirmed', effective_status: 'confirmed' }));

    expect(screen.queryByTestId('asset-list-save')).toBeNull();
    expect(screen.queryByTestId('asset-list-item-name-0')).toBeNull();
    expect(onStale).toHaveBeenCalledTimes(1);
  });

  it('leaves the editor when an expired draft arrives from a re-read', () => {
    const onStale = vi.fn();
    const { rerender } = renderCard({ detail: editable(), onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    rerender(editable({ status: 'draft', effective_status: 'expired' }));

    expect(screen.queryByTestId('asset-list-save')).toBeNull();
    expect(onStale).toHaveBeenCalledTimes(1);
  });

  it('leaves the editor when the card is handed another list', () => {
    const onStale = vi.fn();
    const { rerender } = renderCard({ detail: editable(), onStale });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    rerender(editable({ id: 'other_list' }));

    expect(screen.queryByTestId('asset-list-save')).toBeNull();
    expect(onStale).toHaveBeenCalledTimes(1);
  });
});

describe('AssetListCard source crop editing (#331)', () => {
  const originOne: AssetListCrop = {
    source_input_asset_id: 'scene_1',
    region: { x: 0.25, y: 0.25, width: 0.25, height: 0.25 },
    source_kind: 'image',
    source_width: 1024,
    source_height: 512,
  };
  const originTwo: AssetListCrop = {
    source_input_asset_id: 'scene_1',
    region: { x: 0.5, y: 0.5, width: 0.25, height: 0.25 },
    source_width: 1024,
    source_height: 512,
  };

  function cropped(overrides: Partial<AssetListDetail> = {}): AssetListDetail {
    return detail({
      items: [
        item({ id: 'item_1', references: [{ input_asset_id: 'crop_1' }], crop: originOne }),
        item({
          id: 'item_2', kind: 'weapon', name: '长剑',
          references: [{ input_asset_id: 'crop_2' }], crop: originTwo,
        }),
      ],
      ...overrides,
    });
  }

  /** jsdom lays nothing out: the overlay reports the display size the drag needs. */
  function overlayRect(width = 400, height = 200): Element {
    const overlay = screen.getByTestId('asset-list-crop-overlay');
    vi.spyOn(overlay, 'getBoundingClientRect').mockReturnValue({
      x: 0, y: 0, left: 0, top: 0, right: width, bottom: height, width, height,
      toJSON: () => ({}),
    } as DOMRect);
    return overlay;
  }

  function drag(overlay: Element, from: [number, number], to: [number, number]) {
    fireEvent.pointerDown(overlay, { clientX: from[0], clientY: from[1], pointerId: 1, pointerType: 'mouse' });
    fireEvent.pointerMove(overlay, { clientX: to[0], clientY: to[1], pointerId: 1, pointerType: 'mouse' });
    fireEvent.pointerUp(overlay, { clientX: to[0], clientY: to[1], pointerId: 1, pointerType: 'mouse' });
  }

  function lastEditPayload(edit: ReturnType<typeof vi.fn>) {
    const calls = edit.mock.calls as unknown as Array<[string, string, {
      title: string;
      expected_version: number;
      items: Array<{
        kind: string;
        name: string;
        description?: string;
        route_id?: string;
        references?: Array<{ input_asset_id?: string }>;
        crop?: CropInstruction;
      }>;
    }]>;
    return calls[calls.length - 1][2];
  }

  it('shows the shared source picture with numbered normalized boxes', () => {
    renderCard({ detail: cropped() });

    expect(screen.getByTestId('asset-list-source-image').getAttribute('src')).toBe('/api/v1/assets/scene_1');
    const first = screen.getByTestId('asset-list-crop-box-0');
    expect(first.textContent).toBe('1');
    expect(first.style.left).toBe('25%');
    expect(first.style.top).toBe('25%');
    expect(first.style.width).toBe('25%');
    expect(first.style.height).toBe('25%');
    const second = screen.getByTestId('asset-list-crop-box-1');
    expect(second.textContent).toBe('2');
    expect(second.style.left).toBe('50%');
    // A read-only draft draws the boxes but offers no drag.
    expect(screen.queryByTestId('asset-list-crop-hint')).toBeNull();
  });

  it('explains a draft with no source picture and offers no drawing surface', () => {
    renderCard({ detail: detail({ items: [item()] }) });
    expect(screen.getByTestId('asset-list-crop-no-source').textContent).toBe(
      messages['ui.asset_list.edit_crop_no_source'],
    );
    expect(screen.queryByTestId('asset-list-crop-overlay')).toBeNull();

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    expect(screen.queryByTestId('asset-list-crop-add')).toBeNull();
    expect(screen.queryByTestId('asset-list-item-recrop-0')).toBeNull();
  });

  it('warns when items come from more than one source picture', () => {
    renderCard({
      detail: detail({
        items: [
          item({ id: 'item_1', references: [{ input_asset_id: 'crop_1' }], crop: originOne }),
          item({
            id: 'item_2', kind: 'weapon', name: '长剑',
            references: [{ input_asset_id: 'crop_2' }],
            crop: { ...originTwo, source_input_asset_id: 'scene_2' },
          }),
        ],
      }),
    });

    expect(screen.getByTestId('asset-list-crop-mixed').textContent).toBe(
      messages['ui.asset_list.edit_crop_mixed_source'],
    );
    expect(screen.getByTestId('asset-list-crop-box-0')).toBeTruthy();
    expect(screen.queryByTestId('asset-list-crop-box-1')).toBeNull();
  });

  it('draws a box, names the new item and saves its crop in the edit body', async () => {
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({ detail: cropped(), edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    drag(overlayRect(), [40, 20], [240, 120]);

    const form = screen.getByTestId('asset-list-crop-form');
    expect(form.getAttribute('data-region')).toBe('0.1,0.1,0.5,0.5');
    expect(screen.getByTestId('asset-list-crop-name-required').textContent).toBe(
      messages['ui.asset_list.edit_crop_name_required'],
    );
    const add = screen.getByTestId('asset-list-crop-form-add') as HTMLButtonElement;
    expect(add.disabled).toBe(true);

    fireEvent.change(screen.getByTestId('asset-list-crop-name'), { target: { value: '宝箱' } });
    expect(screen.queryByTestId('asset-list-crop-name-required')).toBeNull();
    fireEvent.change(screen.getByTestId('asset-list-crop-kind'), { target: { value: 'prop' } });
    fireEvent.click(add);

    // The drawn item joins the draft with the next number on the picture.
    expect((screen.getByTestId('asset-list-item-name-2') as HTMLInputElement).value).toBe('宝箱');
    expect(screen.getByTestId('asset-list-crop-box-2').textContent).toBe('3');

    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    const payload = lastEditPayload(edit);
    expect(payload.items).toHaveLength(3);
    expect(payload.items[2]).toEqual({
      kind: 'prop',
      name: '宝箱',
      description: '',
      references: [],
      crop: {
        source_input_asset_id: 'scene_1',
        region: { x: 0.1, y: 0.1, width: 0.5, height: 0.5 },
      },
    });
  });

  it('re-crops an existing item, keeping its name, kind and route', async () => {
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({ detail: cropped(), edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-recrop-0'));
    expect(screen.getByTestId('asset-list-crop-hint').textContent).toBe(
      messages['ui.asset_list.edit_crop_recrop_hint'],
    );
    drag(overlayRect(), [0, 0], [200, 100]);

    const box = screen.getByTestId('asset-list-crop-box-0');
    expect(box.style.left).toBe('0%');
    expect(box.style.width).toBe('50%');
    // The re-crop ends after one drag; the hint returns to adding items.
    expect(screen.getByTestId('asset-list-crop-hint').textContent).toBe(
      messages['ui.asset_list.edit_crop_hint'],
    );

    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    const payload = lastEditPayload(edit);
    expect(payload.items[0]).toMatchObject({
      kind: 'character',
      name: '主角',
      route_id: 'text-to-3d',
      references: [{ input_asset_id: 'crop_1' }],
      crop: {
        source_input_asset_id: 'scene_1',
        region: { x: 0, y: 0, width: 0.5, height: 0.5 },
      },
    });
    // Only the re-cropped item carries an instruction; an unchanged crop is
    // not cut again.
    expect(payload.items[1].crop).toBeUndefined();
  });

  it('refuses a region under the minimum short edge', () => {
    renderCard({ detail: cropped() });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    // 0.025 of the 1024 px source is 25.6 px, under the 64 px edge.
    drag(overlayRect(), [0, 0], [10, 10]);

    expect(screen.getByTestId('asset-list-crop-error').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.edit_crop_too_small'], { minimum: 64 }),
    );
    expect(screen.queryByTestId('asset-list-crop-form')).toBeNull();
    expect(screen.queryByTestId('asset-list-crop-box-2')).toBeNull();
  });

  it('uses the public Schema minimum, falling back to the constant on an older server', () => {
    const strict: ProductSchema = {
      ...schema,
      asset_list: {
        ...schema.asset_list!,
        limits: { ...schema.asset_list!.limits, minimum_crop_short_edge: 128 },
      },
    };
    renderCard({ detail: cropped(), schema: strict });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    // 0.1 x 0.15 of the 1024 x 512 source is 102.4 x 76.8 px: over 64, under 128.
    drag(overlayRect(), [0, 0], [40, 30]);

    expect(screen.getByTestId('asset-list-crop-error').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.edit_crop_too_small'], { minimum: 128 }),
    );
    expect(screen.queryByTestId('asset-list-crop-form')).toBeNull();

    cleanup();
    // The default Schema carries no public minimum: the same box passes under
    // the built-in fallback.
    renderCard({ detail: cropped() });
    fireEvent.click(screen.getByTestId('asset-list-edit'));
    drag(overlayRect(), [0, 0], [40, 30]);
    expect(screen.queryByTestId('asset-list-crop-error')).toBeNull();
    expect(screen.getByTestId('asset-list-crop-form')).toBeTruthy();
  });

  it('refuses a new item when the source already holds the Schema maximum', () => {
    const limited: ProductSchema = {
      ...schema,
      asset_list: {
        ...schema.asset_list!,
        limits: { ...schema.asset_list!.limits, maximum_items_per_image: 2 },
      },
    };
    renderCard({ detail: cropped(), schema: limited });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    drag(overlayRect(), [0, 0], [200, 100]);

    expect(screen.getByTestId('asset-list-crop-error').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.edit_crop_image_limit'], { maximum: 2 }),
    );
    expect(screen.queryByTestId('asset-list-crop-form')).toBeNull();
  });

  it('cancels the naming form and the re-crop with Escape', () => {
    renderCard({ detail: cropped() });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    drag(overlayRect(), [0, 0], [200, 100]);
    expect(screen.getByTestId('asset-list-crop-form')).toBeTruthy();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('asset-list-crop-form')).toBeNull();
    expect(screen.queryByTestId('asset-list-crop-box-2')).toBeNull();
    // Esc leaves the edit itself open.
    expect(screen.getByTestId('asset-list-save')).toBeTruthy();

    fireEvent.click(screen.getByTestId('asset-list-item-recrop-1'));
    expect(screen.getByTestId('asset-list-crop-cancel')).toBeTruthy();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('asset-list-crop-cancel')).toBeNull();
    expect(screen.getByTestId('asset-list-crop-hint').textContent).toBe(
      messages['ui.asset_list.edit_crop_hint'],
    );
  });

  it('holds save and the clearing entries while a named box is pending', () => {
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({ detail: cropped(), edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    drag(overlayRect(), [40, 20], [240, 120]);
    fireEvent.change(screen.getByTestId('asset-list-crop-name'), { target: { value: '宝箱' } });

    // Saving is held back, and so are the entries that would clear the form.
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId('asset-list-crop-add') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId('asset-list-item-recrop-0') as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId('asset-list-save'));
    expect(edit).not.toHaveBeenCalled();

    // A second drag does not silently drop the typed name.
    drag(overlayRect(), [0, 0], [400, 200]);
    expect((screen.getByTestId('asset-list-crop-name') as HTMLInputElement).value).toBe('宝箱');
    expect(screen.getByTestId('asset-list-crop-form').getAttribute('data-region')).toBe('0.1,0.1,0.5,0.5');

    // Only an explicit cancel clears it.
    fireEvent.click(screen.getByTestId('asset-list-crop-form-cancel'));
    expect(screen.queryByTestId('asset-list-crop-form')).toBeNull();
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(false);
  });

  it('keeps an enclosing SideDrawer open while Escape cancels a crop gesture', () => {
    const onClose = vi.fn();
    render(
      <SideDrawer title="项目" closeLabel="关闭" onClose={onClose}>
        <AssetListCard
          detail={cropped()}
          schema={schema}
          projectId="proj"
          client={clientWith(vi.fn(), { edit: vi.fn(async () => detail({ version: 2 })) })}
        />
      </SideDrawer>,
    );

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    drag(overlayRect(), [0, 0], [200, 100]);
    expect(screen.getByTestId('asset-list-crop-form')).toBeTruthy();

    // The card's capture-phase handler cancels the gesture and prevents the
    // default, so the drawer's own Escape handling leaves it open.
    fireEvent.keyDown(screen.getByTestId('asset-list-crop-name'), { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.queryByTestId('asset-list-crop-form')).toBeNull();

    // With no gesture in flight, Escape keeps its usual meaning.
    fireEvent.keyDown(screen.getByTestId('asset-list-save'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('still re-crops at the per-image maximum', async () => {
    const limited: ProductSchema = {
      ...schema,
      asset_list: {
        ...schema.asset_list!,
        limits: { ...schema.asset_list!.limits, maximum_items_per_image: 2 },
      },
    };
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({ detail: cropped(), schema: limited, edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-recrop-0'));
    drag(overlayRect(), [0, 0], [200, 100]);

    // A re-crop replaces a region and adds no item, so the limit does not fire.
    expect(screen.queryByTestId('asset-list-crop-error')).toBeNull();
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    expect(lastEditPayload(edit).items[0].crop).toEqual({
      source_input_asset_id: 'scene_1',
      region: { x: 0, y: 0, width: 0.5, height: 0.5 },
    });
  });

  it('still merges at the per-image maximum', async () => {
    const limited: ProductSchema = {
      ...schema,
      asset_list: {
        ...schema.asset_list!,
        limits: { ...schema.asset_list!.limits, maximum_items_per_image: 2 },
      },
    };
    const edit = vi.fn(async () => detail({ version: 2 }));
    renderCard({ detail: cropped(), schema: limited, edit });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-0'));
    fireEvent.click(screen.getByTestId('asset-list-item-merge-1'));
    fireEvent.click(screen.getByTestId('asset-list-merge'));

    // Merging lowers the item count, so it is never held by the limit.
    expect(screen.queryByTestId('asset-list-crop-error')).toBeNull();
    fireEvent.click(screen.getByTestId('asset-list-save'));
    await waitFor(() => expect(edit).toHaveBeenCalledTimes(1));
    expect(lastEditPayload(edit).items).toHaveLength(1);
  });
});

describe('crop minimum edge (#331)', () => {
  it('keeps the exported minimum in step with the default Catalog', () => {
    const catalog = defaultCatalog.application as unknown as {
      asset_list: { decompose: { crop: { minimum_short_edge: number } } };
    };
    expect(MIN_CROP_SHORT_EDGE).toBe(catalog.asset_list.decompose.crop.minimum_short_edge);
  });
});

describe('AssetListCard run controls (#328)', () => {
  function confirmed(runSummary: AssetListRun | null, overrides: Partial<AssetListDetail> = {}): AssetListDetail {
    return detail({
      status: 'confirmed',
      effective_status: 'confirmed',
      version: 3,
      run: runSummary,
      ...overrides,
    });
  }

  it('shows the run status and the counted progress', () => {
    renderCard({
      detail: confirmed(run({
        status: 'paused',
        pause_reason: 'presence',
        item_counts: { todo: 1, working: 0, review: 2, accepted: 3, returned: 1, failed: 0, skipped: 1 },
      })),
    });

    expect(screen.getByTestId('asset-list-run-status').textContent).toBe(messages['ui.asset_list.run_paused_presence']);
    expect(screen.getByTestId('asset-list-run-progress').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.run_progress'], {
        done: 4, total: 8, working: 0, review: 2, attention: 1,
      }),
    );
    expect(screen.getByTestId('asset-list-run-resume').textContent).toBe(messages['ui.asset_list.run_resume']);
  });

  it('starts an idle run with the run\'s opaque version string and tells the parent to refresh', async () => {
    const { controlRun, onRunChanged } = renderCard({ detail: confirmed(run({ status: 'idle' })) });

    expect(screen.queryByTestId('asset-list-run-start')).toBeTruthy();
    fireEvent.click(screen.getByTestId('asset-list-run-start'));

    await waitFor(() => expect(controlRun).toHaveBeenCalledWith('proj', 'listid', 'start', runVersion));
    await waitFor(() => expect(onRunChanged).toHaveBeenCalledTimes(1));
  });

  it('pauses a running list and stops it only after the confirmation', async () => {
    const { controlRun } = renderCard({ detail: confirmed(run({ status: 'running' })) });

    expect(screen.queryByTestId('asset-list-run-start')).toBeNull();
    fireEvent.click(screen.getByTestId('asset-list-run-pause'));
    await waitFor(() => expect(controlRun).toHaveBeenCalledWith('proj', 'listid', 'pause', runVersion));

    fireEvent.click(screen.getByTestId('asset-list-run-stop'));
    expect(screen.getByTestId('asset-list-run-stop-confirm').textContent).toContain(messages['ui.asset_list.run_stop_confirm']);
    expect(controlRun).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('asset-list-run-stop-confirmed'));
    await waitFor(() => expect(controlRun).toHaveBeenLastCalledWith('proj', 'listid', 'stop', runVersion));
    await waitFor(() => expect(screen.queryByTestId('asset-list-run-stop-confirm')).toBeNull());
  });

  it('cancels the stop confirmation without sending it', () => {
    const { controlRun } = renderCard({ detail: confirmed(run({ status: 'paused' })) });

    fireEvent.click(screen.getByTestId('asset-list-run-stop'));
    fireEvent.click(screen.getByTestId('asset-list-run-stop-cancel'));

    expect(screen.queryByTestId('asset-list-run-stop-confirm')).toBeNull();
    expect(controlRun).not.toHaveBeenCalled();
  });

  it('shows no run control on a draft, run-less, stopped or completed list', () => {
    renderCard({ detail: detail() });
    expect(screen.queryByTestId('asset-list-run-controls')).toBeNull();

    // A confirmed list without a run has no version token to send, so start is
    // not offered; the server would refuse both the missing token and no run.
    cleanup();
    renderCard({ detail: confirmed(null) });
    expect(screen.queryByTestId('asset-list-run-controls')).toBeNull();

    cleanup();
    renderCard({ detail: confirmed(run({ status: 'stopped' })) });
    expect(screen.queryByTestId('asset-list-run-controls')).toBeNull();

    cleanup();
    renderCard({ detail: confirmed(run({ status: 'completed' })) });
    expect(screen.queryByTestId('asset-list-run-controls')).toBeNull();
  });

  it('skips a todo or working item with the item version, and only a working item in a stopped run', async () => {
    const skipItem = vi.fn(async () => item({ state: 'skipped' }));
    const { onRunChanged } = renderCard({
      detail: confirmed(run({ status: 'running' }), {
        items: [
          item({ id: 'item_working', state: 'working', version: 7 }),
          item({ id: 'item_review', state: 'review', version: 2 }),
          item({ id: 'item_todo', state: 'todo', version: 4 }),
        ],
      }),
      skipItem,
    });

    fireEvent.click(screen.getByTestId('asset-list-item-skip-0'));
    await waitFor(() => expect(skipItem).toHaveBeenCalledWith('proj', 'listid', 'item_working', 7));
    await waitFor(() => expect(onRunChanged).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId('asset-list-item-skip-1')).toBeNull();
    expect(screen.getByTestId('asset-list-item-skip-2')).toBeTruthy();

    cleanup();
    renderCard({
      detail: confirmed(run({ status: 'stopped' }), { items: [item({ id: 'item_todo', state: 'todo' })] }),
    });
    expect(screen.queryByTestId('asset-list-item-skip-0')).toBeNull();

    // #508: stopping leaves an in-flight item working; skip is how the person
    // releases it, so the stopped run still offers it for that item.
    cleanup();
    const stoppedSkip = vi.fn(async () => item({ state: 'skipped' }));
    renderCard({
      detail: confirmed(run({ status: 'stopped' }), {
        items: [item({ id: 'item_stuck', state: 'working', version: 3 })],
      }),
      skipItem: stoppedSkip,
    });
    fireEvent.click(screen.getByTestId('asset-list-item-skip-0'));
    await waitFor(() => expect(stoppedSkip).toHaveBeenCalledWith('proj', 'listid', 'item_stuck', 3));
  });

  it('marks an item whose turn waits for an approval card (#508)', () => {
    renderCard({
      detail: confirmed(run({ status: 'running' }), {
        items: [
          item({ id: 'item_card', state: 'working', block: 'awaiting_approval' }),
          item({ id: 'item_plain', state: 'working' }),
        ],
      }),
    });
    const marks = screen.getAllByTestId('asset-list-item-awaiting-approval');
    expect(marks).toHaveLength(1);
    expect(marks[0].textContent).toBe('等待审批');
  });

  it('shows the Schema translation of a refused control', async () => {
    const controlRun = vi.fn().mockRejectedValue(
      new AssetListError('conflict', 409, { code: 'asset_list_version_conflict', detail: 'ui.asset_list.load_failed' }),
    );
    renderCard({ detail: confirmed(run({ status: 'running' })), controlRun });

    fireEvent.click(screen.getByTestId('asset-list-run-pause'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(
      messages['ui.asset_list.load_failed'],
    ));
  });
});