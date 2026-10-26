import { describe, expect, it } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { mergeLineagePages, parseLineage } from '@/lib/workspace/lineage';
import { buildVersionTiles } from '@/lib/workspace/versions';
import { versionTitle } from '@/lib/workspace/version-label';
import { detailSource } from '@/lib/workspace/task-detail';
import type { TrackedTask } from '@/store/workspace-store';

// #330 second follow-ups, observation 2: after a free refetch the strip still showed the failed glTF packaging and
// the failed Meshy Convert as two "3D 失败" tiles, and the refetch as 3D v2 previewing Meshy's re-exported GLB. A
// format conversion does not change the model: it is part of the version it converted (its files are on that
// version's Formats tab), so it has no tile and no number, and its detail names that version as its source.
const schema = {
  content: { messages: { 'ui.result.detail.made_from': '由 {parent} 生成', 'ui.result.detail.unknown_source': '来源未知', 'ui.studio.version.label_3d': '3D v{version}', 'ui.studio.version.conversion_caption': '格式转换' } },
  capabilities: [
    { id: 'text-to-3d', domain: 'model-3d' },
    { id: 'format-conversion', domain: 'processing' },
    { id: 'retexture', domain: 'processing' },
  ],
  runtime: { frontend: { model_processing_capabilities: ['format-conversion', 'retexture'], percentage_multiplier: 100 } },
  task_states: [
    { internal_state: 'RUNNING', public_state: 'IN_PROGRESS', outcome: 'running', terminal: false },
    { internal_state: 'SUCCEEDED', public_state: 'SUCCEEDED', outcome: 'success', terminal: true },
    { internal_state: 'FAILED', public_state: 'FAILED', outcome: 'failure', terminal: true },
  ],
} as unknown as ProductSchema;

const model = (id: string) => ({ id: `${id}-m`, role: 'model', media_type: 'model/gltf-binary', ordinal: 0, url: `/api/v1/tasks/${id}/assets/${id}-m` });
const node = (id: string, minute: number, extra: Record<string, unknown> = {}) => ({
  id, kind: 'task', task_id: id, capability: 'text-to-3d', created_at: `2026-01-01T00:0${minute}:00Z`, assets: [model(id)], version: 0, ...extra,
});
const tracked = (overrides: Partial<TrackedTask>): TrackedTask => ({
  taskId: 'x', sessionId: 's1', capability: 'format-conversion', prompt: '', state: 'FAILED', progress: 0, progressMaximum: 100,
  terminal: true, failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false,
  notFound: false, updatedAt: 0, ...overrides,
});

const page = parseLineage({
  nodes: [node('source', 1, { version: 1 }), node('retexture', 5, { capability: 'retexture', version: 2 })],
  edges: [{ from: 'source', to: 'retexture', kind: 'source_task', evidence: 'recorded' }],
  conversions: [
    { task_id: 'gltf-failed', source_task_id: 'source' },
    { task_id: 'convert-failed', source_task_id: 'source' },
    { task_id: 'convert-refetch', source_task_id: 'source' },
    { task_id: '', source_task_id: 'source' },
    { task_id: 'self', source_task_id: 'self' },
  ],
  truncated: false, next_cursor: '',
});

describe('format conversions fold into their source version (#330 second follow-ups)', () => {
  it('reads the folded conversions of a page and keeps them across pages', () => {
    expect(page.conversions).toEqual({ 'gltf-failed': 'source', 'convert-failed': 'source', 'convert-refetch': 'source' });
    const older = parseLineage({ nodes: [], edges: [], conversions: [{ task_id: 'old-convert', source_task_id: 'older' }], truncated: false, next_cursor: '' });
    expect(mergeLineagePages([page, older]).conversions).toEqual({ ...page.conversions, 'old-convert': 'older' });
    expect(parseLineage({ nodes: [], edges: [] }).conversions).toBeUndefined();
  });

  it('shows no tile for a conversion, failed or succeeded, and keeps the version numbers of the chain', () => {
    const versions = buildVersionTiles({
      schema, graph: page, sessionId: 's1',
      tracked: [tracked({ taskId: 'gltf-failed' }), tracked({ taskId: 'convert-failed' }), tracked({ taskId: 'convert-refetch', state: 'SUCCEEDED' })],
    });
    expect(versions.tiles.map((tile) => [tile.taskId, tile.version, tile.status])).toEqual([['source', 1, 'ready'], ['retexture', 2, 'ready']]);
    for (const id of ['gltf-failed', 'convert-failed', 'convert-refetch']) {
      expect(versions.byTask.has(id)).toBe(false);
      expect(versions.fileTaskParents.get(id)).toEqual([expect.objectContaining({ key: versions.byTask.get('source'), known: true })]);
      expect(detailSource(schema, id, versions).parentKey).toBe(versions.byTask.get('source'));
    }
  });

  it('folds a conversion started from the Formats tab before the chain lists it', () => {
    const versions = buildVersionTiles({
      schema, graph: parseLineage({ nodes: [node('source', 1, { version: 1 })], edges: [] }), sessionId: 's1',
      tracked: [tracked({ taskId: 'new-convert', state: 'RUNNING', terminal: false, formatOf: 'source' })],
    });
    expect(versions.tiles.map((tile) => tile.taskId)).toEqual(['source']);
    expect(versions.fileTaskParents.get('new-convert')?.[0]?.key).toBe(versions.byTask.get('source'));
    // A tracked task of another kind still shows as work in flight.
    const other = buildVersionTiles({
      schema, graph: parseLineage({ nodes: [node('source', 1, { version: 1 })], edges: [] }), sessionId: 's1',
      tracked: [tracked({ taskId: 'retexture-2', capability: 'retexture', state: 'RUNNING', terminal: false })],
    });
    expect(other.tiles.map((tile) => tile.taskId)).toEqual(['source', 'retexture-2']);
  });
});

// #330 review of !341: a conversion made before the fold keeps its node and number (the chat history already names
// the versions after it by number), and the strip says it is a format conversion.
describe('conversions made before the fold (#330 review)', () => {
  it('keeps their number and the numbers after them, captioned as a format conversion', () => {
    const graph = parseLineage({
      nodes: [node('gen-1', 1, { version: 1 }), node('convert-1', 2, { capability: 'format-conversion', version: 2, conversion: true }), node('gen-2', 3, { version: 3 })],
      edges: [{ from: 'gen-1', to: 'convert-1', kind: 'source_task', evidence: 'recorded' }],
    });
    const versions = buildVersionTiles({ schema, graph, sessionId: 's1', tracked: [] });
    expect(versions.tiles.map((tile) => [tile.taskId, tile.version])).toEqual([['gen-1', 1], ['convert-1', 2], ['gen-2', 3]]);
    const titles = versions.tiles.map((tile) => versionTitle(schema, tile, versions.tiles));
    expect(titles).toEqual(['3D v1', '3D v2 · 格式转换', '3D v3']);
  });
});
