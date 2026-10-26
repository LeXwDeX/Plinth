import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  createAuthenticationBrowserClient,
  safeRedirectPath,
} from './browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

describe('safeRedirectPath', () => {
  it('keeps same-origin relative paths', () => {
    expect(safeRedirectPath('/workspace', '/')).toBe('/workspace');
    expect(safeRedirectPath('/canvas?project=one', '/')).toBe(
      '/canvas?project=one',
    );
  });

  it('rejects open-redirect and script values', () => {
    for (const hostile of [
      '//evil.com',
      '/\t/evil.com/landing',
      '/path\x00suffix',
      '/path\x1fsuffix',
      '/\\evil.com',
      'https://evil.com',
      'javascript:alert(1)',
      ' /\r\n/x',
      'workspace',
    ]) {
      expect(safeRedirectPath(hostile, '/fallback')).toBe('/fallback');
    }
  });

  it('falls back for null and empty values', () => {
    expect(safeRedirectPath(null, '/default')).toBe('/default');
    expect(safeRedirectPath('', '/default')).toBe('/default');
  });
});

describe('Authentication Browser Client', () => {
  it('logs in through the Product Schema route and request fields', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.auth_login = '/schema/auth/sign-in';
    schema.authentication.api.username_field = 'account';
    schema.authentication.api.password_field = 'credential';
    let requestedURL = '';
    let requestedBody: unknown;
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      requestedURL = String(input);
      requestedBody = JSON.parse(String(init?.body));
      return Response.json({ configured: true });
    });
    const client = createAuthenticationBrowserClient(schema, { fetcher });

    await client.login('schema-alice', 'schema-password');

    expect(requestedURL).toBe('/schema/auth/sign-in');
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]?.[1]?.method).toBe('POST');
    expect(requestedBody).toEqual({
      account: 'schema-alice',
      credential: 'schema-password',
    });
  });

  it('reports invalid credentials without an SSO contract', async () => {
    const schema = schemaFixture();
    const client = createAuthenticationBrowserClient(schema, {
      fetcher: async () => new Response('', { status: 401 }),
    });
    await expect(client.login('alice', 'invalid')).rejects.toMatchObject({
      status: 401,
      message: schema.authentication.messages.invalid_credentials,
    });
  });

  it('deletes the current session through the Schema route', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.auth_session = '/schema/auth/session';
    const fetcher = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(null, { status: 204 }),
    );
    const client = createAuthenticationBrowserClient(schema, { fetcher });

    await client.logout();

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]?.[0]).toBe('/schema/auth/session');
    expect(fetcher.mock.calls[0]?.[1]?.method).toBe('DELETE');
  });

  it('uses the authentication error field and fallback for failed logout', async () => {
    const schema = schemaFixture();
    schema.authentication.api.error_field = 'problem';
    for (const [body, message] of [
      [JSON.stringify({ problem: 'session unavailable' }), 'session unavailable'],
      ['unavailable', schema.authentication.messages.internal_error],
    ]) {
      const client = createAuthenticationBrowserClient(schema, {
        fetcher: async () => new Response(body, { status: 503 }),
      });
      await expect(client.logout()).rejects.toMatchObject({ status: 503, message });
    }
  });
});
