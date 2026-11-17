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
} from '@/lib/asset-lists/client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

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

function clientWith(decide: ReturnType<typeof vi.fn>): AssetListClient {
  return {
    listProjectLists: vi.fn(async () => []),
    getList: vi.fn(async () => detail()),
    decide: decide as unknown as AssetListClient['decide'],
    listSessionLists: vi.fn(async () => []),
    presence: vi.fn(async () => {}),
  };
}

function renderCard(options: {
  detail?: AssetListDetail;
  decide?: ReturnType<typeof vi.fn>;
  onDecided?: () => void;
} = {}) {
  const decide = options.decide ?? vi.fn(async () => detail({ status: 'confirmed', effective_status: 'confirmed' }));
  const onDecided = options.onDecided ?? vi.fn();
  const view = render(
    <AssetListCard
      detail={options.detail ?? detail()}
      schema={schema}
      projectId="proj"
      client={clientWith(decide)}
      onDecided={onDecided}
    />,
  );
  return { decide, onDecided, view };
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