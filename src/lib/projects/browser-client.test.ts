import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createProjectBrowserClient, ProjectBrowserError } from './browser-client';

const schema = defaultCatalog.application as unknown as ProductSchema;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('project browser client', () => {
  it('lists and maps projects from the public route', async () => {
    const fetcher = vi.fn(async () => jsonResponse({
      projects: [{ id: 'prj_1', name: 'Star Harbor', created_at: 'c', updated_at: 'u' }],
    })) as unknown as typeof fetch;
    const client = createProjectBrowserClient(schema, { fetcher });
    await expect(client.listProjects()).resolves.toEqual([
      { id: 'prj_1', name: 'Star Harbor', createdAt: 'c', updatedAt: 'u' },
    ]);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects', { signal: undefined });
  });

  it('sends the project name on create and returns the server project', async () => {
    const fetcher = vi.fn(async () => jsonResponse({ id: 'prj_1', name: 'Series', created_at: 'c', updated_at: 'u' }, 201)) as unknown as typeof fetch;
    const client = createProjectBrowserClient(schema, { fetcher });
    await expect(client.createProject('Series')).resolves.toMatchObject({ id: 'prj_1', name: 'Series' });
    expect(fetcher).toHaveBeenCalledWith('/api/v1/projects', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ name: 'Series' }),
    }));
  });

  it('routes every project sub-resource by its encoded ids', async () => {
    const calls: string[] = [];
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input));
      return jsonResponse({ assets: [], references: [], palette: [], brief: {}, texts: {} });
    }) as unknown as typeof fetch;
    const client = createProjectBrowserClient(schema, { fetcher });
    await client.listAssets('prj 1');
    await client.getStyleGuide('prj/1');
    await client.getConventions('prj_1');
    await client.promoteAsset('prj_1', { source: { task_id: 't', asset_id: 'a' }, kind: 'character', name: 'Role A' });
    expect(calls).toEqual([
      '/api/v1/projects/prj%201/assets',
      '/api/v1/projects/prj%2F1/style-guide',
      '/api/v1/projects/prj_1/conventions',
      '/api/v1/projects/prj_1/promote',
    ]);
  });

  it('maps style guide, conventions and asset references without leaking private fields', async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes('assets')) {
        return jsonResponse({ assets: [{
          id: 'a1', project_id: 'prj_1', kind: 'character', name: 'Role A', description: 'red',
          images: [{ task_id: 't1', asset_id: 'i1' }, { input_asset_id: 'upload-1' }],
          models: [{ task_id: 't9', asset_id: 'm9' }], created_at: 'c', updated_at: 'u',
          owner_id: 'must-not-leak',
        }] });
      }
      if (String(input).includes('style-guide')) {
        return jsonResponse({ project_id: 'prj_1', enabled: true, text: 'warm', palette: ['#ffffff'],
          references: [{ input_asset_id: 'u1' }], updated_at: 'u', private_prompt: 'no' });
      }
      return jsonResponse({ project_id: 'prj_1', brief: { purpose: 'character' }, texts: { naming: 'hero_' }, updated_at: 'u' });
    }) as unknown as typeof fetch;
    const client = createProjectBrowserClient(schema, { fetcher });
    const assets = await client.listAssets('prj_1');
    expect(assets[0]).toEqual({
      id: 'a1', projectId: 'prj_1', kind: 'character', name: 'Role A', description: 'red',
      images: [{ task_id: 't1', asset_id: 'i1' }, { input_asset_id: 'upload-1' }],
      models: [{ task_id: 't9', asset_id: 'm9' }], createdAt: 'c', updatedAt: 'u',
    });
    await expect(client.getStyleGuide('prj_1')).resolves.toEqual({
      projectId: 'prj_1', enabled: true, text: 'warm', palette: ['#ffffff'],
      references: [{ input_asset_id: 'u1' }], updatedAt: 'u',
    });
    await expect(client.getConventions('prj_1')).resolves.toEqual({
      projectId: 'prj_1', brief: { purpose: 'character' }, texts: { naming: 'hero_' }, updatedAt: 'u',
    });
  });

  it('surfaces the server project copy with its status', async () => {
    const fetcher = vi.fn(async () => jsonResponse({ error: '项目不存在或不属于当前用户。' }, 404)) as unknown as typeof fetch;
    const client = createProjectBrowserClient(schema, { fetcher });
    await expect(client.getStyleGuide('prj_x')).rejects.toEqual(
      expect.objectContaining({ message: '项目不存在或不属于当前用户。', status: 404 }),
    );
    await expect(client.getStyleGuide('prj_x')).rejects.toBeInstanceOf(ProjectBrowserError);
  });
});