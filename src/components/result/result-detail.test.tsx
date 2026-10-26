import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { parseTaskDetail, type TaskDetailWire } from '@/lib/workspace/task-detail';
import { schemaText } from '@/lib/product-schema';
import { targetOfTaskAssets } from '@/lib/workspace/result-target';
import { parseModelFormatCoverage, type ModelFormatCoverage } from '@/lib/workspace/model-formats';
import { blankTile, type VersionTiles } from '@/lib/workspace/versions';

const schema = defaultCatalog.application as unknown as ProductSchema;
const state: { task: TaskDetailWire | null; offering: OfferingWire | undefined; scope: string } = { task: null, offering: undefined, scope: 'auto' };
const versions: VersionTiles = { tiles: [], byTask: new Map(), byCapture: new Map(), fileTaskParents: new Map() };
const versionLookups: (string | null)[] = [];
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: (sessionId: string | null) => { versionLookups.push(sessionId); return versions; } }));

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }),
}));
vi.mock('@/hooks/use-task-detail', () => ({
  useTaskDetail: () => ({ task: state.task, offering: state.offering, error: undefined, isLoading: false }),
}));
// Server rendering reads a store's initial state, so the active session is fixed here instead of set on the store.
vi.mock('@/store/chat-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat-store')>();
  const hook = Object.assign((selector: (value: { activeSessionId: string }) => unknown) => selector({ activeSessionId: 's1' }), actual.useChatStore);
  return { ...actual, useChatStore: hook };
});
vi.mock('@/hooks/use-session-agent-scope', () => ({ useActiveAgentScope: () => state.scope }));
const formats: { coverage: ModelFormatCoverage | null } = { coverage: null };
vi.mock('@/hooks/use-model-format-coverage', () => ({ useModelFormatCoverage: (route: string | null) => ({ data: route ? formats.coverage : undefined, error: undefined, mutate: async () => undefined }) }));

import { previewKindOf, ResultDetail } from './result-detail';

const offering = (metered: boolean) => ({
  id: 'o1', revision: 'r', provider_id: 'openai', provider_name: 'OpenAI', model_id: 'gpt-image-2', model_name: 'GPT Image 2', capability: 'text-to-image',
  available: true, credits_metered: metered,
  parameters: [{ name: 'size', label: '输出尺寸', type: 'text', semantic: 'output-size' }, { name: 'n', label: '生成数量', type: 'number' }],
}) as unknown as OfferingWire;

const ready = (extra: object = {}) => parseTaskDetail({
  id: 't1', capability: 'text-to-image', offering_id: 'o1', provider_id: 'openai', model_id: 'gpt-image-2', state: 'SUCCEEDED', progress: 100,
  created_at: '2026-10-08T10:00:00Z', finished_at: '2026-10-08T10:00:30Z', assets: [{ id: 'a1', media_type: 'image/png' }],
  input: { prompt: '银色机械猫', parameters: { size: '1024x1024', n: 1 } }, credits_charged: 6, ...extra,
});

const target = { taskId: 't1', sessionId: 's1', kind: 'image' as const, url: '/api/v1/tasks/t1/assets/a1', assetId: 'a1', title: '2D v1', status: 'ready' as const };

const rowOf = (html: string, key: string) => html.match(new RegExp(`data-key="${key}"[^>]*>(.*?)</div>`))?.[1] ?? '';

beforeEach(() => {
  state.task = null; state.offering = undefined; state.scope = 'auto';
  versions.tiles = []; versions.byTask = new Map(); versions.fileTaskParents = new Map();
  versionLookups.length = 0; formats.coverage = null;
});

describe('ResultDetail', () => {
  it('renders a source jump and step, and keeps missing evidence unknown', () => {
    state.task = ready({ capability: 'remesh' });
    const parent = blankTile({ key: 'parent', taskId: 'source', domain: 'model', status: 'ready', version: 4 });
    const child = blankTile({ key: 'child', taskId: 't1', domain: 'model', status: 'ready', parents: [{ key: 'parent', known: true, kind: 'source_task', evidence: 'recorded', link: 'process', step: 'remesh' }] });
    versions.tiles = [parent, child]; versions.byTask.set('t1', 'child');
    const html = renderToStaticMarkup(<ResultDetail target={target} />);
    expect(html).toContain('data-testid="result-detail-source-jump"');
    expect(rowOf(html, 'source')).toContain('来自 3D v4');
    expect(rowOf(html, 'step')).toContain(schema.capabilities.find((item) => item.id === 'remesh')?.label_cn);
    child.parents[0].known = false;
    const unknown = renderToStaticMarkup(<ResultDetail target={target} />);
    expect(rowOf(unknown, 'source')).toContain('来源未知');
    expect(unknown).not.toContain('result-detail-source-jump');
  });
  it('shows model, size, ratio, count, prompt and credits for a finished task', () => {
    state.task = ready(); state.offering = offering(true);
    const html = renderToStaticMarkup(<ResultDetail target={target} />);
    expect(html).toContain('data-status="ready"');
    expect(rowOf(html, 'model')).toContain('GPT Image 2');
    expect(rowOf(html, 'size')).toContain('1024x1024');
    expect(rowOf(html, 'ratio')).toContain('1:1');
    expect(rowOf(html, 'count')).toContain('1');
    expect(rowOf(html, 'prompt')).toContain('银色机械猫');
    expect(rowOf(html, 'credits')).toContain('6');
    expect(html).toContain('data-testid="result-detail-download"');
    expect(html).toContain('data-testid="result-detail-open-canvas"');
    expect(html).toContain('data-testid="result-detail-regenerate"');
  });

  it('has no credits row for an unmetered provider', () => {
    state.task = ready(); state.offering = offering(false);
    expect(renderToStaticMarkup(<ResultDetail target={target} />)).not.toContain('data-key="credits"');
  });

  it('shows the live status of a generating task and offers no file actions', () => {
    state.task = ready({ state: 'RUNNING', finished_at: undefined, assets: [] });
    const html = renderToStaticMarkup(<ResultDetail target={{ ...target, url: '', status: 'running' }} />);
    expect(html).toContain('data-status="running"');
    expect(html).toContain('data-testid="result-detail-live"');
    expect(html).not.toContain('result-detail-download');
    expect(html).not.toContain('result-detail-open-canvas');
    expect(html).not.toContain('result-detail-regenerate');
  });

  it('shows the failure reason of a failed task', () => {
    state.task = ready({ state: 'FAILED', session_id: 's1', assets: [], failure: { class: 'x', retryable: false, detail: '上游拒绝了请求' } });
    const html = renderToStaticMarkup(<ResultDetail target={{ ...target, url: '', status: 'failed' }} />);
    expect(html).toContain('data-status="failed"');
    expect(html).toContain('data-testid="result-detail-failure"');
    expect(html).toContain('上游拒绝了请求');
    expect(html).toContain('data-testid="task-recovery-loading"');
  });

  it('links a recovered task to its original task and resolves its returned model bytes for preview', () => {
    state.task = ready({ session_id: 's1', recovery_source: {
      source_task_id: 'source-1', source_attempt_id: 'attempt-1', source_offering_id: 'model', source_offering_revision: 'r1', receipt_digest: 'opaque',
    }, assets: [{ id: 'model-asset', role: 'model', media_type: 'model/gltf-binary', url: '/api/v1/tasks/t1/assets/model-asset' }] });
    const html = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'model', assetId: undefined, url: '' }} />);
    expect(html).toContain('data-testid="task-recovery-source-link"');
    expect(html).toContain('data-testid="task-recovery-open-source"');
    expect(html).toContain('data-kind="model"');
    expect(html).not.toContain('opaque');
  });

  it('hides the conversion the Agent mode cannot run', () => {
    state.task = ready(); state.offering = offering(true);
    state.scope = '2d';
    expect(renderToStaticMarkup(<ResultDetail target={target} />)).not.toContain('result-detail-to3d');
    state.scope = 'auto';
    expect(renderToStaticMarkup(<ResultDetail target={target} />)).toContain('result-detail-to3d');
  });

  it('pages through the results of one group', () => {
    state.task = ready(); state.offering = offering(true);
    const member = (assetId: string) => ({ ...target, assetId, url: `/api/v1/tasks/t1/assets/${assetId}` });
    const group = [member('a1'), member('a2'), member('a3')];
    const middle = renderToStaticMarkup(<ResultDetail target={{ ...group[1], group }} />);
    expect(middle).toContain('data-testid="result-detail-position"');
    expect(rowOf(middle, 'model')).toContain('GPT Image 2');
    expect(middle).toMatch(/result-detail-position[^>]*>2 \/ 3</);
    // The visible label sits over a hidden slot sized to the widest index, so the
    // narrow "1"/"7" digits cannot re-center the pager when the page changes.
    expect(middle).toMatch(/aria-hidden="true"[^>]*>8 \/ 8</);
    expect(middle).not.toMatch(/disabled=""[^>]*data-testid="result-detail-prev"|data-testid="result-detail-prev"[^>]*disabled=""/);
    const wideGroup = Array.from({ length: 12 }, (_, index) => member(`w${index}`));
    const tenth = renderToStaticMarkup(<ResultDetail target={{ ...wideGroup[9], group: wideGroup }} />);
    expect(tenth).toMatch(/result-detail-position[^>]*>10 \/ 12</);
    expect(tenth).toMatch(/aria-hidden="true"[^>]*>88 \/ 88</);
    const first = renderToStaticMarkup(<ResultDetail target={{ ...group[0], group }} />);
    expect(first).toMatch(/disabled=""[^>]*data-testid="result-detail-prev"|data-testid="result-detail-prev"[^>]*disabled=""/);
    expect(renderToStaticMarkup(<ResultDetail target={target} />)).not.toContain('result-detail-group');
  });

  it('keeps the pager and the fixed preview outside the scrolling detail body', () => {
    state.task = ready(); state.offering = offering(true);
    const group = [target, { ...target, assetId: 'a2', url: '/api/v1/tasks/t1/assets/a2' }];
    const html = renderToStaticMarkup(<ResultDetail target={{ ...target, group }} />);
    const pager = html.indexOf('data-testid="result-detail-group"');
    const preview = html.indexOf('data-testid="result-detail-preview"');
    const body = html.indexOf('data-testid="result-detail-body"');
    expect(pager).toBeGreaterThan(-1);
    expect(preview).toBeGreaterThan(pager);
    expect(body).toBeGreaterThan(preview);
    // Rows, actions and downloads scroll inside the body, so turning a page never
    // moves the pager or resizes the preview frame.
    const scrolling = html.slice(body);
    for (const inside of ['result-detail-rows', 'result-detail-actions', 'result-detail-downloads']) {
      expect(scrolling).toContain(inside);
    }
    expect(html).toContain('h-[min(42dvh,24rem)]');
    expect(html).not.toContain('min-h-48 items-center justify-center overflow-hidden');
    expect(html).not.toContain('max-h-[50vh]');
  });

  it('gives a file without a preview a fixed compact frame', () => {
    const file = { kind: 'other' as const, url: '/api/v1/assets/x/content', title: 'f.zip', mediaType: 'application/zip', fileName: 'f.zip', status: 'ready' as const };
    const html = renderToStaticMarkup(<ResultDetail target={file} />);
    expect(html).toContain('data-kind="none"');
    expect(html).toMatch(/h-24 items-center justify-center overflow-hidden[^>]*data-kind="none"/);
  });

  it('keeps one fixed preview frame whatever the media ratio or kind', () => {
    const cases = [
      { kind: 'image' as const, url: '/api/v1/assets/x/content', mediaType: 'image/png', title: 'x.png' },
      { kind: 'other' as const, url: '/api/v1/assets/x/content', mediaType: 'video/mp4', title: 'x.mp4' },
      { kind: 'model' as const, url: '/api/v1/assets/x/content', mediaType: 'model/gltf-binary', title: 'x.glb' },
      { kind: 'splat' as const, url: '/api/v1/assets/x/content', mediaType: 'model/ply', title: 'x.ply' },
    ];
    for (const item of cases) {
      const html = renderToStaticMarkup(<ResultDetail target={{ ...item, status: 'ready' as const }} />);
      expect(html).toContain('h-[min(42dvh,24rem)]');
      expect(html).toContain(`data-kind="${previewKindOf(item)}"`);
    }
  });

  it('shows the conversions of another session dimmed, with the reason on the button', () => {
    state.task = ready(); state.offering = offering(true);
    const html = renderToStaticMarkup(<ResultDetail target={{ ...target, sessionId: 'other' }} />);
    const reason = schemaText(schema, 'ui.result.detail.disabled.other_session');
    expect(html).toContain('data-testid="result-detail-to3d-blocked"');
    expect(html).toContain('data-testid="result-detail-open-canvas-blocked"');
    // The reason leads the buttons' hover hint (a HintTooltip), not a native title; the label still carries it for readers.
    expect(html).not.toContain('title=');
    expect(html).toContain(`aria-label="${schemaText(schema, 'ui.studio.result.to_3d')}: ${reason}"`);
    // The reason is also written out as text, and the dimmed buttons point at it.
    expect(html).toMatch(new RegExp(`data-testid="result-detail-blocked-reason"[^>]*>${reason}<`));
    expect(html).toContain('aria-describedby="result-detail-blocked-reason"');
    expect(html).not.toContain('data-testid="result-detail-to3d"');
    state.scope = '2d';
    expect(renderToStaticMarkup(<ResultDetail target={{ ...target, sessionId: 'other' }} />)).not.toContain('result-detail-to3d-blocked');
  });

  it('keeps downloads in one area under the actions: formats for a finished 3D task, one file otherwise', () => {
    const model = { ...target, kind: 'model' as const, url: '' };
    const html = renderToStaticMarkup(<ResultDetail target={model} />);
    expect(html).toContain('data-testid="result-detail-downloads"');
    expect(html).toContain('data-testid="model-archive-link"');
    expect(html).toContain('data-testid="model-formats-panel"');
    expect(html).not.toContain('data-testid="result-detail-download"');
    expect(html.indexOf('result-detail-actions')).toBeLessThan(html.indexOf('result-detail-downloads'));
    expect(renderToStaticMarkup(<ResultDetail target={{ ...model, status: 'running' }} />)).not.toContain('result-detail-downloads');

    state.task = ready(); state.offering = offering(true);
    const image = renderToStaticMarkup(<ResultDetail target={target} />);
    const actions = image.slice(image.indexOf('data-testid="result-detail-actions"'), image.indexOf('<section'));
    expect(actions).not.toContain('download');
    expect(image).toContain('data-testid="result-detail-download"');
    expect(image).not.toContain('model-formats-panel');
  });

  it('renders an uploaded asset with preview, file info and download only', () => {
    const asset = { kind: 'image' as const, url: '/api/v1/assets/x/content', title: 'cat.png', mediaType: 'image/png', detail: '12 KB · 2026-10-08', fileName: 'cat.png', status: 'ready' as const };
    const html = renderToStaticMarkup(<ResultDetail target={asset} />);
    expect(html).toContain('data-has-task="false"');
    expect(html).toContain('data-testid="result-detail-preview"');
    expect(html).toContain('12 KB');
    expect(html).toContain('data-testid="result-detail-download"');
    for (const hidden of ['result-detail-rows', 'result-detail-open-canvas', 'result-detail-to3d', 'result-detail-regenerate', 'result-detail-copy-prompt']) {
      expect(html).not.toContain(hidden);
    }
  });
});

describe('previewKindOf', () => {
  it('picks the preview by kind and media type', () => {
    expect(previewKindOf({ kind: 'model', url: '/m.glb' })).toBe('model');
    expect(previewKindOf({ kind: 'image', url: '/a.png' })).toBe('image');
    expect(previewKindOf({ kind: 'other', url: '/v.mp4', mediaType: 'video/mp4' })).toBe('video');
    expect(previewKindOf({ kind: 'other', url: '/f.zip', mediaType: 'application/zip' })).toBe('none');
    expect(previewKindOf({ kind: 'image', url: '' })).toBe('none');
  });
});

it('shows every real other artifact as an authenticated attachment with Schema labels and facts', () => {
  state.task = ready({ capability: 'print-analysis', assets: [{ id: 'report', role: 'report', variant: 'json', media_type: 'application/vnd.art-ai.print-report+json', size: 120, ordinal: 0, url: '/api/v1/tasks/t1/assets/report' }, { id: 'extra', role: 'extra', variant: 'zip', media_type: 'application/zip', size: 2048, ordinal: 1, url: '/api/v1/tasks/t1/assets/extra' }] });
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'other', url: '', assetId: undefined }} />);
  expect(html).toContain('data-testid="result-detail-attachments"'); expect(html).toContain('href="/api/v1/tasks/t1/assets/report"'); expect(html).toContain('application/zip'); expect(html).toContain('2.05 KB');
  expect(html).toContain(schema.task_history.assets.attachments.role_labels.report);
  expect(html).not.toContain('<img'); expect(html).not.toContain('result-detail-open-canvas'); expect(html).not.toContain('model-formats-panel');
});
it('retains model formats while also exposing an attached normalized report', () => {
  state.task = ready({ assets: [{ id: 'model', role: 'model', media_type: 'model/gltf-binary', url: '/api/v1/tasks/t1/assets/model' }, { id: 'report', role: 'report', variant: 'json', media_type: 'application/vnd.art-ai.print-report+json', size: 120, url: '/api/v1/tasks/t1/assets/report' }] });
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'model', url: '' }} />);
  expect(html).toContain('model-formats-panel'); expect(html).toContain('result-detail-attachments');
});
it('never navigates to a remote or placeholder attachment URL', () => {
  state.task = ready({ assets: [{ id: 'unsafe', role: 'report', media_type: 'application/zip', url: 'https://foreign.invalid/a' }, { id: 'placeholder', role: 'report', media_type: 'application/zip', url: '#' }] });
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'other', url: '' }} />);
  expect(html).not.toContain('href="https://foreign'); expect(html).not.toContain('href="#"');
});

it.each([['clip', 'model/fbx'], ['print-archive', 'model/3mf']])('offers the real %s artifact for download without a model viewer/formats/source action', (role, media_type) => {
  state.task = ready({ assets: [{ id: 'file', role, media_type, size: 4096, variant: media_type.split('/')[1], url: '/api/v1/tasks/t1/assets/file' }] });
  const fileTarget = targetOfTaskAssets({ taskId: 't1', sessionId: 's1', title: 'File', status: 'ready' }, state.task!.assets);
  expect(fileTarget.kind).toBe('other');
  const html = renderToStaticMarkup(<ResultDetail target={fileTarget} />);
  expect(html).toContain('href="/api/v1/tasks/t1/assets/file"'); expect(html).toContain('result-detail-attachment'); expect(html).toContain(media_type);
  expect(html).not.toContain('<img'); expect(html).not.toContain('model-formats-panel'); expect(html).not.toContain('result-detail-open-canvas'); expect(html).not.toContain('result-detail-to2d');
});


// #189: a lampshade build delivers two STL parts and no previewable model. Each
// part is its own download; the detail must not offer only the first one.
it('offers every model file of a task without a viewable model as its own download', () => {
  const parts = [
    { id: 'base', role: 'model', variant: 'base_stl', media_type: 'model/stl', size: 360284, ordinal: 0, url: '/api/v1/tasks/t1/assets/base' },
    { id: 'lamp', role: 'model', variant: 'lamp_stl', media_type: 'model/stl', size: 13541784, ordinal: 1, url: '/api/v1/tasks/t1/assets/lamp' },
  ];
  state.task = ready({ capability: 'creative-lab-build', assets: parts });
  const fileTarget = targetOfTaskAssets({ taskId: 't1', sessionId: 's1', title: 'Lamp', status: 'ready' }, state.task!.assets);
  const html = renderToStaticMarkup(<ResultDetail target={fileTarget} />);
  expect(html).toContain('href="/api/v1/tasks/t1/assets/base"'); expect(html).toContain('href="/api/v1/tasks/t1/assets/lamp"');
  expect(html.match(/data-testid="result-detail-attachment-download"/g)?.length).toBe(2);
  expect(html).not.toContain('data-testid="result-detail-download"');
  // A task that has a viewable model keeps its formats area and lists no model file twice.
  state.task = ready({ assets: [{ id: 'glb', role: 'model', variant: 'glb', media_type: 'model/gltf-binary', url: '/api/v1/tasks/t1/assets/glb' }, { ...parts[1], id: 'stl', variant: 'stl', url: '/api/v1/tasks/t1/assets/stl' }] });
  const viewable = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'model', url: '' }} />);
  expect(viewable).toContain('model-formats-panel'); expect(viewable).not.toContain('result-detail-attachments');
});

// #189: a detail opened from the account page carries no session; its source
// comes from the session the task itself belongs to, not 「来源未知」.
it('reads the source from the task session when the entry carries no session', () => {
  state.task = ready({ capability: 'creative-lab-build', session_id: 's9' });
  const parent = blankTile({ key: 'preview', taskId: 'proto', domain: 'image', status: 'ready', version: 1 });
  const child = blankTile({ key: 'build', taskId: 't1', domain: 'model', status: 'ready', parents: [{ key: 'preview', known: true, kind: 'source_task', evidence: 'recorded', link: 'process', step: '' }] });
  versions.tiles = [parent, child]; versions.byTask.set('t1', 'build'); versions.byTask.set('proto', 'preview');
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, sessionId: undefined }} />);
  expect(versionLookups).toContain('s9');
  expect(rowOf(html, 'source')).not.toContain(schemaText(schema, 'ui.result.detail.unknown_source'));
});

// #189: the keycap build delivers its OBJ inside a ZIP bundle. The format list
// already offers it as the OBJ row, so it is not listed again as an attachment;
// a bundle the list does not cover (the fridge magnet's ZIP) stays one.
it('lists a delivered file once: in the format list or among the attachments', () => {
  const assets = [
    { id: 'glb', role: 'model', variant: 'glb', media_type: 'model/gltf-binary', url: '/api/v1/tasks/t1/assets/glb' },
    { id: 'zip', role: 'bundle', variant: 'obj_zip', media_type: 'application/zip', size: 2048, url: '/api/v1/tasks/t1/assets/zip' },
  ];
  state.task = ready({ capability: 'creative-lab-build', assets });
  formats.coverage = parseModelFormatCoverage({ task_id: 't1', applicable: true, steps: [], formats: [
    { id: 'glb', label: 'GLB', status: 'available', task_id: 't1', asset_id: 'glb' },
    { id: 'obj', label: 'OBJ', status: 'available', task_id: 't1', asset_id: 'zip' },
  ] });
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'model', url: '' }} />);
  expect(html).toContain('model-formats-panel');
  expect(html).not.toContain('result-detail-attachments');
  formats.coverage = parseModelFormatCoverage({ task_id: 't1', applicable: true, steps: [], formats: [
    { id: 'glb', label: 'GLB', status: 'available', task_id: 't1', asset_id: 'glb' },
  ] });
  const uncovered = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'model', url: '' }} />);
  expect(uncovered).toContain('href="/api/v1/tasks/t1/assets/zip"');
  expect(uncovered).toContain('result-detail-attachments');
});

it.each([true, false])('keeps the selected candidate for preview and download (URL known: %s)', (hasURL) => {
  state.task = ready({ assets: ['a1', 'a2', 'a3'].map((id) => ({ id, role: 'image', media_type: 'image/png', url: `/api/v1/tasks/t1/assets/${id}` })) });
  const group = ['a1', 'a2', 'a3'].map((assetId) => ({ ...target, assetId, url: `/api/v1/tasks/t1/assets/${assetId}` }));
  const selected = { ...group[1], url: hasURL ? group[1].url : '', group };
  const html = renderToStaticMarkup(<ResultDetail target={selected} />);
  expect(html.match(/<img[^>]+src="([^"]+)"/)?.[1]).toBe(group[1].url);
  expect(html).toContain(`href="${group[1].url}"`);
  expect(html).not.toContain(`href="${group[0].url}"`);
  expect(html).toMatch(/result-detail-position[^>]*>2 \/ 3</);
  expect(html).toContain('data-testid="result-detail-open-canvas"');
});

it('resolves an unselected task placeholder consistently for preview and explicit stage actions', () => {
  state.task = ready({ assets: [{ id: 'a1', role: 'image', media_type: 'image/png', url: target.url }] });
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, kind: 'model', assetId: undefined, url: '' }} />);
  expect(html).toContain('data-kind="image"');
  expect(html).toContain('data-testid="result-detail-download"');
  expect(html).toContain('data-testid="result-detail-open-canvas"');
  expect(html).toContain('data-testid="result-detail-to3d"');
  expect(html).not.toContain('data-testid="result-detail-to2d"');
  expect(html).not.toContain('model-formats-panel');
});

it('does not substitute another asset when the explicitly selected asset is absent', () => {
  state.task = ready({ assets: [{ id: 'a1', role: 'image', media_type: 'image/png', url: target.url }] });
  const html = renderToStaticMarkup(<ResultDetail target={{ ...target, assetId: 'unavailable', url: '' }} />);
  expect(html).toContain('data-kind="none"');
  expect(html).not.toContain('data-testid="result-detail-download"');
  expect(html).not.toContain('data-testid="result-detail-open-canvas"');
});
