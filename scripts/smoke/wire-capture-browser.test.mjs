import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chooseOffering, createCanvasInitialization, localRoute, numericOrigin, requestDecision, run, syntheticPrompt } from './wire-capture-browser.mjs';

const base = 'http://127.0.0.1:18765';

test('accepts only canonical numeric loopback origins with explicit ports', () => {
  for (const value of [base, 'http://127.0.0.2:18765', 'http://[::1]:18765']) assert.equal(numericOrigin(value), value);
  for (const value of ['http://localhost:18765', 'https://127.0.0.1:18765', 'http://0.0.0.0:18765',
    'http://192.0.2.1:18765', `${base}/`, `${base}?token=private`, `${base}#fragment`,
    'http://user:password@127.0.0.1:18765', 'http://2130706433:18765', 'http://127.1:18765',
    'http://127.0.0.1:0', 'http://127.0.0.1:65536', 'http://127.999.0.1:18765']) {
    assert.throws(() => numericOrigin(value));
  }
});

test('Schema routes stay same-origin; local API queries are not upstream capture queries', () => {
  assert.equal(localRoute('/api/v1/offerings?capability=text-to-3d', base), '/api/v1/offerings?capability=text-to-3d');
  for (const value of ['//example.test/path', 'https://example.test', '/\\example.test', '/api#fragment']) {
    assert.throws(() => localRoute(value, base));
  }
});

test('only read requests and one-shot login/empty-session writes can pass', () => {
  const permitted = new Map([['POST /api/auth/mock', {}], ['POST /api/chat/sessions', { title: '' }]]);
  for (const route of ['/assets/app.js', '/model-viewer/draco/draco_decoder.wasm',
    '/api/v1/tasks/task_public/assets/asset_public', '/api/v1/tasks?limit=1']) {
    assert.equal(requestDecision(base + route, 'GET', base, permitted), 'read');
  }
  assert.equal(requestDecision(`blob:${base}/local-image`, 'GET', base, permitted), 'local_asset');
  for (const url of ['http://127.0.0.1:18766/', 'https://example.test/model.glb?secret=private',
    `${base}.example.test/`, 'blob:https://example.test/local-image', 'file:///private',
    `http://user:password@127.0.0.1:18765/`]) {
    assert.equal(requestDecision(url, 'GET', base, permitted), 'blocked_origin');
  }
  assert.equal(requestDecision(base + '/api/auth/mock?x=1', 'POST', base, permitted), 'blocked_write');
  for (const [route, body] of [['/api/auth/mock', '{}'], ['/api/chat/sessions', '{"title":""}']]) {
    assert.equal(requestDecision(base + route, 'POST', base, permitted, '{"extra":true}'), 'blocked_write');
    assert.equal(requestDecision(base + route, 'POST', base, permitted, body), 'write');
    assert.equal(requestDecision(base + route, 'POST', base, permitted, body), 'blocked_write');
  }
  for (const [method, route] of [['POST', '/api/v1/tasks/prepare'], ['POST', '/api/v1/tasks/grants'],
    ['PUT', '/api/v1/tasks/grants/request'], ['POST', '/api/v1/tasks'], ['POST', '/api/chat/stream'],
    ['POST', '/api/v1/tasks/task/retry'], ['PUT', '/api/v1/chat/sessions/session/approval'],
    ['POST', '/api/canvas/projects'], ['PUT', '/api/canvas/projects/project/nodes']]) {
    assert.equal(requestDecision(base + route, method, base, permitted), 'blocked_write');
    assert.equal(requestDecision(base + route, method, base, permitted, null, true), 'write');
  }
  assert.equal(requestDecision('http://127.0.0.1:18766/x', 'POST', base, permitted, null, true), 'blocked_origin');
  assert.equal(requestDecision(base + '/api/chat/stream', 'GET', base, permitted, null, true), 'read');
});

const canvasSchema = {
  runtime: { http: { legacy_routes: { canvas_projects: '/api/canvas/projects',
    canvas_project: '/api/canvas/projects/{projectID}', canvas_nodes: '/api/canvas/projects/{projectID}/nodes' } } },
  canvas: { api: { path_parameters: { project_id: 'projectID' }, fields: {
    id: 'id', user_id: 'userId', session_id: 'sessionId', created: 'created', nodes: 'nodes', revision: 'revision',
    canvas_width: 'canvasWidth', canvas_height: 'canvasHeight', coordinate_version: 'coordinateVersion',
    placed_results: 'placedResults', project_id: 'projectId', type: 'type', x: 'x', y: 'y', width: 'width', height: 'height',
    z_index: 'zIndex', data: 'data', visible: 'visible', locked: 'locked',
  } }, defaults: { coordinate_version: 2 }, frontend: { auto_save_delay_milliseconds: 1500,
    artboard: { default_width: 2048, default_height: 2048, center_x: 600, center_y: 400 },
    initial_layer: { enabled: true, fill: 'transparent' }, upload_task_id: 'upload',
    export: { transparent_background_value: 'transparent' } } },
  content: { messages: { 'ui.canvas.layers.initial_name': 'Initial layer' } },
};
const project = { id: 'canvas-new', userId: 'owner', sessionId: 'session-new', created: true, revision: 1 };
const node = { id: 'node-new', type: 'image', x: -424, y: -624, width: 2048, height: 2048, zIndex: 0,
  data: '{"taskId":"upload","name":"Initial layer","blank":true}', visible: true, locked: false };
const save = { nodes: [node], revision: 1, canvasWidth: 2048, canvasHeight: 2048, coordinateVersion: 2,
  placedResults: ['since:1970-01-01T00:00:00.000Z'] };
function startup() {
  const guard = createCanvasInitialization(canvasSchema, base, 'owner');
  guard.bindSession('session-new');
  return guard;
}
function boundCanvas() {
  const guard = startup();
  assert.equal(guard.allow(base + '/api/canvas/projects', 'POST', '{"sessionId":"session-new"}'), 'canvas_create');
  guard.bindProject(project);
  return guard;
}

test('canvas create permits only the exact new session payload once, using public field names', () => {
  const guard = startup(), url = base + '/api/canvas/projects', body = '{"sessionId":"session-new"}';
  for (const invalid of ['{', '[]', 'null', '{}', '{"session_id":"session-new"}',
    '{"sessionId":"another-session"}', '{"sessionId":"session-new","name":"extra"}']) {
    assert.equal(guard.allow(url, 'POST', invalid), null);
  }
  for (const target of [url + '?x=1', url + '?', url + '/', url + '#', base + '/api/canvas/../canvas/projects',
    'http://127.0.0.1:18766/api/canvas/projects', base + '/api/v1/tasks']) {
    assert.equal(guard.allow(target, 'POST', body), null);
  }
  assert.equal(guard.allow(url, 'PATCH', body), null);
  assert.equal(guard.allow(url, 'POST', body), 'canvas_create');
  assert.equal(guard.allow(url, 'POST', body), null, 'duplicate cannot spend a second allowance');
  const renamed = structuredClone(canvasSchema);
  renamed.canvas.api.fields.session_id = 'publicSession';
  const other = createCanvasInitialization(renamed, base, 'owner');
  other.bindSession('session-new');
  assert.equal(other.allow(url, 'POST', body), null);
  assert.equal(other.allow(url, 'POST', '{"publicSession":"session-new"}'), 'canvas_create');
});

test('canvas response must bind a new project to the authenticated owner and new session before any save', () => {
  for (const change of [{ userId: 'other' }, { sessionId: 'other' }, { created: false }, { id: '../other' }, { revision: 2 }]) {
    const guard = startup();
    guard.allow(base + '/api/canvas/projects', 'POST', '{"sessionId":"session-new"}');
    assert.throws(() => guard.bindProject({ ...project, ...change }));
    assert.equal(guard.allow(base + '/api/canvas/projects/canvas-new/nodes', 'PUT', JSON.stringify(save)), null);
  }
  assert.throws(() => startup().bindProject(project), 'unobserved create has no response allowance');
  const guard = boundCanvas();
  assert.throws(() => guard.bindProject(project), 'project binding is one-shot');
});

test('autosave allows one blank initialization of the bound project at the exact revision/version; all other writes fail', () => {
  const guard = boundCanvas(), url = base + '/api/canvas/projects/canvas-new/nodes';
  for (const [method, target] of [['PUT', url.replace('canvas-new', 'another-project')], ['PUT', url + '?x=1'],
    ['POST', url], ['PATCH', base + '/api/canvas/projects/canvas-new'], ['DELETE', base + '/api/canvas/projects/canvas-new'],
    ['POST', base + '/api/v1/tasks'], ['POST', base + '/api/chat/stream'],
    ['POST', base + '/api/v1/agent/consent'], ['POST', base + '/api/upload']]) {
    assert.equal(guard.allow(target, method, JSON.stringify(save)), null);
  }
  for (const change of [{ revision: 2 }, { coordinateVersion: 1 }, { canvasWidth: 1024 }, { placedResults: ['task/asset'] }, { placedResults: [] },
    { sessionId: 'another-session' }, { nodes: [] }, { nodes: [{ ...node, data: '{"imageUrl":"/api/v1/assets/foreign"}' }] }]) {
    assert.equal(guard.allow(url, 'PUT', JSON.stringify({ ...save, ...change })), null);
  }
  assert.deepEqual(guard.counts, { canvas_create: 1, canvas_save: 0 });
  assert.equal(guard.allow(url, 'PUT', JSON.stringify(save)), 'canvas_save');
  assert.equal(guard.allow(url, 'PUT', JSON.stringify(save)), null);
  const persisted = { ...project, revision: 2, canvasWidth: 2048, canvasHeight: 2048, coordinateVersion: 2,
    placedResults: ['since:1970-01-01T00:00:00.000Z'],
    nodes: [{ ...node, projectId: project.id }] };
  guard.assertSaved(persisted);
  for (const change of [{ userId: 'other' }, { id: 'other' }, { revision: 3 }, { nodes: [{ ...node, projectId: 'other' }] }]) {
    assert.throws(() => guard.assertSaved({ ...persisted, ...change }));
  }
});

test('Meshy choice uses public capability, availability and recommendation; no model fallback', () => {
  const first = { provider_id: 'meshy', capability: 'text-to-3d', id: 'meshy/first/text-to-3d', available: true };
  const recommended = { ...first, id: 'meshy/recommended/text-to-3d', recommended: true };
  const disabled = { ...first, id: 'meshy/disabled/text-to-3d', available: false };
  const other = { ...first, provider_id: 'other', id: 'other/default', recommended: true };
  assert.equal(chooseOffering([other, first, recommended, disabled]).selected.id, recommended.id);
  assert.equal(chooseOffering([first, recommended], first.id).selected.id, first.id);
  assert.throws(() => chooseOffering([first, disabled], disabled.id));
  assert.throws(() => chooseOffering([other]));
  assert.throws(() => chooseOffering([{ ...first, available: undefined }]));
});

test('invalid target and live/mock conflict fail before browser launch without leaking input', async () => {
  const canary = 'credential-and-query-canary';
  for (const args of [[], ['--mock-preflight', '--live', '--base', base],
    ['--mock-preflight', '--base', `http://user:${canary}@127.0.0.1:18765/?${canary}`],
    ['--prompt', canary]]) {
    const { report, exitCode } = await run(args);
    assert.notEqual(exitCode, 0);
    assert.equal(report.failure_stage, 'arguments');
    assert.equal(report.submitted, false);
    assert.equal(report.capture, 'blocked_capture');
    assert(!JSON.stringify(report).includes(canary));
    assert(!JSON.stringify(report).includes(syntheticPrompt));
    assert.equal(report.browser_version, undefined);
  }
});
