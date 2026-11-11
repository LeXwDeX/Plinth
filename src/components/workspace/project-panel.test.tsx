// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
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
  for (const fn of [mock.getStyleGuide, mock.setStyleGuide, mock.getConventions, mock.setConventions, mock.listAssets, mock.deleteAsset, mock.promoteAsset]) fn.mockClear();
  toastMock.success.mockClear();
  toastMock.error.mockClear();
});

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

  it('reports a save-specific line when a conventions save fails without a server reason', async () => {
    mock.setConventions.mockRejectedValue(new Error(''));
    render(<ProjectPanel sessionId="s1" />);
    const brief = await screen.findByTestId('project-conventions-brief') as HTMLTextAreaElement;
    fireEvent.change(brief, { target: { value: '{"purpose":"game_character"}' } });
    fireEvent.click(screen.getByTestId('project-conventions-save'));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(messages['ui.project.conventions_save_failed']));
  });
});