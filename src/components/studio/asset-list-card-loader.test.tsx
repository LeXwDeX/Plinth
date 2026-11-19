// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AssetListCardLoader } from '@/components/studio/asset-list-card-loader';
import {
  AssetListError,
  type AssetListClient,
  type AssetListDetail,
  type AssetListItem,
  type AssetListSummary,
} from '@/lib/asset-lists/client';
import type { ProductSchema } from '@/lib/product-schema';

vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }),
}));

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

function item(overrides: Partial<AssetListItem> = {}): AssetListItem {
  return {
    id: 'item_1',
    kind: 'character',
    name: 'Role A',
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

function summary(overrides: Partial<AssetListSummary> = {}): AssetListSummary {
  return {
    id: 'list_1',
    project_id: 'prj_1',
    title: 'Hero set',
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

function detail(overrides: Partial<AssetListDetail> = {}): AssetListDetail {
  return {
    ...summary(),
    version: 1,
    brief: {},
    items: [item()],
    ...overrides,
  };
}

function clientWith(getList: ReturnType<typeof vi.fn>): AssetListClient {
  return {
    listProjectLists: vi.fn(async () => []),
    getList: getList as unknown as AssetListClient['getList'],
    getBoard: vi.fn(async () => ({ list: { ...detail(), version: 1 }, items: [] })),
    decide: vi.fn(async () => detail()),
    controlRun: vi.fn(async () => ({
      status: 'running',
      version: '2026-11-18T01:00:00.123456789Z',
      item_counts: { todo: 0, working: 0, review: 0, accepted: 1, returned: 0, failed: 0, skipped: 0 },
    })) as unknown as AssetListClient['controlRun'],
    skipItem: vi.fn(async () => item({ state: 'skipped' })) as unknown as AssetListClient['skipItem'],
    listSessionLists: vi.fn(async () => []),
    presence: vi.fn(async () => {}),
    edit: vi.fn(async () => detail()) as unknown as AssetListClient['edit'],
  };
}

function renderLoader(
  client: AssetListClient,
  callbacks: {
    onDecided?: () => void;
    onEdited?: (updated: AssetListDetail) => void;
    onStale?: () => void;
  } = {},
) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>
      <AssetListCardLoader
        summary={summary()}
        schema={schema}
        client={client}
        variant="chat"
        onDecided={callbacks.onDecided}
        onEdited={callbacks.onEdited}
        onStale={callbacks.onStale}
      />
    </SWRConfig>,
  );
}

afterEach(cleanup);

describe('asset list card loader', () => {
  it('shows a placeholder while the detail is read', () => {
    const client = clientWith(vi.fn(() => new Promise<AssetListDetail>(() => {})));
    renderLoader(client);
    expect(screen.getByTestId('asset-list-loading')).toBeTruthy();
    expect(screen.queryByTestId('asset-list-card')).toBeNull();
  });

  it('renders the card once the detail arrives', async () => {
    const client = clientWith(vi.fn(async () => detail()));
    renderLoader(client);
    const card = await screen.findByTestId('asset-list-card');
    expect(card.getAttribute('data-variant')).toBe('chat');
    expect(card.textContent).toContain('Hero set');
    expect(client.getList).toHaveBeenCalledWith('prj_1', 'list_1');
  });

  it('reports a failed read and retries it on demand', async () => {
    const getList = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(detail());
    const client = clientWith(getList);
    renderLoader(client);
    const failed = await screen.findByTestId('asset-list-load-failed');
    expect(failed.textContent).toContain(messages['ui.asset_list.load_failed']);
    fireEvent.click(screen.getByTestId('asset-list-retry'));
    await waitFor(() => expect(screen.getByTestId('asset-list-card').textContent).toContain('Hero set'));
    expect(getList).toHaveBeenCalledTimes(2);
  });

  it('shows the detail an edit returned without a second read and tells the parent', async () => {
    const updated = detail({ version: 2, title: 'Hero set v2', items: [item({ name: 'Role B' })] });
    const client = clientWith(vi.fn(async () => detail()));
    client.edit = vi.fn(async () => updated) as unknown as AssetListClient['edit'];
    const onEdited = vi.fn();
    renderLoader(client, { onEdited });

    await screen.findByTestId('asset-list-card');
    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(onEdited).toHaveBeenCalledWith(updated));
    // The returned detail replaces the cached one; the loader does not re-read.
    await waitFor(() => expect(screen.getByTestId('asset-list-card').textContent).toContain('Hero set v2'));
    expect(client.getList).toHaveBeenCalledTimes(1);
  });

  it('re-reads the list on an edit conflict and locks the stale draft', async () => {
    const getList = vi.fn()
      .mockResolvedValueOnce(detail())
      .mockResolvedValue(detail({ version: 2, items: [item({ name: 'Server role' })] }));
    const client = clientWith(getList);
    client.edit = vi.fn().mockRejectedValue(
      new AssetListError('conflict', 409, { code: 'conflict', detail: 'error.conflict' }),
    ) as unknown as AssetListClient['edit'];
    const onStale = vi.fn();
    renderLoader(client, { onStale });

    await screen.findByTestId('asset-list-card');
    fireEvent.click(screen.getByTestId('asset-list-edit'));
    fireEvent.change(screen.getByTestId('asset-list-item-name-0'), { target: { value: 'Local role' } });
    fireEvent.click(screen.getByTestId('asset-list-save'));

    await waitFor(() => expect(screen.getByTestId('asset-list-error').textContent).toBe(
      messages['ui.asset_list.edit_conflict'],
    ));
    // The conflict re-reads the detail and tells the parent to refresh its
    // summary; the newer version locks the draft until the person explicitly
    // reloads, and the local edit is still there.
    await waitFor(() => expect(getList).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(onStale).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByTestId('asset-list-edit-stale')).toBeTruthy());
    expect((screen.getByTestId('asset-list-item-name-0') as HTMLInputElement).value).toBe('Local role');
    expect((screen.getByTestId('asset-list-save') as HTMLButtonElement).disabled).toBe(true);
  });
});