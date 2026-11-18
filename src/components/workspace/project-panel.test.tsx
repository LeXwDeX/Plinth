// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { AssetListDetail, AssetListSummary } from '@/lib/asset-lists/client';
import type { ProductSchema } from '@/lib/product-schema';
import { toast } from 'sonner';
import { ProjectPanel } from './project-panel';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
const toastMock = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock('sonner', () => ({ toast: toastMock }));
const mock = vi.hoisted(() => {
  const getStyleGuide = vi.fn();
  const setStyleGuide = vi.fn();
  const getConventions = vi.fn();
  const setConventions = vi.fn();
  const listAssets = vi.fn();
  const deleteAsset = vi.fn();
  const promoteAsset = vi.fn();
  return {
    activeProjectId: 'prj_1' as string | null,
    activeProject: { id: 'prj_1', name: 'Star Harbor', createdAt: '', updatedAt: '' } as { id: string; name: string; createdAt: string; updatedAt: string } | null,
    guide: {
      projectId: 'prj_1', enabled: true, text: 'warm ink', palette: ['#ffffff'],
      references: [{ input_asset_id: 'style-1' }], updatedAt: 'u',
    },
    conventions: { projectId: 'prj_1', brief: { purpose: 'character' }, texts: { naming: 'hero_' }, updatedAt: 'u' },
    assets: [{
      id: 'a1', projectId: 'prj_1', kind: 'character', name: 'Role A', description: 'red coat',
      images: [{ task_id: 't1', asset_id: 'i1' }], models: [], createdAt: 'c', updatedAt: 'u',
    }],
    getStyleGuide, setStyleGuide, getConventions, setConventions, listAssets, deleteAsset, promoteAsset,
    client: { getStyleGuide, setStyleGuide, getConventions, setConventions, listAssets, deleteAsset, promoteAsset },
  };
});

const assetLists = vi.hoisted(() => {
  const listProjectLists = vi.fn();
  const getList = vi.fn();
  const decide = vi.fn();
  const controlRun = vi.fn();
  const skipItem = vi.fn();
  const listSessionLists = vi.fn();
  return {
    listProjectLists, getList, decide, controlRun, skipItem, listSessionLists,
    client: { listProjectLists, getList, decide, controlRun, skipItem, listSessionLists },
  };
});
vi.mock('@/lib/asset-lists/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/asset-lists/client')>();
  return { ...actual, assetListClient: () => assetLists.client };
});

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));
vi.mock('@/hooks/use-projects', () => ({
  useProjects: () => ({
    projects: mock.activeProject ? [mock.activeProject] : [],
    loaded: true, loading: false, failed: false,
    activeProjectId: mock.activeProjectId, activeProject: mock.activeProject,
    client: mock.client,
  }),
}));
vi.mock('@/hooks/use-style-lock', () => ({
  useStyleLock: () => ({
    lock: { enabled: true, text: 'session ink', references: ['upload-9'], source: 'session' },
    busy: false, failed: false, saveError: null, refresh: vi.fn(), save: vi.fn(),
  }),
}));
vi.mock('@/lib/chat/browser-client', () => ({ createChatBrowserClient: () => ({}) }));
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: object) => unknown) => selector({
    messagesBySession: {
      s1: [{
        id: 'm1', role: 'assistant', content: 'done', createdAt: 1,
        images: [{ url: '/api/v1/tasks/t1/assets/i1' }],
        attachments: [{ handle: '@img1', kind: 'image', role: 'generated', taskId: 't1', assetId: 'i1', imageSlot: 1 }],
      }],
    },
  }),
}));

afterEach(cleanup);
beforeEach(() => {
  mock.activeProjectId = 'prj_1';
  mock.activeProject = { id: 'prj_1', name: 'Star Harbor', createdAt: '', updatedAt: '' };
  mock.getStyleGuide.mockResolvedValue(mock.guide);
  mock.getConventions.mockResolvedValue(mock.conventions);
  mock.listAssets.mockResolvedValue(mock.assets);
  mock.setStyleGuide.mockResolvedValue(mock.guide);
  mock.setConventions.mockResolvedValue(mock.conventions);
  mock.deleteAsset.mockResolvedValue(undefined);
  mock.promoteAsset.mockResolvedValue({ ...mock.assets[0], id: 'a2', name: 'Role B' });
  assetLists.listProjectLists.mockResolvedValue([]);
  assetLists.getList.mockRejectedValue(new Error('unexpected list read'));
  assetLists.decide.mockResolvedValue({});
  assetLists.controlRun.mockResolvedValue({
    status: 'paused',
    pause_reason: 'user',
    item_counts: { todo: 1, working: 0, review: 0, accepted: 0, returned: 0, failed: 0, skipped: 0 },
  });
  assetLists.skipItem.mockResolvedValue({});
  assetLists.listSessionLists.mockResolvedValue([]);
  for (const fn of [mock.getStyleGuide, mock.setStyleGuide, mock.getConventions, mock.setConventions, mock.listAssets, mock.deleteAsset, mock.promoteAsset, assetLists.listProjectLists, assetLists.getList, assetLists.decide, assetLists.controlRun, assetLists.skipItem, assetLists.listSessionLists]) fn.mockClear();
  toastMock.success.mockClear();
  toastMock.error.mockClear();
});

function listSummary(overrides: Partial<AssetListSummary> = {}): AssetListSummary {
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

function listDetail(overrides: Partial<AssetListDetail> = {}): AssetListDetail {
  return {
    ...listSummary(),
    version: 1,
    brief: {},
    items: [],
    ...overrides,
  };
}

describe('project panel', () => {
  it('asks for a project when the session is unassigned', () => {
    mock.activeProjectId = null;
    mock.activeProject = null;
    render(<ProjectPanel sessionId="s1" />);
    expect(screen.getByTestId('project-panel-empty').textContent).toBe(messages['ui.project.no_project_hint']);
  });

  it('loads the style guide, conventions and assets and saves only a changed guide', async () => {
    render(<ProjectPanel sessionId="s1" />);
    const textarea = await screen.findByTestId('project-style-text') as HTMLTextAreaElement;
    expect(textarea.value).toBe('warm ink');
    expect((screen.getByTestId('project-style-save') as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId('project-asset-a1').textContent).toContain('Role A');
    expect((screen.getByTestId('project-conventions-text-naming') as HTMLInputElement).value).toBe('hero_');
    fireEvent.change(textarea, { target: { value: 'cool ink' } });
    fireEvent.click(screen.getByTestId('project-style-save'));
    await waitFor(() => expect(mock.setStyleGuide).toHaveBeenCalledWith('prj_1', expect.objectContaining({ text: 'cool ink', enabled: true })));
  });

  it('confirms an asset deletion before sending it', async () => {
    render(<ProjectPanel sessionId="s1" />);
    await screen.findByTestId('project-asset-a1');
    fireEvent.click(screen.getByTestId('project-asset-delete-a1'));
    expect(mock.deleteAsset).not.toHaveBeenCalled();
    expect(screen.getByTestId('project-asset-delete-confirm-a1')).toBeTruthy();
    fireEvent.click(screen.getByTestId('project-asset-delete-confirm-button-a1'));
    await waitFor(() => expect(mock.deleteAsset).toHaveBeenCalledWith('prj_1', 'a1'));
  });

  it('promotes a finished session picture only after the dialog confirms', async () => {
    render(<ProjectPanel sessionId="s1" />);
    await screen.findByTestId('project-promote-picture-1');
    fireEvent.click(screen.getByTestId('project-promote-picture-1'));
    expect(mock.promoteAsset).not.toHaveBeenCalled();
    fireEvent.change(screen.getByTestId('promote-asset-name'), { target: { value: 'Role B' } });
    fireEvent.click(screen.getByTestId('promote-asset-confirm'));
    await waitFor(() => expect(mock.promoteAsset).toHaveBeenCalledWith('prj_1', expect.objectContaining({
      source: { task_id: 't1', asset_id: 'i1' }, kind: 'character', name: 'Role B',
    })));
  });

  it('promotes the session style lock into the project guide after confirmation', async () => {
    render(<ProjectPanel sessionId="s1" />);
    await waitFor(() => expect((screen.getByTestId('project-promote-style') as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByTestId('project-promote-style'));
    expect(mock.setStyleGuide).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('project-promote-style-confirm'));
    await waitFor(() => expect(mock.setStyleGuide).toHaveBeenCalledWith('prj_1', expect.objectContaining({
      enabled: true, text: 'session ink', references: [{ input_asset_id: 'upload-9' }],
      palette: ['#ffffff'],
    })));
  });

  it('does not promote the style lock before the project guide is loaded', async () => {
    let resolveGuide!: (value: typeof mock.guide) => void;
    mock.getStyleGuide.mockReturnValue(new Promise((resolve) => { resolveGuide = resolve; }));
    render(<ProjectPanel sessionId="s1" />);
    const button = screen.getByTestId('project-promote-style') as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
    expect(screen.queryByTestId('project-promote-style-confirm-dialog')).toBeNull();
    expect(mock.setStyleGuide).not.toHaveBeenCalled();

    await act(async () => { resolveGuide(mock.guide); });
    await waitFor(() => expect((screen.getByTestId('project-promote-style') as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByTestId('project-promote-style'));
    fireEvent.click(screen.getByTestId('project-promote-style-confirm'));
    await waitFor(() => expect(mock.setStyleGuide).toHaveBeenCalledWith('prj_1', expect.objectContaining({
      palette: ['#ffffff'],
    })));
  });

  it('reports the server reason when a guide save fails', async () => {
    mock.setStyleGuide.mockRejectedValue(new Error('风格说明不能超过 2000 个字符。'));
    render(<ProjectPanel sessionId="s1" />);
    const textarea = await screen.findByTestId('project-style-text') as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: 'cool ink' } });
    fireEvent.click(screen.getByTestId('project-style-save'));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('风格说明不能超过 2000 个字符。'));
    expect(toast.error).not.toHaveBeenCalledWith(messages['ui.project.load_failed']);
  });

  it('shows the asset list section and its empty state', async () => {
    render(<ProjectPanel sessionId="s1" />);
    expect(screen.getByTestId('project-asset-lists-section')).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId('project-asset-lists-empty').textContent).toBe(messages['ui.asset_list.empty']));
  });

  it('shows a draft list as a card with its decisions', async () => {
    assetLists.listProjectLists.mockResolvedValue([listSummary({ id: 'list_draft' })]);
    assetLists.getList.mockResolvedValue(listDetail({ id: 'list_draft' }));
    render(<ProjectPanel sessionId="s1" />);
    const card = await screen.findByTestId('asset-list-card');
    expect(card.getAttribute('data-variant')).toBe('panel');
    expect(screen.getByTestId('asset-list-confirm')).toBeTruthy();
    expect(screen.getByTestId('asset-list-reject')).toBeTruthy();
    expect(screen.queryByTestId('project-asset-lists-empty')).toBeNull();
  });

  it('shows a confirmed list\'s run and sends its controls from the panel card', async () => {
    const run = {
      status: 'running',
      pause_reason: '',
      item_counts: { todo: 1, working: 1, review: 0, accepted: 0, returned: 0, failed: 0, skipped: 0 },
    };
    assetLists.listProjectLists.mockResolvedValue([
      listSummary({ id: 'list_confirmed', status: 'confirmed', effective_status: 'confirmed', run }),
    ]);
    assetLists.getList.mockResolvedValue(listDetail({
      id: 'list_confirmed', status: 'confirmed', effective_status: 'confirmed', version: 2, run,
    }));
    render(<ProjectPanel sessionId="s1" />);

    const card = await screen.findByTestId('asset-list-card');
    expect(card.textContent).toContain(messages['ui.asset_list.run_running']);
    expect(screen.getByTestId('asset-list-run-progress')).toBeTruthy();

    fireEvent.click(screen.getByTestId('asset-list-run-pause'));
    await waitFor(() => expect(assetLists.controlRun).toHaveBeenCalledWith(
      'prj_1', 'list_confirmed', 'pause', 2,
    ));
    // The run change re-reads the project list and the card's own detail.
    await waitFor(() => expect(assetLists.listProjectLists).toHaveBeenCalledTimes(2));
  });

  it('shows a decided or expired list as a light row', async () => {
    const states = schema.asset_list?.states ?? [];
    assetLists.listProjectLists.mockResolvedValue([
      listSummary({ id: 'list_rejected', title: 'Rejected set', status: 'rejected', effective_status: 'rejected' }),
      listSummary({ id: 'list_expired', title: 'Expired set', status: 'confirmed', effective_status: 'expired' }),
    ]);
    render(<ProjectPanel sessionId="s1" />);
    const rejected = await screen.findByTestId('project-asset-list-list_rejected');
    expect(rejected.textContent).toContain('Rejected set');
    expect(rejected.textContent).toContain(states.find((state) => state.id === 'rejected')?.label);
    const expired = screen.getByTestId('project-asset-list-list_expired');
    expect(expired.textContent).toContain('Expired set');
    expect(expired.textContent).toContain(messages['ui.asset_list.expired']);
    expect(screen.queryByTestId('asset-list-card')).toBeNull();
  });

  it('keeps the other sections when the list read fails', async () => {
    assetLists.listProjectLists.mockRejectedValue(new Error('offline'));
    render(<ProjectPanel sessionId="s1" />);
    const failed = await screen.findByTestId('project-asset-lists-failed');
    expect(failed.textContent).toBe(messages['ui.asset_list.load_failed']);
    expect(screen.getByTestId('project-asset-a1').textContent).toContain('Role A');
  });
});