import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createAccountResourcesClient } from './account-resources';

describe('account resources browser client', () => {
  it('uses Product Schema routes and encodes owner-independent filters', async () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    schema.runtime.http.routes.account_usage = '/custom/account/usage';
    schema.runtime.http.routes.account_resources = '/custom/account/resources';
    const calls: string[] = [];
    const fetcher = vi.fn(async (url: RequestInfo | URL) => {
      calls.push(String(url));
      return new Response(JSON.stringify({}), { status: 200 });
    });
    const client = createAccountResourcesClient(schema, fetcher);

    await client.getUsage({ startDate: '2026-10-01', endDate: '2026-10-03' });
    await client.getResources({ cursor: 'cursor / 1', page: 2, pageSize: 25, limit: 25 });

    expect(calls).toEqual([
      '/custom/account/usage?start_date=2026-10-01&end_date=2026-10-03',
      '/custom/account/resources?cursor=cursor+%2F+1&limit=25&page=2&page_size=25',
    ]);
    expect(calls.some((url) => url.includes('user_id') || url.includes('userId'))).toBe(false);
  });

  it('surfaces server error detail', async () => {
    const schema = defaultCatalog.application as unknown as ProductSchema;
    const client = createAccountResourcesClient(schema, async () =>
      new Response(JSON.stringify({ detail: 'error.unavailable' }), { status: 503 }),
    );
    await expect(client.getResources({})).rejects.toThrow('error.unavailable');
  });
});

 it('keeps native usage grouped without fabricating legacy fields', async () => {
 const schema = defaultCatalog.application as unknown as ProductSchema;
 const usage = await createAccountResourcesClient(schema, async () => Response.json({total_tasks:1, price_totals:[{unit:'vendor.credit',unit_label:'Vendor credits',amount:'0.5'}]})).getUsage({});
 expect(usage.price_totals?.[0].amount).toBe('0.5');
 expect(usage.estimated_credits).toBeUndefined();
 expect(usage.by_type).toBeUndefined();
 });
