// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import {
  AssetListError,
  type AssetListClient,
  type AssetListItem,
  type AssetListRun,
  type AssetListSummary,
} from '@/lib/asset-lists/client';
import { swrPollingConfig } from '@/lib/polling';
import type { ProductSchema } from '@/lib/product-schema';
import { useConnectivityStore } from '@/store/connectivity-store';
import { latestConfirmedList, useAssetListRun, type UseAssetListRunOptions } from './use-asset-list-run';

const schema = defaultCatalog.application as unknown as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

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

function summary(overrides: Partial<AssetListSummary> = {}): AssetListSummary {
  return {
    id: 'al_1',
    project_id: 'prj_1',
    title: 'Hero set',
    status: 'confirmed',
    effective_status: 'confirmed',
    item_count: 2,
    planned_step_count: 1,
    planned_model3d_step_count: 1,
    decided_at: '2026-11-18T01:00:00Z',
    expires_at: '2026-11-25T00:00:00Z',
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T01:00:00Z',
    run: run(),
    ...overrides,
  };
}

function item(overrides: Partial<AssetListItem> = {}): AssetListItem {
  return {
    id: 'it_1',
    kind: 'character',
    name: 'Hero',
    references: [],
    route_id: 'text-to-3d',
    route: [{ capability: 'text-to-3d', available: true }],
    planned_step_count: 1,
    planned_model3d_step_count: 1,
    state: 'working',
    version: 4,
    round: 1,
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    ...overrides,
  };
}

function clientWith(overrides: Partial<AssetListClient> = {}): AssetListClient {
  return {
    listProjectLists: vi.fn(async () => [summary()]),
    getList: vi.fn(async () => ({ ...summary(), version: 4, brief: {}, items: [] })),
    getBoard: vi.fn(async () => ({ list: { ...summary(), version: 4 }, items: [] })),
    review: vi.fn(async () => ({ list: { ...summary(), version: 4, brief: {}, items: [] }, items: [] })),
    downloadArchive: vi.fn(async () => ({ blob: new Blob(), filename: 'asset-list.zip' })),
    decide: vi.fn(async () => ({ ...summary(), version: 4, brief: {}, items: [] })),
    controlRun: vi.fn(async () => run({ status: 'paused', pause_reason: 'user' })),
    skipItem: vi.fn(async () => item({ state: 'skipped', version: 5 })),
    addItemSteps: vi.fn(async () => item({ version: 5 })),
    listSessionLists: vi.fn(async () => []),
    presence: vi.fn(async () => {}),
    edit: vi.fn(async () => ({ ...summary(), version: 4, brief: {}, items: [] })),
    ...overrides,
  };
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>{children}</SWRConfig>;
}

/** The page's own SWR configuration (`web/src/main.tsx`), so the #385 gate is the one under test. */
function pageWrapper({ children }: { children: React.ReactNode }) {
  return (
    <SWRConfig value={{ ...swrPollingConfig, provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>
      {children}
    </SWRConfig>
  );
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  useConnectivityStore.setState({ sessionExpired: false });
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('latestConfirmedList', () => {
  it('picks the newest confirmed, unexpired list and honours an explicit id', () => {
    const older = summary({ id: 'old', decided_at: '2026-11-17T00:00:00Z' });
    const newer = summary({ id: 'new', decided_at: '2026-11-18T00:00:00Z' });
    const draft = summary({ id: 'draft', status: 'draft', effective_status: 'draft' });
    const expired = summary({ id: 'expired', effective_status: 'expired' });

    expect(latestConfirmedList([draft, older, newer, expired])?.id).toBe('new');
    expect(latestConfirmedList([older, newer], 'old')?.id).toBe('old');
    expect(latestConfirmedList([draft])).toBeNull();
  });
});

describe('useAssetListRun (#328)', () => {
  it('reads the newest confirmed list\'s run', async () => {
    const client = clientWith();
    const { result } = renderHook(
      () => useAssetListRun({ projectId: 'prj_1', client }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.run).toEqual(run()));
    expect(result.current.list?.id).toBe('al_1');
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeUndefined();
    expect(client.listProjectLists).toHaveBeenCalledWith('prj_1');
  });

  it('follows an explicit list id', async () => {
    const client = clientWith({
      listProjectLists: vi.fn(async () => [
        summary({ id: 'newer' }),
        summary({ id: 'chosen', status: 'draft', effective_status: 'draft', run: null }),
      ]),
    });
    const { result } = renderHook(
      () => useAssetListRun({ projectId: 'prj_1', listId: 'chosen', client }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.list?.id).toBe('chosen'));
    expect(result.current.run).toBeNull();
  });

  it('sends no request without a project or when disabled', () => {
    const client = clientWith();
    renderHook(() => useAssetListRun({ projectId: null, client }), { wrapper });
    renderHook(() => useAssetListRun({ projectId: 'prj_1', enabled: false, client }), { wrapper });
    expect(client.listProjectLists).not.toHaveBeenCalled();
  });

  it('controls the run and re-reads the list at once', async () => {
    const client = clientWith();
    const { result } = renderHook(
      () => useAssetListRun({ projectId: 'prj_1', client }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.run).toEqual(run()));

    await act(async () => {
      await result.current.control('pause', runVersion);
    });

    expect(client.controlRun).toHaveBeenCalledWith('prj_1', 'al_1', 'pause', runVersion, undefined);
    expect(client.listProjectLists).toHaveBeenCalledTimes(2);
  });

  it('skips an item and re-reads the list at once', async () => {
    const client = clientWith();
    const { result } = renderHook(
      () => useAssetListRun({ projectId: 'prj_1', client }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.run).toEqual(run()));

    await act(async () => {
      await result.current.skip('it_1', 4, 'not needed');
    });

    expect(client.skipItem).toHaveBeenCalledWith('prj_1', 'al_1', 'it_1', 4, 'not needed');
    expect(client.listProjectLists).toHaveBeenCalledTimes(2);
  });

  it('publishes a refused read', async () => {
    const client = clientWith({
      listProjectLists: vi.fn().mockRejectedValue(
        new AssetListError('offline', 500, { code: 'internal', detail: 'offline' }),
      ),
    });
    const { result } = renderHook(
      () => useAssetListRun({ projectId: 'prj_1', client }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.error).toMatchObject({ status: 500, code: 'internal' }));
    expect(result.current.run).toBeNull();
  });

  it('polls while visible and stops while the tab is hidden', async () => {
    let visibility: DocumentVisibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
    const client = clientWith();
    const options: UseAssetListRunOptions = { projectId: 'prj_1', client, intervalMs: 20 };
    renderHook(() => useAssetListRun(options), { wrapper });

    await waitFor(() => expect(vi.mocked(client.listProjectLists).mock.calls.length).toBeGreaterThanOrEqual(2));

    visibility = 'hidden';
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    // Let any tick already in flight land, then watch a quiet window.
    await new Promise((resolve) => setTimeout(resolve, 40));
    vi.mocked(client.listProjectLists).mockClear();
    await new Promise((resolve) => setTimeout(resolve, 70));
    expect(client.listProjectLists).not.toHaveBeenCalled();

    visibility = 'visible';
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await waitFor(() => expect(client.listProjectLists).toHaveBeenCalled());
  });

  // #511 (#385): the chip's read follows the page's polling rules: the Schema's task polling interval (no fixed 4s of
  // its own), a wait that grows while a list that is not running stays the same, and no read after the login ended.
  const base = schema.runtime.frontend.task_polling_interval_milliseconds;
  const idle = schema.runtime.frontend.idle_polling;

  it('reads a running list again at the Schema\'s task polling interval', async () => {
    vi.useFakeTimers();
    const client = clientWith();
    renderHook(() => useAssetListRun({ projectId: 'prj_1', client }), { wrapper: pageWrapper });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(1);

    for (let read = 2; read <= 4; read += 1) {
      await act(async () => { await vi.advanceTimersByTimeAsync(base - 1); });
      expect(client.listProjectLists).toHaveBeenCalledTimes(read - 1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(client.listProjectLists).toHaveBeenCalledTimes(read);
    }
  });

  it('slows down while a list that is not running stays the same', async () => {
    vi.useFakeTimers();
    const client = clientWith({
      listProjectLists: vi.fn(async () => [summary({ run: run({ status: 'paused', pause_reason: 'user' }) })]),
    });
    renderHook(() => useAssetListRun({ projectId: 'prj_1', client }), { wrapper: pageWrapper });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(1);

    // The first answer counts as a change; the reads after it find the same list.
    await act(async () => { await vi.advanceTimersByTimeAsync(base); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(base); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(3);
    // The next wait is the interval times the Schema's factor.
    const grown = Math.min(idle.max_interval_milliseconds, base * idle.backoff_factor);
    await act(async () => { await vi.advanceTimersByTimeAsync(grown - 1); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(4);
  });

  it('reads nothing once the login has ended', async () => {
    vi.useFakeTimers();
    const client = clientWith();
    renderHook(() => useAssetListRun({ projectId: 'prj_1', client }), { wrapper: pageWrapper });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(1);

    act(() => { useConnectivityStore.setState({ sessionExpired: true }); });
    await act(async () => { await vi.advanceTimersByTimeAsync(10 * base); });
    expect(client.listProjectLists).toHaveBeenCalledTimes(1);
  });
});
