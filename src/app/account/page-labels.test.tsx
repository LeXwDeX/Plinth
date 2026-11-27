// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import AccountPage from './page';
import type { AccountResources, AccountUsage } from '@/lib/account-resources';
import type { ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages as Record<string, string>;
const roleLabels = schema.task_history.assets.attachments.role_labels;
const label = (id: string) => schema.capabilities.find((capability) => capability.id === id)!.label_cn;

const state = vi.hoisted(() => ({
  usage: undefined as unknown,
  pages: undefined as unknown,
  opened: [] as Array<{ title: string; group?: Array<{ title: string }> }>,
}));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('swr', () => ({ default: () => ({ data: state.usage, isLoading: false }) }));
vi.mock('swr/infinite', () => ({ default: () => ({ data: state.pages, isLoading: false, size: 1, setSize: vi.fn() }) }));
vi.mock('@/components/result/result-detail', () => ({ ResultDetailHost: () => null, ResultDetailDialog: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({
  openResultDetail: (target: { title: string; group?: Array<{ title: string }> }) => { state.opened.push(target); },
}));

const taskID = '0b6f6f2e-8c3a-4d0b-9a55-6a6a9f0c1d2e';
const resources: AccountResources = {
  next_cursor: '',
  uploads: { data: Array.from({ length: 2 }, (_, index) => ({
    id: `up${index}`, original_filename: `u${index}.png`, media_type: 'image/png', size: 10, url: `/api/v1/uploads/up${index}`, created_at: '2026-11-20T00:00:00Z',
  })) as unknown as AccountResources['uploads']['data'], total: 12, page: 1, page_size: 2 },
  tasks: [{
    id: taskID, capability: 'image-to-3d', state: 'SUCCEEDED', created_at: '2026-11-20T08:30:00Z',
    assets: [
      { id: 'a1', role: 'model', media_type: 'model/gltf-binary', size: 10, url: `/api/v1/tasks/${taskID}/assets/a1` },
      { id: 'a2', role: 'thumbnail', media_type: 'image/png', size: 10, url: `/api/v1/tasks/${taskID}/assets/a2` },
    ],
  }],
};

afterEach(() => { cleanup(); state.opened = []; });

// #670: the account page names capabilities, asset roles and pages in product copy, not raw ids.
describe('account page labels', () => {
  it('shows the capability label and time instead of the capability id and task UUID', () => {
    state.usage = undefined;
    state.pages = [resources];
    render(<AccountPage />);
    const open = screen.getByTestId('account-task-open');
    expect(open.textContent).toContain(label('image-to-3d'));
    expect(open.textContent).not.toContain('image-to-3d');
    expect(open.textContent).not.toContain(taskID);
    const downloads = screen.getAllByRole('link').map((link) => link.textContent ?? '');
    expect(downloads.some((text) => text.includes(roleLabels.model))).toBe(true);
    expect(downloads.some((text) => text.includes(roleLabels.thumbnail))).toBe(true);
    expect(downloads.join(' ')).not.toMatch(/\b(model|thumbnail)\b/);
  });

  it('titles the result detail with the capability and role labels', () => {
    state.usage = undefined;
    state.pages = [resources];
    render(<AccountPage />);
    fireEvent.click(screen.getByTestId('account-task-open'));
    const titles = [state.opened[0].title, ...(state.opened[0].group ?? []).map((item) => item.title)];
    expect(titles).toContain(`${label('image-to-3d')} · ${roleLabels.model}`);
    for (const title of titles) {
      expect(title).not.toContain('image-to-3d');
      expect(title).not.toMatch(/\b(model|thumbnail)\b/);
    }
  });

  it('titles a task without results by capability and time, not by its UUID', () => {
    state.usage = undefined;
    state.pages = [{ ...resources, tasks: [{ ...resources.tasks[0], assets: [] }] }];
    render(<AccountPage />);
    fireEvent.click(screen.getByTestId('account-task-open'));
    expect(state.opened[0].title).toContain(label('image-to-3d'));
    expect(state.opened[0].title).not.toContain(taskID);
  });

  it('labels usage by feature with capability names', () => {
    state.usage = { user_id: 'owner', total_tasks: 4, estimated_credits: 4, by_type: { 'text-to-image': 3, 'image-to-3d': 1 } } as unknown as AccountUsage;
    state.pages = [resources];
    render(<AccountPage />);
    expect(screen.getByText(messages['ui.account.usage_by_type'])).toBeTruthy();
    expect(screen.queryByText('用量范围')).toBeNull();
    expect(screen.getByText(`${label('text-to-image')}: 3 · ${label('image-to-3d')}: 1`)).toBeTruthy();
  });

  it('writes the upload pager as one sentence', () => {
    state.usage = undefined;
    state.pages = [resources];
    render(<AccountPage />);
    const expected = messages['ui.account.page_of'].replace('{page}', '1').replace('{total}', '12');
    expect(screen.getByText(expected)).toBeTruthy();
  });
});
