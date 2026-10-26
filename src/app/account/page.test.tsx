import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import AccountPage from './page';
import { createAccountResourcesClient } from '@/lib/account-resources';
import type { ProductSchema } from '@/lib/product-schema';
const state = vi.hoisted(() => ({ usage: undefined as unknown }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('swr', () => ({ default: () => ({ data: state.usage, isLoading: false }) }));
vi.mock('swr/infinite', () => ({ default: () => ({ isLoading: false }) }));
vi.mock('@/components/result/result-detail', () => ({ ResultDetailHost: () => null, ResultDetailDialog: () => null }));
describe('account usage price modes', () => {
  it.each([
    { price_totals: [{ unit: 'vendor.credit', unit_label: 'Vendor credits', amount: '0.5' }], expected: '0.5 Vendor credits' },
    { price_totals: null, estimated_credits: 999, expected: '报价未知' },
    { estimated_credits: 0, by_type: { legacy: 0 }, expected: 'legacy: 0' },
  ])('renders actual AccountPage with valid native, malformed native or legacy usage', async ({ expected, ...raw }) => {
    state.usage = await createAccountResourcesClient(defaultCatalog.application as unknown as ProductSchema,
      async () => Response.json({ user_id: 'owner', total_tasks: 1, succeeded_tasks: 1, failed_tasks: 0, ...raw })).getUsage({});
    const html = renderToStaticMarkup(<AccountPage />);
    expect(html).toContain(expected);
    expect(html).not.toContain('undefined');
  });
});
