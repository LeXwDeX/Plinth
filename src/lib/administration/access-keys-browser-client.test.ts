import { describe, expect, it, vi } from 'vitest';
import type { AdministrationAccessKeysSchema } from '@/lib/product-schema';
import {
  AdministrationAccessKeysBrowserError,
  createAdministrationAccessKeysBrowserClient,
} from './access-keys-browser-client';

const schema = {
  api: {
    fields: {
      data: 'rows',
      error: 'issue',
      total: 'itemCount',
      page: 'cursor',
      page_size: 'pageLimit',
      id: 'keyRef',
      owner_id: 'accountRef',
      owner_username: 'login',
      owner_name: 'displayName',
      name: 'title',
      prefix: 'visiblePrefix',
      scopes: 'abilities',
      created_at: 'created',
      last_used_at: 'used',
      request_count: 'hits',
      expires_at: 'expires',
      revoked_at: 'revoked',
      active: 'usable',
      enabled: 'serviceOn',
    },
    query: {
      page: 'cursor',
      page_size: 'limit',
      search: 'find',
      owner_id: 'account',
    },
    path_parameters: { key_id: 'keyRef' },
  },
  limits: {
    default_page_size: 20,
    max_page_size: 100,
    search_max_length: 120,
  },
  messages: {
    invalid_request: 'configured invalid',
    key_not_found: 'configured missing',
    internal_error: 'configured internal',
  },
} as unknown as AdministrationAccessKeysSchema;

const routes = {
  collection: '/configured/keys',
  item: '/configured/keys/{keyRef}',
  switch: '/configured/keys/service',
};

describe('administration access keys browser client', () => {
  it('从 Schema 生成列表请求并隐藏线协议', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(JSON.stringify({
      rows: [{
        keyRef: 'key/one',
        accountRef: 'user/one',
        login: 'alice',
        displayName: 'Alice',
        title: 'CLI',
        visiblePrefix: 'plk_abc',
        abilities: ['read', 'generate'],
        created: '2026-07-26T00:00:00Z',
        used: null,
        hits: 12,
        expires: '2026-10-24T00:00:00Z',
        revoked: null,
        usable: true,
      }],
      itemCount: 1,
      cursor: 2,
      pageLimit: 7,
      serviceOn: false,
    })));
    const client = createAdministrationAccessKeysBrowserClient({
      schema,
      routes,
      fetcher,
    });

    const page = await client.list({
      page: 2,
      pageSize: 7,
      search: 'cat',
      ownerID: 'user/one',
    });

    const requested = new URL(
      String(fetcher.mock.calls[0]?.[0]),
      'https://art-ai.test',
    );
    expect(requested.pathname).toBe('/configured/keys');
    expect(Object.fromEntries(requested.searchParams)).toEqual({
      cursor: '2',
      limit: '7',
      find: 'cat',
      account: 'user/one',
    });
    expect(page).toEqual({
      data: [{
        id: 'key/one',
        ownerId: 'user/one',
        ownerUsername: 'alice',
        ownerName: 'Alice',
        name: 'CLI',
        prefix: 'plk_abc',
        scopes: ['read', 'generate'],
        createdAt: '2026-07-26T00:00:00Z',
        lastUsedAt: null,
        requestCount: 12,
        expiresAt: '2026-10-24T00:00:00Z',
        revokedAt: null,
        active: true,
      }],
      total: 1,
      page: 2,
      pageSize: 7,
      enabled: false,
    });
  });

  it('吊销走带 keyID 的单项路由，204 视为成功', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(null, { status: 204 }));
    const client = createAdministrationAccessKeysBrowserClient({
      schema,
      routes,
      fetcher,
    });

    await client.revoke('key/one');

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(String(fetcher.mock.calls[0]?.[0])).toBe(
      '/configured/keys/key%2Fone',
    );
    expect(fetcher.mock.calls[0]?.[1]?.method).toBe('DELETE');
  });

  it('整体开关按 Schema 字段名发 PUT 并读回状态', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(JSON.stringify({ serviceOn: true })));
    const client = createAdministrationAccessKeysBrowserClient({
      schema,
      routes,
      fetcher,
    });

    await expect(client.setEnabled(false)).resolves.toBe(true);
    expect(String(fetcher.mock.calls[0]?.[0])).toBe('/configured/keys/service');
    expect(fetcher.mock.calls[0]?.[1]?.method).toBe('PUT');
    expect(fetcher.mock.calls[0]?.[1]?.body).toBe(
      JSON.stringify({ serviceOn: false }),
    );
  });

  it('使用 Schema 错误字段并保留 HTTP 状态与请求 ID', async () => {
    const client = createAdministrationAccessKeysBrowserClient({
      schema,
      routes,
      fetcher: vi.fn(async () => new Response(JSON.stringify({
        issue: 'configured missing',
        request_id: 'req_0123456789abcdef',
      }), { status: 404 })),
    });

    await expect(client.revoke('missing')).rejects.toEqual(
      new AdministrationAccessKeysBrowserError(
        404,
        'configured missing',
        'req_0123456789abcdef',
      ),
    );
  });

  it('错误体缺失时回退到 Schema 的内部错误文案', async () => {
    const client = createAdministrationAccessKeysBrowserClient({
      schema,
      routes,
      fetcher: vi.fn(async () => new Response('', { status: 500 })),
    });

    await expect(client.list()).rejects.toMatchObject({
      status: 500,
      message: 'configured internal',
    });
  });
});