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
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import AdminAccessKeysPage from './page';

// jsdom has no PointerEvent; the Base UI switch reads it when clicked.
class TestPointerEvent extends MouseEvent {
  readonly pointerId: number;

  readonly pointerType: string;

  readonly isPrimary: boolean;

  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init);
    this.pointerId = init.pointerId ?? 1;
    this.pointerType = init.pointerType ?? 'mouse';
    this.isPrimary = init.isPrimary ?? true;
  }
}

beforeAll(() => {
  Object.defineProperty(window, 'PointerEvent', {
    configurable: true,
    writable: true,
    value: TestPointerEvent,
  });
});

const baseSchema = defaultCatalog.application as unknown as ProductSchema;

const state = vi.hoisted(() => ({
  schema: null as unknown,
  page: undefined as unknown,
  owners: [] as unknown[],
  swrCalls: [] as string[],
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
    state.swrCalls.push(String(key[0]));
    if (key[0] === 'administration-access-keys-owners') {
      return { data: state.owners, isLoading: false };
    }
    return {
      data: state.page,
      isLoading: false,
      error: undefined,
      mutate: async () => undefined,
    };
  },
}));

const cloneSchema = (): ProductSchema =>
  JSON.parse(JSON.stringify(baseSchema)) as ProductSchema;

const accessKey = (overrides: Record<string, unknown> = {}) => ({
  id: 'key-1',
  ownerId: 'owner-a',
  ownerUsername: 'alice',
  ownerName: 'Alice',
  name: 'deploy bot',
  prefix: 'plk_key-1',
  scopes: ['read', 'generate'],
  createdAt: '2026-11-18T09:00:00Z',
  lastUsedAt: '2026-11-18T10:00:00Z',
  requestCount: 12,
  expiresAt: '2026-12-18T09:00:00Z',
  revokedAt: null,
  active: true,
  ...overrides,
});

const page = (overrides: Record<string, unknown> = {}) => ({
  data: [accessKey()],
  total: 1,
  page: 1,
  pageSize: 20,
  enabled: true,
  ...overrides,
});

beforeEach(() => {
  state.schema = cloneSchema();
  state.page = page();
  state.owners = [];
  state.swrCalls = [];
});

afterEach(cleanup);

describe('administration access keys page (#332)', () => {
  it('renders an unavailable state, not a crash, when the Schema section is absent', () => {
    const schema = cloneSchema();
    delete schema.administration.access_keys;
    state.schema = schema;
    render(<AdminAccessKeysPage />);
    expect(screen.getByTestId('admin-access-keys-unavailable')).toBeTruthy();
    expect(
      screen.getByText('无法使用，当前产品配置未启用访问 Key 管理'),
    ).toBeTruthy();
    // The missing section is not read for a list or an owner dropdown.
    expect(state.swrCalls).toEqual([]);
  });

  it('lists keys with column headers, usage and the owner', () => {
    render(<AdminAccessKeysPage />);
    expect(screen.getByText('deploy bot')).toBeTruthy();
    expect(screen.getByText('Alice')).toBeTruthy();
    expect(screen.getByText('12')).toBeTruthy();
    expect(screen.getByText('有效')).toBeTruthy();
    // Every table heading names its column for assistive technology.
    expect(document.querySelectorAll('th[scope="col"]')).toHaveLength(11);
    // The revoke control names the key it revokes, not just "revoke".
    expect(screen.getByRole('button', { name: '吊销 deploy bot' })).toBeTruthy();
    // The overall switch shows the list's enabled state.
    expect(
      screen
        .getByRole('switch', { name: '对外 MCP 与访问 Key' })
        .getAttribute('aria-checked'),
    ).toBe('true');
  });

  it('names the key in the revoke confirmation', async () => {
    render(<AdminAccessKeysPage />);
    fireEvent.click(screen.getByRole('button', { name: '吊销 deploy bot' }));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText(
        '吊销后 Key「deploy bot」（plk_key-1）立即失效，且不能恢复。确定吊销？',
      ),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        within(dialog).getByTestId('access-keys-revoke-confirm'),
      ).toBeTruthy(),
    );
  });

  it('asks before switching the whole service off', async () => {
    render(<AdminAccessKeysPage />);
    fireEvent.click(screen.getByRole('switch', { name: '对外 MCP 与访问 Key' }));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText(
        '关闭后，所有访问 Key 与短时签名下载立即被拒绝；重新开启后恢复。',
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByTestId('access-keys-switch-confirm').textContent,
    ).toContain('关闭');
  });
});