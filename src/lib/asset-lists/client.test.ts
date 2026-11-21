import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { AssetListProductSchema, ProductSchema } from '@/lib/product-schema';
import {
  AssetListError,
  createAssetListClient,
  type AssetListDetail,
  type AssetListEditInput,
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
  // #331: the public limits carry the per-image decompose cap. The raw Catalog
  // declares the value under asset_list.decompose; the server's public
  // projection copies it into limits, which this type mirrors.
  it('types the public per-image decompose cap', () => {
    const limits: AssetListProductSchema['limits'] = {
      max_lists_per_project: 20,
      draft_ttl_seconds: 604800,
      name_max_length: 120,
      description_max_length: 1000,
      max_references_per_item: 4,
      maximum_items_per_image: 24,
    };
    expect(limits.maximum_items_per_image).toBe(24);
  });

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
      project_asset_list_board: '/api/v1/projects/{projectID}/asset-lists/{listID}/board',
      project_asset_list_run: '/api/v1/projects/{projectID}/asset-lists/{listID}/run',
      project_asset_list_item: '/api/v1/projects/{projectID}/asset-lists/{listID}/items/{itemID}',
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

  it('reads the acceptance board from its own route with encoded ids', async () => {
    const board = { list: { ...summary, version: 3 }, items: [{ ...detail.items[0], result: {}, review: {}, latest_review: null }] };
    const fetcher = vi.fn(async () => jsonResponse(board)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.getBoard('prj/1', 'al 1')).resolves.toEqual(board);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%2F1/asset-lists/al%201/board');
  });

  // #329d: one accept of many items on the review route, gated by the list
  // version the board read. Reasons and the note travel only when present.
  it('puts a batch review with the expected version and unwraps the outcome', async () => {
    const outcome = {
      list: { ...detail, version: 5 },
      items: [{ item_id: 'it_1', verdict: 'approved', project_asset_id: 'pa_1' }],
    };
    const fetcher = vi.fn(async () => jsonResponse(outcome)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.review('prj 1', 'al 1', {
      decision: 'accept', item_ids: ['it_1', 'it_2'], expected_version: 4,
    })).resolves.toEqual(outcome);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%201/asset-lists/al%201/review', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'accept', item_ids: ['it_1', 'it_2'], expected_version: 4 }),
    });

    await client.review('prj_1', 'al_1', {
      decision: 'return', item_ids: ['it_2'], reason_ids: ['style', 'details'], note: '偏冷', expected_version: 5,
    });
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects/prj_1/asset-lists/al_1/review', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        decision: 'return', item_ids: ['it_2'], reason_ids: ['style', 'details'], note: '偏冷', expected_version: 5,
      }),
    });
  });

  it('downloads the batch archive and reads the server filename', async () => {
    const fetcher = vi.fn(async () => new Response('zip-bytes', {
      status: 200,
      headers: { 'Content-Disposition': 'attachment; filename="asset-list-al_1.zip"' },
    })) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    const download = await client.downloadArchive('prj 1', 'al 1', ['it 1', 'it_2']);
    await expect(download.blob.text()).resolves.toBe('zip-bytes');
    expect(download.filename).toBe('asset-list-al_1.zip');
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/projects/prj%201/asset-lists/al%201/archive?item_ids=it%201&item_ids=it_2',
    );
  });

  // #329d: the too-large refusal carries the server's own copy so the board can
  // offer a narrowed retry instead of a generic failure.
  it('surfaces a 413 archive refusal with its detail', async () => {
    const fetcher = vi.fn(async () => jsonResponse({
      code: 'asset_list_archive_too_large',
      detail: '已通过项超出整批下载上限；请用 item_ids 分批下载。',
    }, 413)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.downloadArchive('prj_1', 'al_1')).rejects.toMatchObject({
      name: 'AssetListError',
      status: 413,
      code: 'asset_list_archive_too_large',
      detail: '已通过项超出整批下载上限；请用 item_ids 分批下载。',
    });
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

  it('posts a run control with the opaque run version string and returns the run', async () => {
    const run = {
      status: 'running',
      pause_reason: '',
      version: '2026-11-18T01:00:00.123456789Z',
      item_counts: { todo: 1, working: 1, review: 0, accepted: 0, returned: 0, failed: 0, skipped: 0 },
    };
    const fetcher = vi.fn(async () => jsonResponse(run)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.controlRun('prj 1', 'al/1', 'start', run.version)).resolves.toEqual(run);
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects/prj%201/asset-lists/al%2F1/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'start', expected_version: '2026-11-18T01:00:00.123456789Z' }),
    });
    await client.controlRun('prj_1', 'al_1', 'pause', '2026-11-18T01:00:01Z', 'manual');
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects/prj_1/asset-lists/al_1/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'pause', expected_version: '2026-11-18T01:00:01Z', reason: 'manual' }),
    });
  });

  it('puts a skip with the item version and returns the item', async () => {
    const skipped = { ...detail.items[0], state: 'skipped', version: 8 };
    const fetcher = vi.fn(async () => jsonResponse(skipped)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.skipItem('prj 1', 'al 1', 'it/1', 7)).resolves.toEqual(skipped);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%201/asset-lists/al%201/items/it%2F1', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'skip', expected_version: 7 }),
    });
    await client.skipItem('prj_1', 'al_1', 'it_1', 8, 'not needed');
    expect(fetcher).toHaveBeenLastCalledWith('/api/v1/projects/prj_1/asset-lists/al_1/items/it_1', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'skip', expected_version: 8, reason: 'not needed' }),
    });
  });

  it('shows the server detail of a refused run control', async () => {
    const fetcher = vi.fn(async () => jsonResponse({
      code: 'asset_list_version_conflict', detail: '制作清单已被更新，请重试。',
    }, 409)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    await expect(client.controlRun('prj_1', 'al_1', 'stop', '2026-11-18T01:00:02Z')).rejects.toMatchObject({
      name: 'AssetListError',
      status: 409,
      code: 'asset_list_version_conflict',
      detail: '制作清单已被更新，请重试。',
    });
    await expect(client.skipItem('prj_1', 'al_1', 'it_1', 2)).rejects.toMatchObject({ status: 409 });
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

  // #331: an edit is one whole-list replacement on the detail route, with the
  // list version as the CAS token and each item's optional crop instruction.
  it('puts a whole-list edit and returns the stored detail', async () => {
    const updated = { ...detail, version: 5, title: 'Hero set' };
    const fetcher = vi.fn(async () => jsonResponse(updated)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    const input: AssetListEditInput = {
      title: 'Hero set',
      expected_version: 4,
      brief: { purpose: 'character' },
      items: [
        {
          kind: 'character',
          name: 'Hero renamed',
          description: 'one crop',
          route_id: 'text-to-3d',
          references: [{ input_asset_id: 'crop_2' }],
          crop: {
            source_input_asset_id: 'scene_1',
            region: { x: 0.1, y: 0.2, width: 0.3, height: 0.4 },
          },
        },
        {
          kind: 'weapon',
          name: 'Sword',
          references: [],
        },
      ],
    };

    await expect(client.edit('prj 1', 'al 1', input)).resolves.toEqual(updated);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects/prj%201/asset-lists/al%201', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
  });

  it.each([
    ['asset_list_region_invalid', 400],
    ['asset_list_region_too_small', 400],
    ['asset_list_reference_invalid', 400],
    ['asset_list_image_item_limit', 400],
    ['conflict', 409],
  ])('passes the %s refusal of an edit through', async (code: string, status: number) => {
    const fetcher = vi.fn(async () => jsonResponse({ code, detail: `refused: ${code}` }, status)) as unknown as typeof fetch;
    const client = createAssetListClient(schema, { fetcher });
    const edit = client.edit('prj_1', 'al_1', {
      title: 'Hero set', expected_version: 4, items: [],
    });
    await expect(edit).rejects.toBeInstanceOf(AssetListError);
    await expect(edit).rejects.toMatchObject({
      status,
      code,
      detail: `refused: ${code}`,
      message: `refused: ${code}`,
    });
  });
});