import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  AssetListError,
  createAssetListClient,
  type AssetListDetail,
  type AssetListSummary,
} from './client';

const schema = defaultCatalog.application as unknown as ProductSchema;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const summary: AssetListSummary = {
  id: 'al_1',
  project_id: 'prj_1',
  source_session_id: 's1',
  title: 'Hero set',
  status: 'draft',
  effective_status: 'draft',
  item_count: 1,
  planned_step_count: 1,
  planned_model3d_step_count: 1,
  expires_at: '2026-11-25T00:00:00Z',
  created_at: '2026-11-18T00:00:00Z',
  updated_at: '2026-11-18T00:00:00Z',
};

const detail: AssetListDetail = {
  ...summary,
  version: 4,
  brief: { purpose: 'character' },
  items: [{
    id: 'it_1',
    kind: 'character',
    name: 'Hero',
    references: [{ task_id: 't1', asset_id: 'a1' }],
    route_id: 'text-to-3d',
    route: [{ capability: 'text-to-3d', offering_id: 'o1', available: true }],
    planned_step_count: 1,
    planned_model3d_step_count: 1,
    state: 'todo',
    version: 1,
    round: 0,
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
  }],
};

describe('asset list schema contract', () => {
  it('publishes the asset list section and the four route templates', () => {
    expect(schema.asset_list).toMatchObject({
      limits: {
        max_lists_per_project: 20,
        draft_ttl_seconds: 604800,
        name_max_length: 120,
        description_max_length: 1000,
        max_references_per_item: 4,
      },
    });
    expect(schema.asset_list?.kinds[0]).toMatchObject({
      id: 'character', label: '角色', project_kind: 'character', default_route: 'text-to-3d',
    });
    expect(schema.asset_list?.routes[0]).toMatchObject({
      id: 'text-to-3d', label: '文生 3D', steps: [{ capability: 'text-to-3d' }],
    });
    expect(schema.asset_list?.states.map((state) => state.id)).toEqual(['draft', 'confirmed', 'rejected', 'expired', 'superseded']);
    expect(schema.asset_list?.item_states.map((state) => state.id)).toEqual(
      ['todo', 'working', 'review', 'accepted', 'returned', 'failed', 'skipped'],
    );
    expect(schema.runtime.http.routes).toMatchObject({
      project_asset_lists: '/api/v1/projects/{projectID}/asset-lists',
      project_asset_list: '/api/v1/projects/{projectID}/asset-lists/{listID}',
      project_asset_list_decision: '/api/v1/projects/{projectID}/asset-lists/{listID}/decision',
      chat_session_asset_lists: '/api/v1/chat/sessions/{sessionID}/asset-lists',
      project_presence: '/api/v1/projects/{projectID}/presence',
    });
  });
});

describe('asset list client', () => {
  it('lists a project\'s lists from the Schema route and unwraps data', async () => {
    const fetcher = vi.fn(async () => jsonResponse({ data: [summary] })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.listProjectLists('prj 1')).resolves.toEqual([summary]);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%201/asset-lists');
  });

  it('reads one list as a bare detail object with encoded ids', async () => {
    const fetcher = vi.fn(async () => jsonResponse(detail)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.getList('prj/1', 'al 1')).resolves.toEqual(detail);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%2F1/asset-lists/al%201');
  });

  it('puts a decision with and without a reason and returns the detail', async () => {
    const fetcher = vi.fn(async () => jsonResponse(detail)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.decide('prj_1', 'al_1', 'confirm', 'ready')).resolves.toEqual(detail);
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects/prj_1/asset-lists/al_1/decision', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'confirm', reason: 'ready' }),
    });
    await client.decide('prj_1', 'al_1', 'reject');
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects/prj_1/asset-lists/al_1/decision', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'reject' }),
    });
  });

  it('lists a session\'s lists from the Schema route and unwraps data', async () => {
    const fetcher = vi.fn(async () => jsonResponse({ data: [summary] })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.listSessionLists('s 1')).resolves.toEqual([summary]);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/chat/sessions/s%201/asset-lists');
  });

  it('posts presence from the Schema route and treats 204 as success', async () => {
    const fetcher = vi.fn(async () => new Response(null, { status: 204 })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.presence('prj 1', 'tab 1', 'present')).resolves.toBeUndefined();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%201/presence', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tab_id: 'tab 1', state: 'present' }),
    });
  });

  it('lets a leave request outlive the page', async () => {
    const fetcher = vi.fn(async () => new Response(null, { status: 204 })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.presence('prj_1', 'tab_1', 'leave')).resolves.toBeUndefined();
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj_1/presence', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tab_id: 'tab_1', state: 'leave' }),
      keepalive: true,
    });
  });

  it('refuses presence when the Catalog has no route', async () => {
    const routes = { ...schema.runtime.http.routes, project_presence: undefined };
    const old = {
      ...schema,
      runtime: { ...schema.runtime, http: { ...schema.runtime.http, routes } },
    } as ProductSchema;
    const fetcher = vi.fn() as unknown as typeof fetch;
    await expect(createAssetListClient(old, { fetcher }).presence('p1', 't1', 'present')).rejects.toMatchObject({
      name: 'AssetListError',
      status: 0,
    });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('takes the route template from the Schema it was built with', async () => {
    const custom = {
      ...schema,
      runtime: {
        ...schema.runtime,
        http: {
          ...schema.runtime.http,
          routes: { ...schema.runtime.http.routes, project_asset_lists: '/custom/{projectID}/lists' },
        },
      },
    } as ProductSchema;
    const fetcher = vi.fn(async () => jsonResponse({ data: [] })) as unknown as typeof fetch;
    await createAssetListClient(custom, { fetcher }).listProjectLists('p1');
    expect(fetcher).toHaveBeenCalledWith('/custom/p1/lists');
  });

  it('refuses to build a URL when the Catalog has no asset list route', async () => {
    const routes = { ...schema.runtime.http.routes, project_asset_lists: undefined };
    const old = {
      ...schema,
      runtime: { ...schema.runtime, http: { ...schema.runtime.http, routes } },
    } as ProductSchema;
    const fetcher = vi.fn() as unknown as typeof fetch;
    await expect(createAssetListClient(old, { fetcher }).listProjectLists('p1')).rejects.toMatchObject({
      name: 'AssetListError',
      status: 0,
    });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('surfaces the server code and detail on a refusal', async () => {
    const fetcher = vi.fn(async () => jsonResponse({
      code: 'asset_list_expired', detail: '制作清单「Hero」已过期。', request_id: 'r1',
    }, 409)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.getList('prj_1', 'al_1')).rejects.toBeInstanceOf(AssetListError);
    await expect(client.getList('prj_1', 'al_1')).rejects.toMatchObject({
      status: 409,
      code: 'asset_list_expired',
      detail: '制作清单「Hero」已过期。',
      message: '制作清单「Hero」已过期。',
    });
  });

  it('carries the blocking list of a 409 confirm conflict', async () => {
    const fetcher = vi.fn(async () => jsonResponse({
      code: 'asset_list_active_conflict', detail: '项目已有确认的清单', active_list_id: 'al_9', active_list_title: '旧清单',
    }, 409)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.decide('prj_1', 'al_1', 'confirm')).rejects.toMatchObject({
      status: 409,
      code: 'asset_list_active_conflict',
      activeListId: 'al_9',
      activeListTitle: '旧清单',
    });
  });

  it('carries only the list id of a 409 working conflict', async () => {
    const fetcher = vi.fn(async () => jsonResponse({
      code: 'asset_list_active_working', detail: '清单仍有进行中的项', active_list_id: 'al_9',
    }, 409)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.decide('prj_1', 'al_1', 'replace')).rejects.toMatchObject({
      code: 'asset_list_active_working',
      activeListId: 'al_9',
      activeListTitle: undefined,
    });
  });

  it('keeps the status when the error body is not JSON', async () => {
    const fetcher = vi.fn(async () => new Response('bad gateway', { status: 502 })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.listProjectLists('prj_1')).rejects.toMatchObject({
      status: 502,
      code: '',
      message: schema.chat.presentation.messages.internal_error,
    });
  });

  it('refuses a success body that is not JSON', async () => {
    const fetcher = vi.fn(async () => new Response('not json', { status: 200 })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.getList('prj_1', 'al_1')).rejects.toBeInstanceOf(AssetListError);
  });

  it('lets a network failure through unchanged', async () => {
    const fetcher = vi.fn(async () => { throw new TypeError('Failed to fetch'); }) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.listSessionLists('s1')).rejects.toBeInstanceOf(TypeError);
  });
});