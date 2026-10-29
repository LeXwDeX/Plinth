import { describe, expect, it } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { mergeLineagePages, parseLineage } from '@/lib/workspace/lineage';
import {
  branchOf,
  buildVersionTiles,
  domainOfCapability,
  foldColumn,
  isOlderVersion,
  layoutColumns,
  primaryParent,
  versionStatusLabel,
} from '@/lib/workspace/versions';
import { parseThreeViews } from '@/lib/workspace/three-view';
import type { TrackedTask } from '@/store/workspace-store';

const schema = {
  content: { messages: { 'ui.studio.merge.label': '手绘', 'failure.insufficient_balance': '余额不足，换个模型' } },
  capabilities: [
    { id: 'text-to-image', domain: 'image' },
    { id: 'text-to-3d', domain: 'model-3d' },
    { id: 'rigging', domain: 'processing' },
    { id: 'background-removal', domain: 'processing' },
  ],
  runtime: { frontend: { model_processing_capabilities: ['rigging'] } },
  task_states: [
    { internal_state: 'QUEUED', public_state: 'PENDING', outcome: 'pending', terminal: false },
    { internal_state: 'RUNNING', public_state: 'IN_PROGRESS', outcome: 'running', terminal: false },
    { internal_state: 'FAILED', public_state: 'FAILED', outcome: 'failure', terminal: true },
    { internal_state: 'CANCELED', public_state: 'CANCELED', outcome: 'canceled', terminal: true },
  ],
} as unknown as ProductSchema;

const asset = (id: string, role: string, mediaType: string, ordinal = 0) => ({
  id, role, media_type: mediaType, ordinal, url: `/api/v1/tasks/t/assets/${id}`,
});
const task = (id: string, minute: number, assets: unknown[], extra: Record<string, unknown> = {}) => ({
  id, kind: 'task', task_id: id, created_at: `2026-01-01T00:${String(minute).padStart(2, '0')}:00Z`, assets, ...extra,
});
const edge = (from: string, to: string, kind: string, evidence: string) => ({ from, to, kind, evidence });
const image = (id: string) => asset(`${id}-a`, 'image', 'image/png');
const model = (id: string) => asset(`${id}-m`, 'model', 'model/gltf-binary');

const tracked = (overrides: Partial<TrackedTask>): TrackedTask => ({
  taskId: 'x', sessionId: 's1', capability: 'text-to-3d', prompt: '', state: '', progress: 0, progressMaximum: 100,
  terminal: false, failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false,
  notFound: false, updatedAt: 0, ...overrides,
});

const graph = parseLineage({
  nodes: [
    task('img1', 1, [image('img1'), asset('img1-b', 'image', 'image/png', 1)]),
    task('mdl1', 2, [model('mdl1'), asset('mdl1-t', 'thumbnail', 'image/png', 1)]),
    task('img2', 3, [image('img2')]),
    task('img3', 4, [image('img3')]),
    { id: 'unknown-1', kind: 'unknown' },
  ],
  edges: [
    edge('img1', 'mdl1', 'reference', 'recorded'),
    edge('mdl1', 'img2', 'capture', 'recorded'),
    edge('img1', 'img3', 'source_task', 'recorded'),
    edge('unknown-1', 'img1', 'reference', 'unknown'),
  ],
});

describe('version tiles', () => {
  it('keeps the declared Gaussian Splat identity as a model-domain lineage asset', () => {
    const splatGraph = parseLineage({ nodes: [task('splat-1', 8, [{
      id: 'splat-asset', role: 'gaussian-splat', variant: 'splat', delivery_profile: 'gaussian-splat-32-v1',
      media_type: 'application/vnd.art-ai.gaussian-splat', ordinal: 0, url: '/api/splat',
    }], { capability: 'image-to-splat' })], edges: [] });
    const { tiles } = buildVersionTiles({ schema: { ...schema, capabilities: [...schema.capabilities, { id: 'image-to-splat', domain: 'model-3d' }] } as ProductSchema, graph: splatGraph, tracked: [], sessionId: 's1' });
    expect(tiles).toHaveLength(1);
    expect(tiles[0]).toMatchObject({ domain: 'model', assets: [{ kind: 'splat', deliveryProfile: 'gaussian-splat-32-v1' }] });
  });

  it('labels each domain by order and keeps a candidate group as one tile', () => {
    const { tiles } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
    expect(tiles.map((tile) => `${tile.domain}${tile.version}`)).toEqual(['image1', 'model1', 'image2', 'image3']);
    expect(tiles[0].assets).toHaveLength(2);
    expect(tiles[1].thumbnailUrl).toContain('mdl1-t');
    expect(tiles[1].assets.map((entry) => entry.kind)).toEqual(['model']);
  });

  it('uses the thumbnail role for a 3D tile, never a texture map, and none when the provider sent no preview', () => {
    const textured = parseLineage({ nodes: [
      task('m1', 1, [model('m1'), asset('m1-tex', 'texture', 'image/png', 0), asset('m1-th', 'thumbnail', 'image/png', 1)]),
      task('m2', 2, [model('m2'), asset('m2-tex', 'texture', 'image/png', 0)]),
    ], edges: [] });
    const { tiles } = buildVersionTiles({ schema, graph: textured, tracked: [], sessionId: 's1' });
    expect(tiles[0].thumbnailUrl).toContain('m1-th');
    expect(tiles[1].thumbnailUrl).toBe('');
  });

  it('classifies connectors and leaves unknown sources dashed', () => {
    const { tiles } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
    const byTask = Object.fromEntries(tiles.map((tile) => [tile.taskId, tile]));
    expect(primaryParent(byTask.mdl1)).toMatchObject({ known: true, link: 'to_model', key: byTask.img1.key });
    expect(primaryParent(byTask.img2)).toMatchObject({ known: true, link: 'capture', key: byTask.mdl1.key });
    expect(primaryParent(byTask.img3)).toMatchObject({ known: true, link: 'edit' });
    expect(primaryParent(byTask.img1)).toMatchObject({ known: false, link: 'unknown', key: '' });
  });

  it('collapses a three-view group into one version represented by the source slot', () => {
    const groupGraph = parseLineage({
      nodes: [
        task('front', 1, [image('front')], { group: 'run1', slot: 'front' }),
        task('side', 2, [image('side')], { group: 'run1', slot: 'side' }),
        task('other', 3, [image('other')]),
      ],
      edges: [],
      groups: [{
        id: 'run1', kind: 'three_view', label: '三视图', complete: false, source_slot: 'front', created_at: '2026-01-01T00:01:00Z',
        members: [{ slot: 'front', label: '正', task_id: 'front' }, { slot: 'side', label: '侧', task_id: 'side' }],
      }],
    });
    const { tiles, byTask } = buildVersionTiles({ schema, graph: groupGraph, tracked: [], sessionId: 's1' });
    expect(tiles.map((tile) => `${tile.key}:${tile.version}`)).toEqual(['g:run1:1', 'n:other:2']);
    expect(tiles[0]).toMatchObject({ taskId: 'front', groupLabel: '三视图', groupComplete: false });
    expect(tiles[0].slots.map((slot) => slot.slot)).toEqual(['front', 'side']);
    expect(byTask.get('side')).toBe('g:run1');
  });

  it('adds pending, running and failed tasks that the version chain does not know yet', () => {
    const { tiles } = buildVersionTiles({
      schema, graph, sessionId: 's1',
      tracked: [
        tracked({ taskId: 'queued', capability: 'text-to-image', state: 'QUEUED' }),
        tracked({ taskId: 'live', capability: 'rigging', state: 'RUNNING', progress: 45 }),
        tracked({ taskId: 'bad', capability: 'text-to-3d', state: 'FAILED', terminal: true, liveError: 'boom' }),
        tracked({ taskId: 'gone', capability: 'text-to-3d', state: 'CANCELED', terminal: true }),
        tracked({ taskId: 'img1' }),
        tracked({ taskId: 'foreign', sessionId: 's2' }),
      ],
    });
    const added = tiles.slice(4);
    expect(added.map((tile) => `${tile.taskId}:${tile.status}:${tile.domain}:${tile.version}`)).toEqual([
      'queued:pending:image:0', 'live:running:model:0', 'bad:failed:model:0',
    ]);
    expect(added[1].progress).toBeCloseTo(0.45);
    expect(added[2].failure).toBe('boom');
  });

  it('knows which capability makes which kind of result', () => {
    expect(domainOfCapability(schema, 'rigging')).toBe('model');
    expect(domainOfCapability(schema, 'background-removal')).toBe('image');
    expect(domainOfCapability(schema, 'missing')).toBeNull();
  });
});

describe('paged versions', () => {
  it('resolves a pending cross-page parent and never renumbers loaded tiles', () => {
    const newest = parseLineage({ nodes: [task('new', 3, [image('new')], { version: 3 })],
      edges: [edge('old', 'new', 'source_task', 'recorded')], next_cursor: 'older', truncated: true });
    const older = parseLineage({ nodes: [task('middle', 2, [image('middle')], { version: 2 }), task('old', 1, [image('old')], { version: 1 })],
      edges: [], next_cursor: '', truncated: false });
    const first = buildVersionTiles({ schema, graph: newest, tracked: [], sessionId: 's1' }).tiles[0];
    expect(first.version).toBe(3);
    expect(primaryParent(first)?.known).toBe(false);
    const merged = mergeLineagePages([newest, older, older]);
    expect(merged.nodes).toHaveLength(3);
    expect(merged.truncated).toBe(false);
    const after = buildVersionTiles({ schema, graph: merged, tracked: [], sessionId: 's1' }).tiles;
    expect(after.map((tile) => tile.version)).toEqual([1, 2, 3]);
    expect(primaryParent(after[2])).toMatchObject({ known: true, key: 'n:old' });
  });

  it('shows failed frozen-source attempts and retries without changing successful numbers, even after refresh', () => {
    const withAttempts = parseLineage({
      nodes: [task('source', 1, [model('source')], { version: 1, state: 'SUCCEEDED', capability: 'text-to-3d' }),
        task('failed', 2, [], { state: 'FAILED', capability: 'rigging', failure: { class: 'upstream_rejected', detail: '失败' } }),
        task('retry', 3, [], { state: 'RUNNING', capability: 'rigging', progress: 45 }),
        task('unknown', 4, [], { state: 'QUEUED', capability: 'rigging' })],
      edges: [edge('source', 'failed', 'source_task', 'recorded'), edge('source', 'retry', 'source_asset', 'recorded')], next_cursor: '',
    });
    const { tiles } = buildVersionTiles({ schema: { ...schema, task_states: [...schema.task_states, { internal_state: 'SUCCEEDED', public_state: 'SUCCEEDED', outcome: 'success', terminal: true, label: '完成' }] } as ProductSchema,
      graph: withAttempts, tracked: [], sessionId: 's1' });
    expect(tiles.map((tile) => [tile.taskId, tile.version, tile.status])).toEqual([['source', 1, 'ready'], ['failed', 0, 'failed'], ['retry', 0, 'running'], ['unknown', 0, 'pending']]);
    expect(primaryParent(tiles[1])).toMatchObject({ known: true, key: 'n:source', link: 'process', step: 'rigging' });
    expect(primaryParent(tiles[2])).toMatchObject({ known: true, key: 'n:source' });
    expect(primaryParent(tiles[3])).toMatchObject({ known: false });
    expect(layoutColumns(tiles)[1].tiles.map((tile) => tile.taskId)).toEqual(['failed', 'retry']);
    const custom = { ...schema, task_states: schema.task_states.map((state) => ({ ...state, label: `schema:${state.internal_state}` })) };
    expect(versionStatusLabel(custom, tiles[1])).toBe('schema:FAILED');
    expect(versionStatusLabel(custom, tiles[2])).toBe('schema:RUNNING');
  });
});

describe('branches', () => {
  it('stacks results of the same source in one column and highlights the current branch', () => {
    const { tiles } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
    const keys = Object.fromEntries(tiles.map((tile) => [tile.taskId, tile.key]));
    const columns = layoutColumns(tiles);
    // img1 is a root; mdl1 and img3 both come from img1, so they share a column.
    expect(columns.map((column) => column.tiles.map((tile) => tile.taskId))).toEqual([['img1'], ['mdl1', 'img3'], ['img2']]);
    const branch = branchOf(tiles, keys.img2);
    expect([...branch].sort()).toEqual([keys.img1, keys.mdl1, keys.img2].sort());
    expect(branch.has(keys.img3)).toBe(false);
  });
});

describe('folding a column of siblings', () => {
  const { tiles: all } = buildVersionTiles({ schema, graph: parseLineage({
    nodes: [
      task('root', 1, [image('root')]),
      task('b1', 2, [image('b1')]), task('b2', 3, [image('b2')]), task('b3', 4, [image('b3')]),
      task('b4', 5, [image('b4')]), task('b5', 6, [image('b5')]),
    ],
    edges: ['b1', 'b2', 'b3', 'b4', 'b5'].map((id) => edge('root', id, 'source_task', 'recorded')),
  }), tracked: [], sessionId: 's1' });
  const keyOf = (taskId: string) => all.find((tile) => tile.taskId === taskId)!.key;
  const siblings = all.filter((tile) => tile.taskId !== 'root');

  it('keeps up to three siblings as they are', () => {
    expect(foldColumn(siblings.slice(0, 3), new Set(), null).hidden).toHaveLength(0);
  });

  it('shows two and folds the rest into +N when there are more than three', () => {
    const folded = foldColumn(siblings, new Set(), null);
    expect(folded.visible.map((tile) => tile.taskId)).toEqual(['b1', 'b2']);
    expect(folded.hidden.map((tile) => tile.taskId)).toEqual(['b3', 'b4', 'b5']);
  });

  it('never folds the current version or its branch away', () => {
    const current = keyOf('b4');
    const branch = branchOf(all, current);
    const folded = foldColumn(siblings, branch, current);
    expect(folded.visible.map((tile) => tile.taskId)).toContain('b4');
    expect(folded.visible.map((tile) => tile.taskId)).toEqual(['b1', 'b4']);
    expect(folded.hidden).toHaveLength(3);
  });

  it('keeps column order among the visible tiles', () => {
    const current = keyOf('b5');
    const folded = foldColumn(siblings, branchOf(all, current), current);
    expect(folded.visible.map((tile) => tile.taskId)).toEqual(['b1', 'b5']);
  });
});

describe('older versions', () => {
  const { tiles } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
  const keyOf = (taskId: string) => tiles.find((tile) => tile.taskId === taskId)!.key;

  it('calls a version older when a newer one of its kind exists', () => {
    expect(isOlderVersion(tiles, keyOf('img1'))).toBe(true);
    expect(isOlderVersion(tiles, keyOf('img2'))).toBe(true);
    expect(isOlderVersion(tiles, keyOf('img3'))).toBe(false);
    expect(isOlderVersion(tiles, keyOf('mdl1'))).toBe(false);
    expect(isOlderVersion(tiles, null)).toBe(false);
    expect(isOlderVersion(tiles, 'missing')).toBe(false);
  });

  it('does not count a failed attempt as newer', () => {
    const withFailure = buildVersionTiles({
      schema, graph, sessionId: 's1',
      tracked: [tracked({ taskId: 'bad', capability: 'text-to-image', state: 'FAILED', terminal: true, failure: { detail: 'x' } as TrackedTask['failure'] })],
    }).tiles;
    expect(isOlderVersion(withFailure, withFailure.find((tile) => tile.taskId === 'img3')!.key)).toBe(false);
  });
});

describe('three-view groups', () => {
  const group = (slots: Array<[string, string, string]>) => parseThreeViews({ items: [{
    run_id: 'run1', label: '三视图', source_asset_id: 'img1-a', created_at: '2026-01-01T00:09:00Z', complete: false,
    slots: slots.map(([key, status, taskId]) => ({ key, label: key, status, attempt: 1, task_id: taskId, startable: false, retryable: false })),
    model_source: { slot: 'front', available: false, note: 'front only' },
  }] });

  it('adds one tile for a group none of whose views has finished, linked to its source picture', () => {
    const { tiles, byTask } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1', threeViews: group([['front', 'running', 'tv1'], ['side', 'not_started', ''], ['back', 'not_started', '']]) });
    const tile = tiles.find((item) => item.key === 'g:run1')!;
    expect(tile).toMatchObject({ domain: 'image', status: 'running', groupLabel: '三视图', taskId: 'tv1' });
    expect(tile.views.map((view) => [view.key, view.status])).toEqual([['front', 'running'], ['side', 'not_started'], ['back', 'not_started']]);
    expect(tile.parents[0]).toMatchObject({ known: true, key: tiles.find((item) => item.taskId === 'img1')!.key });
    expect(byTask.get('tv1')).toBe('g:run1');
  });

  it('does not give a running view its own second tile from the task tracker', () => {
    const { tiles } = buildVersionTiles({
      schema, graph, sessionId: 's1',
      tracked: [tracked({ taskId: 'tv1', capability: 'text-to-image', state: 'RUNNING' })],
      threeViews: group([['front', 'running', 'tv1'], ['side', 'not_started', ''], ['back', 'not_started', '']]),
    });
    expect(tiles.filter((tile) => tile.taskIds.includes('tv1'))).toHaveLength(1);
  });

  it('takes the views and completeness onto the chain tile once a view has a picture', () => {
    const lineage = parseLineage({
      nodes: [task('tv1', 9, [image('tv1')], { group: 'run1', slot: 'front' })],
      edges: [],
      groups: [{ id: 'run1', kind: 'three_view', label: '三视图', complete: false, source_slot: 'front', created_at: '2026-01-01T00:09:00Z', members: [{ slot: 'front', label: 'front', task_id: 'tv1' }] }],
    });
    const { tiles } = buildVersionTiles({ schema, graph: lineage, tracked: [], sessionId: 's1', threeViews: group([['front', 'succeeded', 'tv1'], ['side', 'failed', 'tv2'], ['back', 'not_started', '']]) });
    expect(tiles.filter((tile) => tile.key === 'g:run1')).toHaveLength(1);
    const tile = tiles.find((item) => item.key === 'g:run1')!;
    expect(tile.status).toBe('ready');
    expect(tile.views.map((view) => view.status)).toEqual(['succeeded', 'failed', 'not_started']);
  });
});

describe('merged versions', () => {
  const merged = [
    { nodeId: 'm1', url: '/api/v1/assets/m1', from: 'img3', at: '2026-01-01T01:00:00Z' },
    { nodeId: 'm2', url: '/api/v1/assets/m2', from: 'canvas:merged:m1', at: '2026-01-01T02:00:00Z' },
  ];

  it('makes each merge a numbered tile linked to the version it came from, merges chained', () => {
    const { tiles, byTask } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1', merged });
    const first = tiles.find((tile) => tile.key === 'm:m1')!;
    const second = tiles.find((tile) => tile.key === 'm:m2')!;
    expect(first).toMatchObject({ domain: 'image', status: 'ready', taskId: 'canvas:merged:m1', version: 1, groupLabel: '手绘' });
    expect(second.version).toBe(2);
    expect(first.assets[0]).toMatchObject({ url: '/api/v1/assets/m1', kind: 'image' });
    expect(first.parents[0]).toMatchObject({ known: true, key: byTask.get('img3'), link: 'merge' });
    expect(second.parents[0]).toMatchObject({ known: true, key: 'm:m1' });
    expect(byTask.get('canvas:merged:m2')).toBe('m:m2');
  });

  it('keeps an unreachable source as unknown instead of guessing', () => {
    const { tiles } = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1', merged: [{ nodeId: 'm9', url: '/x', from: 'gone', at: '' }] });
    expect(tiles.find((tile) => tile.key === 'm:m9')!.parents[0]).toMatchObject({ known: false });
  });
});

describe('balance failures in the strip', () => {
  it('shows the detail the server projected from the Schema (failure.<class>), with no client-side special case', () => {
    const { tiles } = buildVersionTiles({
      schema, graph: undefined, sessionId: 's1',
      tracked: [tracked({
        taskId: 'tb', capability: 'text-to-image', state: 'FAILED', terminal: true,
        failure: { failureClass: 'insufficient_balance', detail: '余额不足，换个模型', retryable: false, ambiguous: false },
      })],
    });
    expect(tiles[0]).toMatchObject({ status: 'failed', failure: '余额不足，换个模型' });
  });
});

describe('screenshots, uploads and the link between versions', () => {
  const shot = {
    id: 'capture:c1', kind: 'capture', capture_id: 'c1', created_at: '2026-01-01T00:05:00Z',
    pixel_width: 1280, pixel_height: 720,
    assets: [{ id: 'shot-a', role: 'screenshot', media_type: 'image/png', ordinal: 0, url: '/api/v1/assets/shot-a' }],
  };
  const upload = {
    id: 'upload:u1', kind: 'upload', created_at: '2026-01-01T00:00:00Z',
    assets: [{ id: 'u1', role: 'input', media_type: 'image/png', ordinal: 0, url: '/api/v1/assets/u1' }],
  };

  it('gives a screenshot its own numbered 2D version that names the model it was taken from', () => {
    const lineage = parseLineage({
      nodes: [task('mdl1', 2, [model('mdl1')]), shot, task('img9', 6, [image('img9')])],
      edges: [edge('mdl1', 'capture:c1', 'capture', 'recorded'), edge('capture:c1', 'img9', 'reference', 'recorded')],
    });
    const { tiles, byCapture } = buildVersionTiles({ schema, graph: lineage, tracked: [], sessionId: 's1' });
    const capture = tiles.find((tile) => tile.origin === 'capture')!;
    expect(capture).toMatchObject({ domain: 'image', version: 1, captureId: 'c1', taskId: 'mdl1', size: { width: 1280, height: 720 } });
    expect(capture.parents[0]).toMatchObject({ known: true, link: 'capture' });
    expect(byCapture.get('c1')).toBe(capture.key);
    // the picture made from the screenshot comes after it and is linked to it
    const child = tiles.find((tile) => tile.taskId === 'img9')!;
    expect(child.version).toBe(2);
    expect(child.parents[0]).toMatchObject({ known: true, key: capture.key });
  });

  it('shows an uploaded reference as a version of its own and links the results made from it', () => {
    const lineage = parseLineage({
      nodes: [upload, task('img1', 3, [image('img1')])],
      edges: [edge('upload:u1', 'img1', 'reference', 'recorded')],
    });
    const { tiles } = buildVersionTiles({ schema, graph: lineage, tracked: [], sessionId: 's1' });
    const uploaded = tiles.find((tile) => tile.origin === 'upload')!;
    const child = tiles.find((tile) => tile.taskId === 'img1')!;
    expect(uploaded.domain).toBe('image');
    expect(child.parents[0]).toMatchObject({ known: true, key: uploaded.key, link: 'reference' });
    // an upload is not a step of the line: results are numbered from 1
    expect(child.version).toBe(1);
  });

  it('does not make a version older just because a picture was uploaded after it', () => {
    const lineage = parseLineage({
      nodes: [task('img1', 1, [image('img1')]), { ...upload, created_at: '2026-01-01T00:09:00Z' }],
      edges: [],
    });
    const { tiles } = buildVersionTiles({ schema, graph: lineage, tracked: [], sessionId: 's1' });
    expect(isOlderVersion(tiles, tiles.find((tile) => tile.taskId === 'img1')!.key)).toBe(false);
  });

  it('names a masked repaint, a cut-out and a processing step on the link', () => {
    const lineage = parseLineage({
      nodes: [
        task('img1', 1, [image('img1')]),
        task('img2', 2, [image('img2')], { operation: 'inpaint', capability: 'image-to-image' }),
        task('img3', 3, [image('img3')], { capability: 'background-removal' }),
        task('mdl1', 4, [model('mdl1')]),
        task('mdl2', 5, [model('mdl2')], { capability: 'rigging' }),
      ],
      edges: [
        edge('img1', 'img2', 'source_task', 'recorded'),
        edge('img2', 'img3', 'source_task', 'recorded'),
        edge('img1', 'mdl1', 'reference', 'recorded'),
        edge('mdl1', 'mdl2', 'source_task', 'recorded'),
      ],
    });
    const { tiles } = buildVersionTiles({ schema, graph: lineage, tracked: [], sessionId: 's1' });
    const byTask = Object.fromEntries(tiles.map((tile) => [tile.taskId, tile]));
    expect(primaryParent(byTask.img2)).toMatchObject({ link: 'inpaint' });
    expect(primaryParent(byTask.img3)).toMatchObject({ link: 'remove_background' });
    expect(primaryParent(byTask.mdl2)).toMatchObject({ link: 'process', step: 'rigging' });
  });

  it('gives the shared connector of a column one link for every result of the source', () => {
    const lineage = parseLineage({
      nodes: [
        task('img1', 1, [image('img1')]),
        task('img2', 2, [image('img2')]),
        task('img3', 3, [image('img3')], { operation: 'inpaint' }),
      ],
      edges: [edge('img1', 'img2', 'source_task', 'recorded'), edge('img1', 'img3', 'source_task', 'recorded')],
    });
    const { tiles } = buildVersionTiles({ schema, graph: lineage, tracked: [], sessionId: 's1' });
    const columns = layoutColumns(tiles);
    const shared = columns.find((column) => column.tiles.length === 2)!;
    expect(shared.links.map((link) => link.link)).toEqual(['edit', 'inpaint']);
  });
});

it('keeps pure file Tasks out of media versions for both lineage and tracker', () => {
  const report = asset('print-report', 'report', 'application/vnd.art-ai.print-report+json');
  const fileSchema = { ...schema, task_states: [...schema.task_states, { internal_state: 'SUCCEEDED', public_state: 'SUCCEEDED', outcome: 'success', terminal: true, label: '成功', color: '', progress: { mode: 'reported' } }], capabilities: [...schema.capabilities, { id: 'print-analysis', domain: 'processing' }], runtime: { ...schema.runtime, frontend: { ...schema.runtime.frontend, model_processing_capabilities: ['rigging', 'print-analysis'] } } } as ProductSchema;
  const source = parseLineage({ nodes: [task('report-only', 1, [report], { capability: 'print-analysis', state: 'SUCCEEDED' })], edges: [] });
  expect(buildVersionTiles({ schema: fileSchema, graph: source, tracked: [], sessionId: 's1' }).tiles).toEqual([]);
  const raw = { id: 'print-report', role: 'report', variant: 'json', delivery: 'inline', ordinal: 0, mediaType: report.media_type, size: 32, url: report.url, kind: 'other' as const };
  const pendingProjection = tracked({ taskId: 'report-only', capability: 'print-analysis', terminal: true, state: 'SUCCEEDED', assets: [raw], downloadables: [raw] });
  expect(buildVersionTiles({ schema: fileSchema, graph: undefined, tracked: [pendingProjection], sessionId: 's1' }).tiles).toEqual([]);
});
it('keeps media version identities while attachments remain on the public Task detail', () => {
  const report = asset('report', 'report', 'application/vnd.art-ai.print-report+json');
  const source = parseLineage({ nodes: [task('model-with-report', 1, [model('model-with-report'), report]), task('image-with-file', 2, [image('image-with-file'), asset('notes','other','text/plain')])], edges: [] });
  const result = buildVersionTiles({ schema, graph: source, tracked: [], sessionId: 's1' });
  expect(result.tiles.map(tile => [tile.domain, tile.version])).toEqual([['model',1],['image',1]]);
  expect(result.tiles[0].assets.map(asset => asset.id)).toEqual(['model-with-report-m']);
  expect(result.tiles[0].taskId).toBe('model-with-report');
  expect(result.tiles[1].assets.map(asset => asset.id)).toEqual(['image-with-file-a']);
  expect(result.tiles[1].taskId).toBe('image-with-file');
});

it('retains the real group media identity when an attachment sorts before the image', () => {
  const source = parseLineage({ nodes: [task('front', 1, [asset('report-first', 'report', 'application/vnd.art-ai.print-report+json',0), asset('front-image','image','image/png',1)], { group: 'views', slot: 'front' })], edges: [], groups: [{ id: 'views', kind: 'three_view', label: 'Views', complete: true, source_slot: 'front', members: [{ slot: 'front', label: 'Front', task_id: 'front' }] }] });
  const result = buildVersionTiles({ schema, graph: source, tracked: [], sessionId: 's1' });
  expect(result.tiles[0].slots[0].assetId).toBe('front-image');
  expect(result.tiles[0].assets.map(asset => asset.id)).toEqual(['front-image']);
});

it('does not resurrect a known file-only graph Task as a guessed media tracker tile', () => {
  const fileSchema = { ...schema, task_states: [...schema.task_states, { internal_state: 'SUCCEEDED', public_state: 'SUCCEEDED', outcome: 'success', terminal: true, label: '成功', color: '', progress: { mode: 'reported' } }], capabilities: [...schema.capabilities, { id: 'print-analysis', domain: 'processing' }] } as ProductSchema;
  const source = parseLineage({ nodes: [task('report-only', 1, [asset('report','report','application/vnd.art-ai.print-report+json')], { capability: 'print-analysis', state: 'SUCCEEDED' })], edges: [] });
  const stale = tracked({ taskId: 'report-only', capability: 'print-analysis', terminal: true, state: 'SUCCEEDED', assets: [], downloadables: [] });
  expect(buildVersionTiles({ schema: fileSchema, graph: source, tracked: [stale], sessionId: 's1' }).tiles).toEqual([]);
});

it.each([['clip', 'model/fbx'], ['print-archive', 'model/3mf']])('never creates media versions for explicit %s files with model MIME', (role, mime) => {
  const source = parseLineage({ nodes: [task('file-only',1,[asset('file',role,mime)], { capability: 'rigging' })], edges: [] });
  expect(buildVersionTiles({ schema, graph: source, tracked: [], sessionId: 's1' }).tiles).toEqual([]);
});
