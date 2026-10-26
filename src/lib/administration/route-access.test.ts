import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { projectProductRoute } from '@/lib/administration/route-access';
import type { ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;
const administrableKeys = [
  'dashboard',
  'users',
  'audit',
  'generations',
  'user-assets',
  'agent-configuration',
  'llm',
];

function route(pathname: string, isAdmin: boolean) {
  return projectProductRoute({
    schema,
    pathname,
    isAdmin,
    administrableKeys,
  });
}

describe('administration route access', () => {
  it('keeps the public routes reachable for every signed-in user', () => {
    expect(route('/', false)).toEqual({ kind: 'workspace' });
    expect(route('/', true)).toEqual({ kind: 'workspace' });
    expect(route(schema.runtime.frontend.client_routes.account, false)).toEqual({ kind: 'account' });
    expect(route(schema.runtime.frontend.client_routes.account, true)).toEqual({ kind: 'account' });
    expect(route('/login', false)).toEqual({ kind: 'login' });
  });

  it('refuses every administration destination for a non administrator', () => {
    for (const item of schema.administration.navigation) {
      expect(route(item.href, false)).toEqual({ kind: 'forbidden' });
      if (item.match === 'prefix') {
        expect(route(`${item.href}/detail`, false)).toEqual({ kind: 'forbidden' });
      }
    }
  });

  it('refuses administration destinations for a non administrator before the session resolves', () => {
    // 登出或会话尚未取回时 isAdmin 只能是 false：此时不得闪现管理外壳。
    expect(route('/admin', false)).toEqual({ kind: 'forbidden' });
  });

  it('resolves administration destinations for an administrator', () => {
    for (const item of schema.administration.navigation) {
      expect(route(item.href, true)).toEqual({
        kind: 'administration',
        key: item.key,
      });
    }
    expect(route('/admin/users/detail', true)).toEqual({
      kind: 'administration',
      key: 'users',
    });
  });

  it('reports unrouted paths as not found rather than forbidden', () => {
    expect(route('/text-to-image', true)).toEqual({ kind: 'not-found' });
    expect(route('/text-to-image', false)).toEqual({ kind: 'not-found' });
    expect(route('/administer', true)).toEqual({ kind: 'not-found' });
  });

  it('does not render a navigation entry without a registered page', () => {
    const extended = structuredClone(schema);
    extended.administration.navigation.push({
      key: 'unregistered',
      href: '/admin/unregistered',
      label: '未接线',
      icon: 'x',
      match: 'exact',
    });
    expect(
      projectProductRoute({
        schema: extended,
        pathname: '/admin/unregistered',
        isAdmin: true,
        administrableKeys,
      }),
    ).toEqual({ kind: 'not-found' });
  });
});
