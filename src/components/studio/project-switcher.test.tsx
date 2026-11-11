// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ProjectSwitcher } from './project-switcher';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
const mock = vi.hoisted(() => ({
  activeProject: { id: 'prj_1', name: 'Star Harbor', createdAt: '', updatedAt: '' } as { id: string; name: string; createdAt: string; updatedAt: string } | null,
  activeProjectId: 'prj_1' as string | null,
  projects: [
    { id: 'prj_1', name: 'Star Harbor', createdAt: '', updatedAt: '' },
    { id: 'prj_2', name: 'Other', createdAt: '', updatedAt: '' },
  ],
  select: vi.fn(async () => {}),
  create: vi.fn(async () => ({ id: 'prj_3', name: 'New', createdAt: '', updatedAt: '' })),
  refresh: vi.fn(async () => {}),
}));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-projects', () => ({
  useProjects: () => ({
    projects: mock.projects, loaded: true, loading: false, failed: false,
    activeProjectId: mock.activeProjectId, activeProject: mock.activeProject,
    select: mock.select, create: mock.create, refresh: mock.refresh,
  }),
}));
vi.mock('@/store/chat-store', () => ({ useChatStore: () => 's1' }));

afterEach(cleanup);
beforeEach(() => {
  mock.select.mockClear();
  mock.create.mockClear();
});

describe('project switcher', () => {
  it('shows the open session project and chooses another one', async () => {
    render(<ProjectSwitcher />);
    expect(screen.getByTestId('project-switcher-trigger').textContent).toContain('Star Harbor');
    fireEvent.click(screen.getByTestId('project-switcher-trigger'));
    fireEvent.click(await screen.findByTestId('project-switcher-project-prj_2'));
    await waitFor(() => expect(mock.select).toHaveBeenCalledWith('prj_2'));
  });

  it('creates a project from the typed name and selects it', async () => {
    mock.activeProject = null;
    mock.activeProjectId = null;
    render(<ProjectSwitcher />);
    expect(screen.getByTestId('project-switcher-trigger').textContent).toContain(messages['ui.project.none']);
    fireEvent.click(screen.getByTestId('project-switcher-trigger'));
    const input = await screen.findByTestId('project-switcher-name');
    fireEvent.change(input, { target: { value: 'New' } });
    fireEvent.click(screen.getByTestId('project-switcher-create'));
    await waitFor(() => expect(mock.create).toHaveBeenCalledWith('New'));
  });

  it('offers clearing the session project', async () => {
    render(<ProjectSwitcher />);
    fireEvent.click(screen.getByTestId('project-switcher-trigger'));
    fireEvent.click(await screen.findByTestId('project-switcher-clear'));
    await waitFor(() => expect(mock.select).toHaveBeenCalledWith(null));
  });
});