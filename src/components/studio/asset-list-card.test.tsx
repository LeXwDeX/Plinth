// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AssetListCard } from '@/components/studio/asset-list-card';
import {
  AssetListError,
  type AssetListClient,
  type AssetListDetail,
  type AssetListItem,
  type AssetListRun,
  type CropInstruction,
} from '@/lib/asset-lists/client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }),
}));

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const text = (key: string) => schemaText(schema, key);

afterEach(cleanup);

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
    decide: decide as unknown as AssetListClient['decide'],
    controlRun: (controls.controlRun ?? vi.fn(async () => run())) as unknown as AssetListClient['controlRun'],
    skipItem: (controls.skipItem ?? vi.fn(async () => item({ state: 'skipped' }))) as unknown as AssetListClient['skipItem'],
    listSessionLists: vi.fn(async () => []),
    presence: vi.fn(async () => {}),
    edit: (controls.edit ?? vi.fn(async () => detail())) as unknown as AssetListClient['edit'],
  };
}

function renderCard(options: {
  detail?: AssetListDetail;
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
  const view = render(
    <AssetListCard
      detail={options.detail ?? detail()}
      schema={schema}
      projectId="proj"
      client={clientWith(decide, { controlRun, skipItem, edit })}
      onDecided={onDecided}
      onRunChanged={onRunChanged}
      onEdited={onEdited}
      onStale={onStale}
    />,
  );
  return { decide, controlRun, skipItem, edit, onDecided, onRunChanged, onEdited, onStale, view };
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
    renderCard({ detail: editable(), edit: vi.fn().mockRejectedValue(refused) });

    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(refused.detail));
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

  it('skips a todo or working item with the item version, but not in a stopped run', async () => {
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