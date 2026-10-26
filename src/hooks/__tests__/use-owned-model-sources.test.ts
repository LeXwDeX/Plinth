import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { AccountResources } from '@/lib/account-resources';
const runtime = vi.hoisted(() => ({ owner: 'u1', session: 's1', keys: [] as unknown[], requests: [] as unknown[], data: undefined as AccountResources | undefined }));
vi.mock('react', () => ({ useMemo: (factory: () => unknown) => factory(), useState: (initial: unknown) => [typeof initial === 'function' ? (initial as () => unknown)() : initial, vi.fn()] }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
vi.mock('swr', () => ({ default: (key: unknown, fetcher: () => unknown) => { runtime.keys.push(key); if (key) runtime.requests.push(fetcher()); return { data: runtime.data, isLoading: false, error: undefined, mutate: vi.fn() }; } }));
vi.mock('@/lib/account-resources', () => ({ createAccountResourcesClient: () => ({ getResources: (query: unknown) => query }) }));
import { ownedModelSourcesKey, useOwnedModelSources } from '../use-owned-model-sources';
beforeEach(() => { runtime.keys = []; runtime.requests = []; runtime.data = undefined; });
describe('owned model source list', () => {
  it('separates owner, session, Schema revision and page cache scopes', () => {
    const key = ownedModelSourcesKey('r1', 'u1', 's1', 1, '/assets');
    expect(key).not.toEqual(ownedModelSourcesKey('r1', 'u2', 's1', 1, '/assets'));
    expect(key).not.toEqual(ownedModelSourcesKey('r1', 'u1', 's2', 1, '/assets'));
    expect(key).not.toEqual(ownedModelSourcesKey('r2', 'u1', 's1', 1, '/assets'));
    expect(key).not.toEqual(ownedModelSourcesKey('r1', 'u1', 's1', 2, '/assets'));
  });
  it('reads only the existing owner asset API and filters by the Schema asset type', () => {
    const type = catalog.application.runtime.input_assets.model_profile?.asset_type ?? 'model';
    runtime.data = { tasks: [], next_cursor: '', uploads: { data: [{ id: 'a', asset_type: type }, { id: 'image', asset_type: 'image' }] as AccountResources['uploads']['data'], total: 2, page: 1, page_size: 24 } };
    const result = useOwnedModelSources('u1', 's1', true);
    expect(result.assets.map(asset => asset.id)).toEqual(['a']);
    expect(runtime.requests).toEqual([expect.objectContaining({ page: 1, limit: 1 })]);
  });
  it.each([['', 's1', true], ['u1', '', true], ['u1', 's1', false]] as const)('does not request uploads without an active owner/session/input mode', (owner, session, enabled) => {
    useOwnedModelSources(owner, session, enabled);
    expect(runtime.keys).toEqual([null]); expect(runtime.requests).toEqual([]);
  });
});
