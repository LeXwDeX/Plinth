// @vitest-environment jsdom
import React from 'react';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { AdministrationUser } from '@/lib/administration/users-browser-client';
import AdminUsersPage from './page';

const baseSchema = defaultCatalog.application as unknown as ProductSchema;

const state = vi.hoisted(() => ({
  schema: null as unknown,
  users: [] as unknown[],
  createUser: vi.fn(),
  updateUser: vi.fn(),
}));

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: state.schema,
    text: (key: string) =>
      (state.schema as ProductSchema).content.messages[key] ?? '',
  }),
}));

vi.mock('swr', () => ({
  default: (key: unknown[]) => {
    if (key[0] === 'administration-current-user') {
      return { data: 'admin-self' };
    }
    return {
      data: state.users,
      isLoading: false,
      error: undefined,
      mutate: async () => undefined,
    };
  },
}));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

vi.mock('@/lib/administration/users-browser-client', async (importOriginal) => {
  const original = await importOriginal<
    typeof import('@/lib/administration/users-browser-client')
  >();
  return {
    ...original,
    createAdministrationUsersBrowserClient: () => ({
      listUsers: vi.fn(),
      createUser: state.createUser,
      updateUser: state.updateUser,
      deleteUser: vi.fn(),
      currentUserID: vi.fn(),
    }),
  };
});

const user = (overrides: Partial<AdministrationUser>): AdministrationUser => ({
  id: 'user-1',
  username: 'alice',
  nickname: 'Alice',
  name: 'Alice',
  email: 'alice@example.test',
  isAdmin: false,
  isActive: true,
  createdAt: '2026-11-25T09:00:00Z',
  lastLogin: null,
  openId: null,
  projectGroupKey: null,
  ...overrides,
});

const usersCopy = () =>
  (state.schema as ProductSchema).administration.users.presentation;

describe('Administration users page project group (#599)', () => {
  beforeEach(() => {
    state.schema = JSON.parse(JSON.stringify(baseSchema)) as ProductSchema;
    state.users = [
      user({ id: 'user-1', username: 'alice', projectGroupKey: 'studio-b' }),
      user({ id: 'user-2', username: 'bob', projectGroupKey: 'studio-a' }),
      user({ id: 'user-3', username: 'carol', projectGroupKey: 'studio-b' }),
      user({ id: 'user-4', username: 'dave', projectGroupKey: null }),
    ];
    state.createUser.mockReset().mockResolvedValue(user({}));
    state.updateUser.mockReset().mockResolvedValue(user({}));
  });

  afterEach(() => cleanup());

  it('shows a Schema-labelled project group column with the placeholder for ungrouped users', () => {
    const presentation = usersCopy();
    presentation.text.column_project_group = 'Configured group column';
    presentation.empty_placeholder = 'configured-none';
    render(<AdminUsersPage />);

    const table = screen.getByRole('table');
    const headers = within(table).getAllByRole('columnheader').map((cell) => cell.textContent);
    expect(headers).toContain('Configured group column');
    const groupColumn = headers.indexOf('Configured group column');
    const groupOf = (username: string) => {
      const row = within(table).getByText(username).closest('tr')!;
      return within(row).getAllByRole('cell')[groupColumn].textContent;
    };
    expect(groupOf('alice')).toBe('studio-b');
    expect(groupOf('bob')).toBe('studio-a');
    expect(groupOf('dave')).toBe('configured-none');
  });

  // #670: an unreadable time shows the placeholder instead of throwing a RangeError.
  it('shows the placeholder for an unreadable creation or login time', () => {
    usersCopy().empty_placeholder = 'configured-none';
    state.users = [user({ id: 'user-9', username: 'erin', createdAt: 'not a date', lastLogin: 'also bad' })];
    render(<AdminUsersPage />);
    const row = within(screen.getByRole('table')).getByText('erin').closest('tr')!;
    const cells = within(row).getAllByRole('cell').map((cell) => cell.textContent);
    expect(cells.filter((text) => text === 'configured-none').length).toBeGreaterThanOrEqual(2);
  });

  it('creates a user with a project group picked from the existing KEYs', async () => {
    const presentation = usersCopy();
    presentation.text.project_group_label = 'Configured group';
    presentation.text.project_group_placeholder = 'configured group placeholder';
    presentation.text.project_group_description = 'configured group help';
    render(<AdminUsersPage />);
    fireEvent.click(screen.getByRole('button', { name: presentation.text.add_user }));

    const input = await screen.findByLabelText('Configured group');
    expect(input.getAttribute('placeholder')).toBe('configured group placeholder');
    expect(screen.getByText('configured group help')).toBeTruthy();
    const listID = input.getAttribute('list');
    expect(listID).toBeTruthy();
    const datalist = document.getElementById(listID!);
    expect(datalist?.tagName).toBe('DATALIST');
    expect(Array.from(datalist!.querySelectorAll('option')).map((option) => option.value))
      .toEqual(['studio-a', 'studio-b']);

    fireEvent.change(screen.getByLabelText(presentation.text.username_label), {
      target: { value: 'erin' },
    });
    fireEvent.change(screen.getByLabelText(presentation.text.password_label), {
      target: { value: 'secret-123' },
    });
    fireEvent.change(input, { target: { value: 'studio-a' } });
    fireEvent.click(screen.getByRole('button', { name: presentation.text.save }));

    await waitFor(() => expect(state.createUser).toHaveBeenCalledTimes(1));
    expect(state.createUser.mock.calls[0][0]).toMatchObject({
      username: 'erin',
      projectGroupKey: 'studio-a',
    });
  });

  it('edits a user with the current group prefilled and sends an empty KEY to clear it', async () => {
    const presentation = usersCopy();
    render(<AdminUsersPage />);
    const row = within(screen.getByRole('table')).getByText('alice').closest('tr')!;
    fireEvent.click(within(row).getByRole('button', { name: presentation.text.edit }));

    const input = await screen.findByLabelText(presentation.text.project_group_label) as HTMLInputElement;
    expect(input.value).toBe('studio-b');
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: presentation.text.save }));

    await waitFor(() => expect(state.updateUser).toHaveBeenCalledTimes(1));
    expect(state.updateUser.mock.calls[0][0]).toBe('user-1');
    expect(state.updateUser.mock.calls[0][1]).toMatchObject({ projectGroupKey: '' });
  });
});
