import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { buildDetailRows, conversionAllowed, creditsText, detailActions, detailBlockedReason, detailPrompt, detailSource, detailStatus, formatDuration, parseTaskDetail, type ActionContext } from './task-detail';
import { buildVersionTiles } from './versions';
import { parseLineage } from './lineage';

const schema = defaultCatalog.application as unknown as ProductSchema;

const offering = (extra: Partial<OfferingWire> = {}): OfferingWire => ({
  id: 'openai/gpt-image-2/text-to-image', revision: 'r', provider_id: 'openai', provider_name: 'OpenAI', model_id: 'gpt-image-2', model_name: 'GPT Image 2',
  capability: 'text-to-image', available: true, credits_metered: true,
  parameters: [
    { name: 'prompt', label: 'Prompt', type: 'text', semantic: 'prompt' },
    { name: 'size', label: '输出尺寸', type: 'text', semantic: 'output-size', options: [{ value: '1536x1024', label: '1536x1024（横）' }] },
    { name: 'n', label: '生成数量', type: 'number' },
    { name: 'quality', label: '质量', type: 'select', options: [{ value: 'high', label: '高' }], presentation: { surface: 'provider' } },
    { name: 'seed_secret', label: '内部', type: 'text', presentation: { surface: 'hidden' } },
    { name: 'image', label: '参考图', type: 'asset' },
  ],
  ...extra,
}) as OfferingWire;

const wire = {
  id: 't1', capability: 'text-to-image', offering_id: 'openai/gpt-image-2/text-to-image', provider_id: 'openai', model_id: 'gpt-image-2',
  state: 'SUCCEEDED', progress: 100, created_at: '2026-10-08T10:00:00Z', finished_at: '2026-10-08T10:01:12Z',
  assets: [{ id: 'a1', media_type: 'image/png' }, { id: 'a2', media_type: 'image/png' }],
  input: { prompt: '银色机械猫', parameters: { size: '1536x1024', n: 2, quality: 'high', seed_secret: 's', image: 'x' } },
  credits_charged: 12,
};

const labelsOf = (rows: Array<{ key: string; label: string; value: string }>) => Object.fromEntries(rows.map((row) => [row.key, row.value]));

describe('parseTaskDetail', () => {
  it('reads the public task, the stored input and the image count', () => {
    const task = parseTaskDetail(wire);
    expect(task).toMatchObject({ id: 't1', prompt: '银色机械猫', creditsCharged: 12, assetCount: 2, modelId: 'gpt-image-2' });
    expect(task?.parameters.size).toBe('1536x1024');
  });

  it('copes with a server that does not report input or credits', () => {
    const task = parseTaskDetail({ id: 't2', state: 'RUNNING', assets: [] });
    expect(task).toMatchObject({ prompt: '', creditsCharged: null, parameters: {} });
    expect(parseTaskDetail(null)).toBeNull();
    expect(parseTaskDetail({})).toBeNull();
  });

  it('reads the owner-scoped recovery provenance without exposing receipt or storage fields', () => {
    const task = parseTaskDetail({ ...wire, recovery_source: {
      source_task_id: 'source-1', source_attempt_id: 'attempt-1', source_offering_id: 'meshy/text', source_offering_revision: 'r4',
      receipt_digest: 'digest',
    } });
    expect(task?.recoverySource).toEqual({ sourceTaskId: 'source-1', sourceAttemptId: 'attempt-1', sourceOfferingId: 'meshy/text', sourceOfferingRevision: 'r4' });
    expect(JSON.stringify(task?.recoverySource)).not.toContain('digest');
    expect(parseTaskDetail({ ...wire, recovery_source: { source_task_id: '' } })?.recoverySource).toBeNull();
  });
});

describe('buildDetailRows', () => {
  it('adds a recorded source version and step; unknown parents never become jump targets', () => {
    const graph = parseLineage({ nodes: [
      { id: 'parent', task_id: 'parent', kind: 'task', version: 8, assets: [{ id: 'pa', role: 'model', media_type: 'model/gltf-binary', url: '/api/v1/tasks/parent/assets/pa' }] },
      { id: 't1', task_id: 't1', kind: 'task', capability: 'remesh', assets: [{ id: 'ta', role: 'model', media_type: 'model/gltf-binary', url: '/api/v1/tasks/t1/assets/ta' }] },
    ], edges: [{ from: 'parent', to: 't1', kind: 'source_task', evidence: 'recorded' }], next_cursor: '' });
    const versions = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
    const source = detailSource(schema, 't1', versions);
    expect(source).toEqual({ label: '来自 3D v8', parentKey: 'n:parent' });
    const task = parseTaskDetail({ ...wire, capability: 'remesh' });
    const rows = buildDetailRows(schema, { task, source });
    expect(rows.find((row) => row.key === 'source')).toMatchObject({ value: '来自 3D v8', parentKey: 'n:parent' });
    expect(rows.find((row) => row.key === 'step')?.value).toBe(schema.capabilities.find((item) => item.id === 'remesh')?.label_cn);
    graph.edges[0].evidence = 'unknown';
    const unknown = detailSource(schema, 't1', buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' }));
    expect(unknown).toEqual({ label: '来源未知', parentKey: '' });
    expect(buildDetailRows(schema, { task, source: unknown }).find((row) => row.key === 'source')?.parentKey).toBeUndefined();
  });
  it('keeps a report source without making the report a media version', () => {
    const graph = parseLineage({ nodes: [
      { id: 'source', task_id: 'source', kind: 'task', created_at: '2026-10-08T10:00:00Z', assets: [{ id: 'model', role: 'model', media_type: 'model/gltf-binary', url: '/model' }] },
      { id: 'check', task_id: 'check', kind: 'task', capability: 'rig-check', state: 'SUCCEEDED', created_at: '2026-10-08T10:01:00Z', assets: [{ id: 'report', role: 'report', media_type: 'application/vnd.art-ai.rig-check-report+json', url: '/report' }] },
      { id: 'next', task_id: 'next', kind: 'task', created_at: '2026-10-08T10:02:00Z', assets: [{ id: 'next-model', role: 'model', media_type: 'model/gltf-binary', url: '/next-model' }] },
    ], edges: [{ from: 'source', to: 'check', kind: 'source_task', evidence: 'recorded' }], next_cursor: '' });
    const build = () => buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
    const versions = build();
    expect(versions.tiles.map(tile => [tile.taskId, tile.domain, tile.version])).toEqual([['source', 'model', 1], ['next', 'model', 2]]);
    expect(versions.byTask.has('check')).toBe(false);
    const source = detailSource(schema, 'check', versions);
    expect(source).toEqual({ label: '来自 3D v1', parentKey: 'n:source' });
    const task = parseTaskDetail({ ...wire, id: 'check', capability: 'rig-check', input: { parameters: { source_task: 'source' } } });
    expect(buildDetailRows(schema, { task, source }).find(row => row.key === 'source')).toMatchObject({ value: '来自 3D v1', parentKey: 'n:source' });

    graph.edges[0].evidence = 'unknown';
    expect(detailSource(schema, 'check', build())).toEqual({ label: '来源未知', parentKey: '' });
    graph.edges[0].evidence = 'recorded';
    graph.nodes = graph.nodes.filter(node => node.id !== 'source');
    expect(detailSource(schema, 'check', build())).toEqual({ label: '来源未知', parentKey: '' });
  });

  it('lists model, size, ratio, count, declared parameters, credits and times; skips hidden, asset and prompt parameters', () => {
    const rows = labelsOf(buildDetailRows(schema, { task: parseTaskDetail(wire), offering: offering() }));
    expect(rows.model).toBe('GPT Image 2 · OpenAI');
    expect(rows.size).toBe('1536x1024（横）');
    expect(rows.ratio).toBe('3:2');
    expect(rows.count).toBe('2');
    expect(rows['param.quality']).toBe('高');
    expect(Object.keys(rows)).not.toContain('param.seed_secret');
    expect(Object.keys(rows)).not.toContain('param.image');
    expect(Object.keys(rows)).not.toContain('param.prompt');
    expect(rows.credits).toContain('12');
    expect(rows.duration).toContain('1');
    expect(rows.created).not.toBe('');
  });

  it('counts delivered pictures only for an image task, never the texture maps and thumbnail of a 3D task', () => {
    const imageOnly = offering({ parameters: [] });
    expect(labelsOf(buildDetailRows(schema, { task: parseTaskDetail({ ...wire, input: {} }), offering: imageOnly })).count).toBe('2');
    const model = parseTaskDetail({
      ...wire, capability: 'image-to-3d', input: {},
      assets: [
        { id: 'm', role: 'model', variant: 'glb', media_type: 'model/gltf-binary' },
        ...['base_color', 'metallic', 'normal', 'roughness'].map((map) => ({ id: map, role: 'texture', variant: `texture.1.${map}`, media_type: 'image/png' })),
        { id: 'thumb', role: 'thumbnail', variant: 'primary', media_type: 'image/png' },
      ],
    });
    const rows = labelsOf(buildDetailRows(schema, { task: model, offering: offering({ capability: 'image-to-3d', parameters: [] }) }));
    expect(Object.keys(rows)).not.toContain('count');
  });

  it('reads a boolean parameter with declared options as the option label, and a plain boolean as yes/no', () => {
    const base = offering();
    const withBooleans = offering({
      parameters: [
        ...base.parameters,
        { name: 'mode', label: '生成模式', type: 'boolean', options: [{ value: 'false', label: '常规' }, { value: 'true', label: '分部件' }], presentation: { surface: 'provider' } },
        { name: 'plain', label: '开关', type: 'boolean', presentation: { surface: 'provider' } },
      ] as OfferingWire['parameters'],
    });
    const task = parseTaskDetail({ ...wire, input: { prompt: 'x', parameters: { mode: true, plain: true } } });
    const rows = labelsOf(buildDetailRows(schema, { task, offering: withBooleans }));
    expect(rows['param.mode']).toBe('分部件');
    expect(rows['param.plain']).toBe(schemaText(schema, 'ui.result.detail.yes'));
    const regular = parseTaskDetail({ ...wire, input: { prompt: 'x', parameters: { mode: false } } });
    expect(labelsOf(buildDetailRows(schema, { task: regular, offering: withBooleans }))['param.mode']).toBe('常规');
  });

  it('leaves a withdrawn option value out of a frozen parameter', () => {
    const withFormats = offering({ parameters: [{
      name: 'target_formats', label: '目标格式', type: 'string-array',
      options: ['glb', 'fbx', 'obj', 'usdz', 'stl', '3mf'].map((value) => ({ value, label: value.toUpperCase() })),
      presentation: { surface: 'provider' },
    }] as OfferingWire['parameters'] });
    const task = parseTaskDetail({ ...wire, input: { prompt: 'x', parameters: { target_formats: ['glb', 'fbx', 'blend'] } } });
    const rows = labelsOf(buildDetailRows(schema, { task, offering: withFormats }));
    expect(rows['param.target_formats']).toBe('GLB、FBX');
  });

  it('has no credits row when the offering is not credits-metered, even if a number is reported', () => {
    const rows = labelsOf(buildDetailRows(schema, { task: parseTaskDetail(wire), offering: offering({ credits_metered: false }) }));
    expect(rows.credits).toBeUndefined();
    expect(labelsOf(buildDetailRows(schema, { task: parseTaskDetail(wire), offering: undefined })).credits).toBeUndefined();
    expect(creditsText(schema, offering({ credits_metered: false }), 12)).toBe('/');
    expect(creditsText(schema, undefined, null)).toBe('/');
    expect(creditsText(schema, offering(), null)).toBe('/');
  });

  it('falls back to the measured picture size while the stored input is unknown', () => {
    const rows = labelsOf(buildDetailRows(schema, { task: parseTaskDetail({ id: 't3', state: 'RUNNING', assets: [] }), offering: offering(), measured: { width: 1024, height: 1024 } }));
    expect(rows.size).toBe('1024x1024');
    expect(rows.ratio).toBe('1:1');
  });

  it('shows no time rows for an unfinished task and keeps the credits row of a metered offering', () => {
    const rows = buildDetailRows(schema, { task: parseTaskDetail({ id: 't4', state: 'RUNNING', created_at: '2026-10-08T10:00:00Z', assets: [] }), offering: offering() });
    const keys = rows.map((row) => row.key);
    expect(keys).toContain('credits');
    expect(keys).toContain('created');
    expect(keys).not.toContain('finished');
    expect(keys).not.toContain('duration');
  });
});

describe('prompt, status, duration', () => {
  it('prefers the stored prompt, then the one known where the card is', () => {
    expect(detailPrompt({ task: parseTaskDetail(wire), fallbackPrompt: 'x' })).toBe('银色机械猫');
    expect(detailPrompt({ task: null, fallbackPrompt: '  草稿 ' })).toBe('草稿');
  });

  it('maps task states through the Schema, never a local table', () => {
    const outcomes = schema.task_states.map((state) => [state.internal_state, detailStatus(schema, state.internal_state)] as const);
    expect(outcomes.some(([, status]) => status === 'ready')).toBe(true);
    expect(outcomes.some(([, status]) => status === 'failed')).toBe(true);
    expect(detailStatus(schema, 'NOT_A_STATE')).toBeNull();
  });

  it('words durations in minutes only past a minute', () => {
    expect(formatDuration(schema, '2026-10-08T10:00:00Z', '2026-10-08T10:00:09Z')).toContain('9');
    expect(formatDuration(schema, '2026-10-08T10:00:00Z', '2026-10-08T10:01:12Z')).toContain('12');
    expect(formatDuration(schema, '', '')).toBe('');
  });
});

describe('detailActions', () => {
  const base: ActionContext = { kind: 'image', status: 'ready', hasTask: true, inActiveSession: true, hasPrompt: true, hasFile: true, scope: 'auto' };

  it('offers everything on a finished picture in auto mode', () => {
    expect(detailActions(base)).toEqual({ download: true, openOnCanvas: true, sendTo3D: true, sendTo2D: false, regenerate: true, copyPrompt: true });
  });

  it('hides 3D conversion in 2D mode and the 2D screenshot in 3D mode', () => {
    expect(detailActions({ ...base, scope: '2d' }).sendTo3D).toBe(false);
    expect(detailActions({ ...base, kind: 'model', scope: '3d' }).sendTo2D).toBe(false);
    expect(detailActions({ ...base, kind: 'model', scope: '2d' }).sendTo2D).toBe(true);
    expect(detailActions({ ...base, kind: 'model' }).sendTo3D).toBe(false);
  });

  it('offers no file actions while generating, and no regenerate until it ends', () => {
    const running = detailActions({ ...base, status: 'running', hasFile: false });
    expect(running).toMatchObject({ download: false, openOnCanvas: false, sendTo3D: false, regenerate: false, copyPrompt: true });
    const failed = detailActions({ ...base, status: 'failed', hasFile: false });
    expect(failed).toMatchObject({ download: false, regenerate: true });
  });

  it('keeps an uploaded asset to download only', () => {
    expect(detailActions({ ...base, hasTask: false, hasPrompt: false })).toEqual({ download: true, openOnCanvas: false, sendTo3D: false, sendTo2D: false, regenerate: false, copyPrompt: false });
  });

  it('keeps a result of another session off the stage', () => {
    expect(detailActions({ ...base, inActiveSession: false })).toMatchObject({ openOnCanvas: false, sendTo3D: false, regenerate: false });
  });
});

describe('detailBlockedReason / conversionAllowed', () => {
  const base: ActionContext = { kind: 'image', status: 'ready', hasTask: true, inActiveSession: true, hasPrompt: true, hasFile: true, scope: 'auto' };

  it('gives a reason only for a finished media result of another session', () => {
    expect(detailBlockedReason(base)).toBeNull();
    expect(detailBlockedReason({ ...base, inActiveSession: false })).toBe('ui.result.detail.disabled.other_session');
    expect(detailBlockedReason({ ...base, inActiveSession: false, status: 'running', hasFile: false })).toBeNull();
    expect(detailBlockedReason({ ...base, inActiveSession: false, hasTask: false })).toBeNull();
  });

  it('allows only the conversion of the current mode', () => {
    expect(conversionAllowed('image', 'auto')).toBe('sendTo3D');
    expect(conversionAllowed('image', '2d')).toBeNull();
    expect(conversionAllowed('model', '3d')).toBeNull();
    expect(conversionAllowed('model', '2d')).toBe('sendTo2D');
    expect(conversionAllowed('other', 'auto')).toBeNull();
  });
});

describe('credits row for every credits-metered offering of the catalog', () => {
  const metered: Array<{ id: string; capability: string; provider_id: string; credits_metered: boolean }> = [];
  const visit = (node: unknown) => {
    if (Array.isArray(node)) node.forEach(visit);
    else if (node && typeof node === 'object') {
      const record = node as Record<string, unknown>;
      if (record.credits_metered === true && typeof record.id === 'string' && typeof record.capability === 'string') metered.push(record as never);
      Object.values(record).forEach(visit);
    }
  };
  visit(defaultCatalog);

  it('covers generation and post-processing offerings of Meshy and Tripo', () => {
    const ids = metered.map((item) => item.id);
    for (const id of ['meshy/processing/remesh', 'meshy/processing/retexture', 'meshy/processing/rigging', 'meshy/processing/animation', 'tripo/tripo-v3.1/format-conversion', 'meshy/meshy-7.1/text-to-3d', 'tripo/tripo-v3.1-full/image-to-3d']) {
      expect(ids).toContain(id);
    }
  });

  it.each(metered.map((item) => [item.id, item] as const))('%s shows the reported credits as a number', (_id, item) => {
    const wireTask = { id: 't', capability: item.capability, offering_id: item.id, provider_id: item.provider_id, state: 'SUCCEEDED', credits_charged: 7, assets: [] };
    const rows = labelsOf(buildDetailRows(schema, { task: parseTaskDetail(wireTask), offering: offering({ id: item.id, capability: item.capability, credits_metered: true }) }));
    expect(rows.credits).toMatch(/7/);
    expect(rows.credits).not.toBe('/');
  });
});

it('retains every public attachment fact without inventing image/model outputs', () => {
  const report = { id: 'report', role: 'report', variant: 'json', delivery: 'inline', ordinal: 2, media_type: 'application/vnd.art-ai.print-report+json', size: 800, url: '/api/v1/tasks/t1/assets/report' };
  const task = parseTaskDetail({ ...wire, assets: [report] });
  expect(task?.assets).toEqual([{ id: 'report', role: 'report', variant: 'json', delivery: 'inline', ordinal: 2, deliveryProfile: '', mediaType: report.media_type, size: 800, url: report.url, kind: 'other', required: false }]);
  expect(task?.assetCount).toBe(0);
});
