// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AssetListProgressChip } from '@/components/studio/asset-list-progress-chip';
import type { AssetListClient, AssetListRun, AssetListSummary } from '@/lib/asset-lists/client';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';
import { resetProjectStore, useProjectStore } from '@/store/project-store';
import { useStudioStore } from '@/store/studio-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

function run(overrides: Partial<AssetListRun> = {}): AssetListRun {
  return {
    status: 'running',
    pause_reason: '',
    item_counts: { todo: 1, working: 1, review: 1, accepted: 2, returned: 0, failed: 0, skipped: 0 },
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
    item_count: 5,
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

function clientWith(lists: AssetListSummary[]): AssetListClient {
  return {
    listProjectLists: vi.fn(async () => lists),
    getList: vi.fn(),
    decide: vi.fn(),
    controlRun: vi.fn(),
    skipItem: vi.fn(),
    listSessionLists: vi.fn(async () => []),
    presence: vi.fn(async () => {}),
  } as unknown as AssetListClient;
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>{children}</SWRConfig>;
}

beforeEach(() => {
  resetProjectStore();
  useStudioStore.setState({ panelTab2d: null, panelTab3d: null, panelCollapsed: true });
});

afterEach(cleanup);

describe('AssetListProgressChip (#328)', () => {
  it('shows the newest confirmed list\'s run status and counts', async () => {
    render(
      <AssetListProgressChip projectId="prj_1" client={clientWith([summary()])} intervalMs={60_000} />,
      { wrapper },
    );

    const chip = await screen.findByTestId('asset-list-progress-chip');
    expect(chip.getAttribute('data-status')).toBe('running');
    expect(screen.getByTestId('asset-list-progress-chip-status').textContent).toBe(messages['ui.asset_list.run_running']);
    expect(screen.getByTestId('asset-list-progress-chip-progress').textContent).toBe(
      formatSchemaText(messages['ui.asset_list.run_progress'], {
        done: 2, total: 5, working: 1, review: 1, attention: 0,
      }),
    );
  });

  it('follows the open project from the store and draws nothing without a run', async () => {
    useProjectStore.setState({ activeProjectId: 'prj_1' });
    const client = clientWith([summary()]);
    render(<AssetListProgressChip client={client} intervalMs={60_000} />, { wrapper });

    await screen.findByTestId('asset-list-progress-chip');
    expect(client.listProjectLists).toHaveBeenCalledWith('prj_1');

    cleanup();
    const draftClient = clientWith([summary({ status: 'draft', effective_status: 'draft', run: null })]);
    render(
      <AssetListProgressChip projectId="prj_1" client={draftClient} intervalMs={60_000} />,
      { wrapper },
    );
    await waitFor(() => expect(draftClient.listProjectLists).toHaveBeenCalled());
    expect(screen.queryByTestId('asset-list-progress-chip')).toBeNull();
  });

  it('opens the project tab of the right panel on click', async () => {
    render(
      <AssetListProgressChip projectId="prj_1" client={clientWith([summary()])} intervalMs={60_000} />,
      { wrapper },
    );

    fireEvent.click(await screen.findByTestId('asset-list-progress-chip'));

    expect(useStudioStore.getState().panelTab2d).toBe('project');
    expect(useStudioStore.getState().panelTab3d).toBe('project');
    expect(useStudioStore.getState().panelCollapsed).toBe(false);
  });
});