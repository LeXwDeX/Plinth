// @vitest-environment jsdom

import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import LoginPage from '@/app/login/page';
import { AppTopbar } from './app-topbar';
import type { ProductSchema } from '@/lib/product-schema';

const base = defaultCatalog.application as unknown as ProductSchema;
const englishFields = (defaultCatalog.application as unknown as {
  localization: { bundles: { en: { fields: Record<string, string> } } };
}).localization.bundles.en.fields;
const commit = '0123456789abcdef0123456789abcdef01234567';

const state = vi.hoisted(() => ({ schema: undefined as unknown }));
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: state.schema, text: () => '', refreshing: false }),
}));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner', name: 'Owner' }, isAdmin: false }) }));
vi.mock('@/components/workspace/workspace-balance', () => ({ WorkspaceBalance: () => null }));
vi.mock('@/lib/browser-navigation', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useSearchParams: () => new URLSearchParams(),
}));

/** The schema as the server projects it for a locale: Chinese is the catalog, English applies its field bundle. */
function projected(locale: 'zh' | 'en'): ProductSchema {
  const schema = structuredClone(base) as unknown as Record<string, unknown>;
  if (locale === 'en') {
    for (const [path, value] of Object.entries(englishFields)) {
      const keys = path.split('.');
      let node = schema as Record<string, unknown> | undefined;
      for (const key of keys.slice(0, -1)) {
        node = node?.[key] as Record<string, unknown> | undefined;
      }
      if (node && typeof node[keys[keys.length - 1]] === 'string') {
        node[keys[keys.length - 1]] = value;
      }
    }
  }
  return schema as unknown as ProductSchema;
}

function withService(locale: 'zh' | 'en', release: string | undefined): ProductSchema {
  const schema = projected(locale);
  schema.content.service = { ...schema.content.service, version: commit, ...(release === undefined ? {} : { release }) };
  return schema;
}

afterEach(() => cleanup());

// #676: the brand and the login page show the release, never the commit.
describe.each(['zh', 'en'] as const)('release label (%s)', (locale) => {
  it.each([
    ['v1.9.5', 'v1.9.5'],
    ['v1.9.4-12-gfbf5000', 'v1.9.4-12-gfbf5000'],
    ['1.9.5', `${base.content.brand.version_prefix}1.9.5`],
  ])('top bar shows release %s next to the brand, without the commit', (release, shown) => {
    state.schema = withService(locale, release);
    render(<AppTopbar />);
    const label = screen.getByTestId('app-topbar-release');
    expect(label.textContent).toBe(shown);
    // In the same md+ group as the brand title, so it hides with it on narrow screens.
    expect(label.parentElement?.className).toContain('hidden');
    expect(label.parentElement?.className).toContain('md:inline-flex');
    expect(label.parentElement?.textContent).toBe(`${base.content.brand.title}${shown}`);
    const header = screen.getByTestId('app-topbar');
    expect(header.innerHTML).not.toContain(commit);
    expect(header.innerHTML).not.toMatch(/[0-9a-f]{40}/);
  });

  it.each([undefined, '', '  '])('top bar renders no version and no lone prefix when release is %j', (release) => {
    state.schema = withService(locale, release);
    render(<AppTopbar />);
    expect(screen.queryByTestId('app-topbar-release')).toBeNull();
    const brand = screen.getByText(base.content.brand.title);
    expect(brand.parentElement?.textContent).toBe(base.content.brand.title);
    expect(screen.getByTestId('app-topbar').innerHTML).not.toContain(commit);
  });

  it('login page shows the release, not the commit', () => {
    state.schema = withService(locale, 'v1.9.5');
    const { container } = render(<LoginPage />);
    expect(screen.getByTestId('login-release').textContent).toBe('v1.9.5');
    expect(container.innerHTML).not.toMatch(/[0-9a-f]{40}/);
  });

  it.each([undefined, ''])('login page shows no version when release is %j', (release) => {
    state.schema = withService(locale, release);
    const { container } = render(<LoginPage />);
    expect(screen.queryByTestId('login-release')).toBeNull();
    expect(container.innerHTML).not.toContain(commit);
    expect(container.textContent).not.toMatch(new RegExp(`(^|\\s)${base.content.brand.version_prefix}(\\s|$)`));
  });
});
