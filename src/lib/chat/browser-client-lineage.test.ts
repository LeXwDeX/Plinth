import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from './browser-client';

const schema = catalog.application as unknown as ProductSchema;

describe('lineage client', () => {
  it('keeps legacy calls unpaged and opts into Schema-sized pages with cursor', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ nodes: [], edges: [], truncated: false, next_cursor: '' }), { status: 200 }));
    const client = createChatBrowserClient(schema, { fetcher });
    await client.getLineage('session/a');
    await client.getLineagePage('session/a');
    await client.getLineagePage('session/a', 'older+/=');
    const route = schema.runtime.http.routes.chat_session_lineage!.replace('{sessionID}', 'session%2Fa');
    expect(fetcher.mock.calls[0]?.[0]).toBe(route);
    expect(fetcher.mock.calls[1]?.[0]).toBe(`${route}?cursor=`);
    expect(fetcher.mock.calls[2]?.[0]).toBe(`${route}?cursor=older%2B%2F%3D`);
  });
});
