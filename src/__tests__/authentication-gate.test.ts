import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { projectBrowserAccess } from '../../web/src/authentication-gate';

describe('password login routing', () => {
  it('uses the login query contract to preserve the requested path', () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    schema.authentication.login.redirect_query = 'next';
    schema.runtime.frontend.client_routes.login = '/sign-in';
    expect(projectBrowserAccess({ schema, requestedPath: '/account?tab=assets', isLoading: false, hasUser: false }))
      .toEqual({ state: 'redirect', href: '/sign-in?next=%2Faccount%3Ftab%3Dassets' });
  });

  it('keeps public pages accessible and waits for protected session checks', () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    expect(projectBrowserAccess({ schema, requestedPath: '/login?sso=auto', isLoading: true, hasUser: false }))
      .toEqual({ state: 'render' });
    expect(projectBrowserAccess({ schema, requestedPath: '/account', isLoading: true, hasUser: false }))
      .toEqual({ state: 'loading' });
    expect(projectBrowserAccess({ schema, requestedPath: '/account', isLoading: false, hasUser: true }))
      .toEqual({ state: 'render' });
  });

  // #62: a 429 (or any non-401 failure) on the session request says nothing about the login.
  it('does not send the visitor to the login page when the session request fails', () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    expect(projectBrowserAccess({ schema, requestedPath: '/', isLoading: false, hasUser: false, sessionFailure: 'rate_limited' }))
      .toEqual({ state: 'session-failed', failure: 'rate_limited' });
    expect(projectBrowserAccess({ schema, requestedPath: '/', isLoading: false, hasUser: false, sessionFailure: 'unavailable' }))
      .toEqual({ state: 'session-failed', failure: 'unavailable' });
    expect(projectBrowserAccess({ schema, requestedPath: '/', isLoading: false, hasUser: true, sessionFailure: 'rate_limited' }))
      .toEqual({ state: 'render' });
    expect(projectBrowserAccess({ schema, requestedPath: '/', isLoading: false, hasUser: false, sessionFailure: null }).state)
      .toBe('redirect');
  });
});
