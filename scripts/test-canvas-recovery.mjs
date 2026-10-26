#!/usr/bin/env node
// Mounted, loopback-only browser regression for canvas recovery bugs:
// - #312: a persisted per-session viewport is overwritten on reload by the image-count fit, and the
//   restored current object must not fit over that viewport either.
// - #313: an automatic result registers its placement receipt before the picture is on the board,
//   so a failed load or a session switch leaves a receipt without a node and the replay is skipped;
//   a stale automatic-join plan must not re-add a picture after the person deleted it.
//
// The real WorkspaceCanvasBoard runs against the Product Schema. Every HTTP response is a fixture in this
// process: no app server, provider, paid call or deployment is involved.
//
//   node scripts/test-canvas-recovery.mjs
//
// Optional env: PLAYWRIGHT_MODULE_PATH, OUT (report and screenshots), ENGINES (default chromium,webkit).
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { deflateSync } from 'node:zlib';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright');
const catalog = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const schema = catalog.application;
const offering = catalog.offerings.find((item) => item.id === 'openai/gpt-image-2/image-to-image');
assert(offering, 'missing GPT image-to-image offering');

const OUT = process.env.OUT || await mkdtemp(path.join(tmpdir(), 'canvas-recovery-'));
assert(path.isAbsolute(OUT), 'OUT must be absolute');
await mkdir(OUT, { recursive: true, mode: 0o700 });
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const results = [];

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (crc ^ 0xffffffff) >>> 0;
}
function pngChunk(type, data) {
  const name = Buffer.from(type, 'ascii');
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length, 0);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([name, data])), 0);
  return Buffer.concat([length, name, data, crc]);
}
const pngCache = new Map();
function png(width, height) {
  const key = `${width}x${height}`;
  if (pngCache.has(key)) return pngCache.get(key);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const stride = width * 4 + 1;
  const raw = Buffer.alloc(stride * height);
  for (let y = 0; y < height; y += 1) {
    raw[y * stride] = 0;
    for (let x = 0; x < width; x += 1) {
      const offset = y * stride + 1 + x * 4;
      raw[offset] = x < width / 2 ? 220 : 40;
      raw[offset + 1] = 60;
      raw[offset + 2] = x < width / 2 ? 40 : 220;
      raw[offset + 3] = 255;
    }
  }
  const body = Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', deflateSync(raw, { level: 6 })),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
  pngCache.set(key, body);
  return body;
}

const SINCE = 'since:';
const RECEIPT = [SINCE + '2026-10-20T00:00:00Z'];
const SIZE = { width: 2048, height: 2048 };
const SEED_VIEWPORT = { zoom: 0.37, panX: 321, panY: -123 };
const SECOND_VIEWPORT = { zoom: 0.29, panX: -140, panY: 266 };

const VIRTUAL_REQUEST = 'virtual:canvas-recovery';
const VIRTUAL_ID = '\0canvas-recovery';
const fixtureApp = `
  import React, { StrictMode } from 'react';
  import { createRoot } from 'react-dom/client';
  import '${path.join(root, 'src/app/globals.css').replaceAll('\\', '/')}';
  import { ProductSchemaProvider } from '@/components/product-schema-provider';
  import { Stage } from '@/components/studio/stage';
  import { WorkspaceCanvasBoard } from '@/components/workspace/workspace-canvas-board';
  import { useCanvasStore } from '@/store/canvas-store';
  import { useChatStore } from '@/store/chat-store';
  import { useStudioStore } from '@/store/studio-store';
  import { useWorkspaceStore } from '@/store/workspace-store';

  const schema = window.__ART_AI_PRODUCT_SCHEMA__;
  const fixture = window.__FIXTURE__;
  useChatStore.getState().setSessions(fixture.sessions);
  useChatStore.getState().setActiveSession('s1');
  useWorkspaceStore.setState({ modelByDomain: fixture.modelByDomain, currentObject: null, trackedTasks: [] });
  useCanvasStore.setState({ selectedNodeIds: [] });
  window.__canvasStore = useCanvasStore;
  window.__chatStore = useChatStore;
  window.__studioStore = useStudioStore;
  window.__workspaceStore = useWorkspaceStore;

  const h = React.createElement;
  function App() {
    // The restored-object case needs the real Stage, whose opening restoration supplies currentObject.
    const body = fixture.mountStage ? h(Stage) : h(WorkspaceCanvasBoard);
    return h(ProductSchemaProvider, { initialSchema: schema },
      h('div', { className: 'flex h-dvh w-full' },
        h('section', { className: '@container relative flex min-h-0 min-w-0 flex-1 flex-col bg-canvas' },
          h('div', { className: 'relative flex min-h-0 flex-1' }, body))));
  }
  createRoot(document.getElementById('root')).render(h(StrictMode, null, h(App)));
`;

function nodeRecord(id, x, y, width, height, data, zIndex = 1) {
  return {
    id, projectId: '', type: 'image', x, y, width, height, zIndex,
    data: JSON.stringify(data), visible: true, locked: false,
    createdAt: '2026-10-21T00:00:00Z', updatedAt: '2026-10-21T00:00:00Z',
  };
}

function projectFor(id, sessionId, nodes, placedResults) {
  return {
    id, name: `Canvas ${id}`, sessionId, userId: 'fixture-user',
    width: SIZE.width, height: SIZE.height, revision: 1, nodes, placedResults,
    created: false, saves: [], saveAttempts: 0,
  };
}

function lineageTask(id, url, createdAt = '2026-10-21T01:00:00Z', operation = '') {
  return {
    id, task_id: id, kind: 'task', state: 'SUCCEEDED', capability: operation === 'inpaint' ? 'image-to-image' : 'text-to-image',
    operation, created_at: createdAt,
    assets: [{ id: `${id}_asset`, role: 'image', variant: '', media_type: 'image/png', ordinal: 0, url, kind: 'image' }],
  };
}

function scenarioFixture(name) {
  const imageNode = nodeRecord('seed', 0, 0, 1200, 800, {
    imageUrl: '/api/v1/assets/seed', taskId: 'seed_task',
  });
  const blankNode = nodeRecord('blank', 0, 0, SIZE.width, SIZE.height, { blank: true });
  const secondImage = nodeRecord('second', 100, 100, 900, 900, {
    imageUrl: '/api/v1/assets/second', taskId: 'second_task',
  });
  const projects = {
    project_1: projectFor('project_1', 's1', [], RECEIPT),
    project_2: projectFor('project_2', 's2', [], RECEIPT),
  };
  if (name === 'blank') projects.project_1.nodes = [blankNode];
  else if (name === 'no-viewport') projects.project_1.nodes = [imageNode];
  else if (name === 'aba') {
    projects.project_1.nodes = [imageNode];
    projects.project_2.nodes = [secondImage];
  } else if (name === 'stage-restored') {
    projects.project_1.nodes = [imageNode];
    // The seed picture is already on the board; only the Stage restoration may consume it.
    projects.project_1.placedResults = [...RECEIPT, 'seed_task#seed_task_asset'];
  } else if (name === 'stage-quiet-first') {
    // A board the person already works on, without a current object yet. Stage opens the session here.
    projects.project_1.nodes = [imageNode];
  } else if (['viewport', 'pending-race', 'stale-plan'].includes(name) || name.startsWith('fail-once')) {
    projects.project_1.nodes = name === 'viewport' ? [imageNode] : [];
  } else if (name.startsWith('pending')) {
    projects.project_1.nodes = [];
  }
  if (name === 'legacy') projects.project_1.placedResults = null;
  const lineage = name.startsWith('fail-once') ? { nodes: [lineageTask('once_task', '/api/v1/assets/once')] }
    : name.startsWith('pending') ? { nodes: [lineageTask('pending_task', '/api/v1/assets/pending')] }
      : name === 'legacy' ? { nodes: [lineageTask('old_task', '/api/v1/assets/once', '2026-10-19T01:00:00Z')] }
        : name === 'stage-restored' ? { nodes: [lineageTask('seed_task', '/api/v1/assets/seed', '2026-10-20T01:00:00Z')] }
          : name === 'stage-quiet-first' ? { nodes: [] }
            : name === 'stale-plan' ? { nodes: [lineageTask('newer_task', '/api/v1/assets/newer', '2026-10-21T02:00:00Z')] }
            : name === 'suppress' ? { nodes: [
              lineageTask('inpaint_task', '/api/v1/assets/inpaint', '2026-10-21T02:00:00Z', 'inpaint'),
              lineageTask('normal_task', '/api/v1/assets/normal', '2026-10-21T02:01:00Z'),
            ] }
              : { nodes: [] };
  return {
    name, size: SIZE, projects, lineage,
    mountStage: name === 'stage-restored' || name === 'stage-quiet-first',
    lastObject: name === 'stage-restored'
      ? { kind: 'image', taskId: 'seed_task', assetId: 'seed_task_asset' } : null,
    // Full-route control for held pictures: each picture waits until the test releases it.
    assets: name === 'stale-plan' ? {
      older: { requests: 0, hold: true, released: false, waiters: [] },
      newer: { requests: 0, hold: true, released: false, waiters: [] },
    } : name === 'stage-quiet-first' ? {
      late: { requests: 0, hold: true, released: false, waiters: [] },
    } : null,
    sessions: [
      { id: 's1', title: 'fixture one', createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-21T00:00:00Z', agentScope: 'auto' },
      { id: 's2', title: 'fixture two', createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-21T00:00:00Z', agentScope: 'auto' },
    ],
    modelByDomain: { image: offering.model_id, 'model-3d': null },
    viewportSeeds: name === 'aba'
      ? { s1: SEED_VIEWPORT, s2: SECOND_VIEWPORT }
      : name === 'no-viewport' ? {} : { s1: SEED_VIEWPORT },
    pending: false, release: null, assetAttempts: 0, projectReads: [],
    pickReads: 0, lineageReads: 0,
  };
}

function projectWire(project) {
  const now = '2026-10-21T00:00:00Z';
  return {
    id: project.id, userId: project.userId, name: project.name, sessionId: project.sessionId,
    viewport: '{"zoom":1,"panX":0,"panY":0}', bgStyle: 'dots', createdAt: now, updatedAt: now,
    revision: project.revision,
    // The wire needs both timestamps on every node; a node read from a saved PUT body has none.
    nodes: project.nodes.map((node) => ({
      ...node, projectId: project.id,
      createdAt: node.createdAt ?? now, updatedAt: node.updatedAt ?? now,
    })),
    placedResults: project.placedResults,
    canvasWidth: project.width, canvasHeight: project.height, coordinateVersion: 2,
    created: project.created,
  };
}

await createServer({
  configFile: false,
  root: path.join(root, 'web'),
  publicDir: path.join(root, 'public'),
  cacheDir: path.join(OUT, '.vite'),
  resolve: { alias: { '@': path.join(root, 'src') } },
  logLevel: 'error',
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{
    name: 'canvas-recovery-fixture',
    resolveId(id) { if (id === VIRTUAL_REQUEST) return VIRTUAL_ID; },
    load(id) { return id === VIRTUAL_ID ? fixtureApp : null; },
    transformIndexHtml(html) {
      // Vite may already have tagged the entry script; replace whatever main.tsx URL it carries so the
      // fixture app always mounts instead of the real router (whose login page would hide the failure).
      return html.replace('{{.Schema}}', JSON.stringify(schema))
        .replace(/src="[^"]*\/main\.tsx[^"]*"/, `src="/@id/${VIRTUAL_REQUEST}"`);
    },
  }],
}).then(async (server) => {
  await server.listen();
  const base = server.resolvedUrls.local[0].replace(/\/$/, '');
  const origin = new URL(base).origin;
  try {
    for (const engine of ENGINES) {
      for (const name of ['viewport', 'blank', 'no-viewport', 'aba', 'fail-once', 'pending-hop', 'pending-race', 'legacy', 'suppress',
        'stage-restored', 'stage-quiet-first', 'stale-plan']) {
        await record(engine, name, () => runCase(engine, name, base, origin));
      }
    }
  } finally {
    await server.close();
  }
  await writeFile(path.join(OUT, 'report.json'), JSON.stringify(results, null, 2), { mode: 0o600 });
  for (const item of results) console.log(`${item.passed ? 'PASS' : 'FAIL'} ${item.engine} ${item.name}${item.error ? `: ${item.error}` : ''}`);
  if (results.some((item) => !item.passed)) process.exitCode = 1;
});

async function record(engine, name, fn) {
  const started = Date.now();
  console.error(`START ${engine} ${name}`);
  try {
    await fn();
    results.push({ engine, name, passed: true, ms: Date.now() - started });
  } catch (error) {
    results.push({ engine, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
    console.error(`FAIL ${engine} ${name}`);
    console.error(error);
  }
}

async function runCase(engine, name, base, origin) {
  const fixture = scenarioFixture(name);
  const browser = await pw[engine].launch({ headless: true });
  const errors = [];
  const external = [];
  let page;
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' });
    page = await context.newPage();
    page.setDefaultTimeout(30_000);
    page.on('pageerror', (error) => errors.push(`pageerror:${error.message}`));
    page.on('console', (message) => { if (message.type() === 'error') errors.push(`console:${message.text()}`); });
    await page.addInitScript(({ initialSchema, fixture, seeds, lastObject }) => {
      window.__ART_AI_PRODUCT_SCHEMA__ = initialSchema;
      window.__FIXTURE__ = fixture;
      if (!sessionStorage.getItem('canvas-recovery-seeded')) {
        if (Object.keys(seeds).length > 0) {
          localStorage.setItem('canvas-store', JSON.stringify({ state: { viewportBySession: seeds }, version: 0 }));
        }
        if (lastObject) localStorage.setItem('studio.last-object.s1', JSON.stringify(lastObject));
        sessionStorage.setItem('canvas-recovery-seeded', '1');
      }
    }, {
      initialSchema: schema,
      fixture: {
        size: fixture.size, sessions: fixture.sessions, modelByDomain: fixture.modelByDomain,
        mountStage: fixture.mountStage,
      },
      seeds: fixture.viewportSeeds,
      lastObject: fixture.lastObject,
    });
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      if (url.origin !== origin) { external.push(url.href); await route.abort(); return; }
      if (!url.pathname.startsWith('/api/')) { await route.continue(); return; }
      if (url.pathname === schema.runtime.http.routes.product_schema) { await route.fulfill({ json: { schema } }); return; }
      if (url.pathname === schema.runtime.http.legacy_routes.version) {
        await route.fulfill({ json: { version: 'fixture' }, headers: { 'access-control-allow-origin': origin } });
        return;
      }
      if (url.pathname === schema.runtime.http.routes.client_errors) { await route.fulfill({ status: 204 }); return; }
      if (url.pathname === schema.runtime.http.routes.agent_params) {
        await route.fulfill({ json: { params: {}, parameter_states: {} } }); return;
      }
      if (url.pathname === schema.runtime.http.routes.offerings) {
        const capability = url.searchParams.get('capability');
        await route.fulfill({ json: { offerings: capability === 'image-to-image' ? [offering] : [] } });
        return;
      }
      const scopeOf = (id) => schema.runtime.http.routes.chat_session_agent_scope.replace('{sessionID}', id);
      const threeOf = (id) => schema.runtime.http.routes.chat_session_three_views.replace('{sessionID}', id);
      const lineageOf = (id) => schema.runtime.http.routes.chat_session_lineage.replace('{sessionID}', id);
      const pickOf = (id) => schema.runtime.http.routes.chat_session_pick.replace('{sessionID}', id);
      if (url.pathname === pickOf('s1') || url.pathname === pickOf('s2')) {
        fixture.pickReads += 1;
        await route.fulfill({ json: {} }); return;
      }
      if (url.pathname === scopeOf('s1') || url.pathname === scopeOf('s2')) {
        await route.fulfill({ json: { agent_scope: 'auto' } }); return;
      }
      if (url.pathname === threeOf('s1') || url.pathname === threeOf('s2')) {
        await route.fulfill({ json: { items: [] } }); return;
      }
      if (url.pathname === lineageOf('s1')) {
        fixture.lineageReads += 1;
        await route.fulfill({ json: { ...fixture.lineage, edges: [], groups: [], next_cursor: '' } }); return;
      }
      if (url.pathname === lineageOf('s2')) {
        await route.fulfill({ json: { nodes: [], edges: [], groups: [], next_cursor: '' } }); return;
      }
      if (url.pathname === schema.runtime.http.legacy_routes.canvas_projects && request.method() === 'POST') {
        const body = JSON.parse(request.postData() || '{}');
        const session = body[schema.canvas.api.fields.session_id];
        const project = fixture.projects[session === 's2' ? 'project_2' : 'project_1'];
        await route.fulfill({ json: projectWire(project) }); return;
      }
      const projectIDOf = (template) => {
        const [before, after] = template.split('{projectID}');
        if (!url.pathname.startsWith(before) || !url.pathname.endsWith(after)) return '';
        const id = url.pathname.slice(before.length, url.pathname.length - after.length);
        return id && !id.includes('/') ? decodeURIComponent(id) : '';
      };
      const project = fixture.projects[projectIDOf(schema.runtime.http.legacy_routes.canvas_project)];
      if (project && request.method() === 'GET') {
        fixture.projectReads.push(project.id);
        await route.fulfill({ json: projectWire(project) }); return;
      }
      const writeProject = fixture.projects[projectIDOf(schema.runtime.http.legacy_routes.canvas_nodes)];
      if (writeProject && request.method() === 'PUT') {
        const body = JSON.parse(request.postData() || '{}');
        const fields = schema.canvas.api.fields;
        writeProject.saveAttempts += 1;
        writeProject.nodes = (body[fields.nodes] ?? []).map((node) => ({ ...node }));
        if (Array.isArray(body[fields.placed_results])) writeProject.placedResults = body[fields.placed_results];
        writeProject.revision += 1;
        writeProject.saves.push({
          nodes: writeProject.nodes.map((node) => ({ id: node[fields.id], data: node.data })),
          placedResults: writeProject.placedResults,
        });
        await route.fulfill({ json: { ok: true, count: writeProject.nodes.length } }); return;
      }
      const assetPrefix = schema.runtime.http.routes.asset_item.replace('{assetID}', '');
      if (url.pathname.startsWith(assetPrefix) && request.method() === 'GET') {
        const id = decodeURIComponent(url.pathname.slice(assetPrefix.length));
        const held = fixture.assets?.[id];
        if (held) {
          held.requests += 1;
          if (held.hold && !held.released) {
            await new Promise((resolve) => held.waiters.push(resolve));
          }
          await route.fulfill({ status: 200, contentType: 'image/png', body: png(1200, 800) });
          return;
        }
        if (id === 'pending') {
          fixture.pendingRequests = (fixture.pendingRequests || 0) + 1;
          // Hold only the first request: a retry after the session returns must not wait forever.
          if (!fixture.pending) {
            fixture.pending = true;
            await new Promise((resolve) => { fixture.release = resolve; });
            fixture.released = true;
          }
        }
        if (id === 'once') {
          fixture.assetAttempts += 1;
        }
        if (id === 'once' && fixture.assetAttempts === 1) {
          await route.fulfill({ status: 404, body: 'first attempt fails' });
          return;
        }
        await route.fulfill({ status: 200, contentType: 'image/png', body: png(1200, 800) });
        return;
      }
      await route.fulfill({ status: 404, json: {} });
    });
    await page.goto(base, { waitUntil: 'domcontentloaded' });
    await waitLoaded(page);
    if (name === 'viewport' || name === 'blank') await checkViewport(page, fixture);
    else if (name === 'no-viewport') await checkNoViewport(page);
    else if (name === 'aba') await checkAba(page, fixture);
    else if (name === 'fail-once') await checkFailOnce(page, fixture);
    else if (name === 'pending-hop') await checkPendingHop(page, fixture);
    else if (name === 'pending-race') await checkPendingRace(page, fixture);
    else if (name === 'legacy') await checkLegacy(page, fixture);
    else if (name === 'suppress') await checkSuppress(page);
    else if (name === 'stage-restored') await checkStageRestored(page, fixture);
    else if (name === 'stage-quiet-first') await checkStageQuietFirst(page, fixture);
    else if (name === 'stale-plan') await checkStalePlan(page, fixture);
    assert.deepEqual(external, [], 'no external requests');
    const unexpected = errors.filter((error) => !error.includes('404') && !error.includes('ERR_ABORTED'));
    assert.deepEqual(unexpected, []);
    await page.screenshot({ path: path.join(OUT, `${engine}-${name}.png`) });
    await context.close();
  } catch (error) {
    console.error('diagnostic', { engine, name, errors, text: await page?.locator('body').innerText().catch(() => '') });
    throw error;
  } finally {
    await browser.close();
  }
}

async function waitLoaded(page, session = 's1') {
  await page.waitForSelector('[data-canvas-drawing-area]');
  await page.waitForFunction((id) => Boolean(window.__canvasStore.getState().projectBySession[id] &&
    window.__canvasStore.getState().canvasSizeBySession[id]), session);
  await page.waitForFunction(() => {
    const area = document.querySelector('[data-canvas-drawing-area]');
    return Boolean(area && area.clientWidth > 0 && area.clientHeight > 0);
  });
}

function readState(page, session = 's1') {
  return page.evaluate((id) => ({
    viewport: window.__canvasStore.getState().viewportBySession[id],
    nodes: (window.__canvasStore.getState().nodesBySession[id] ?? []).map((node) => ({
      id: node.id, imageUrl: node.imageUrl, blank: node.blank === true,
    })),
    placed: window.__canvasStore.getState().placedResultsBySession[id],
  }), session);
}

async function checkViewport(page, fixture) {
  await page.waitForTimeout(600);
  let state = await readState(page);
  assert.deepEqual(state.viewport, SEED_VIEWPORT, 'the saved viewport survives the first load');
  // A manual pan must survive an ordinary reload too.
  await page.evaluate(() => window.__canvasStore.getState().setViewport('s1', { zoom: 0.42, panX: 77, panY: 88 }));
  await page.waitForTimeout(300);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForTimeout(600);
  state = await readState(page);
  assert.deepEqual(state.viewport, { zoom: 0.42, panX: 77, panY: 88 }, 'a reload keeps the pan');
  assert.equal(state.nodes.length, 1, 'reload keeps the node');
  assert.equal(fixture.projects.project_1.saves.length, 0, 'restoring the view writes no node/size save');
  // Newly generated content still arrives in view: a picture added while a saved viewport is in use
  // changes the viewport to fit it.
  await page.evaluate(() => window.__canvasStore.getState().addNode('s1', {
    id: 'arrival', type: 'image', taskId: 'arrival_task', imageUrl: '/api/v1/assets/second',
    x: 2200, y: 1500, width: 700, height: 500, zIndex: 2,
  }));
  await page.waitForTimeout(500);
  const arrived = await readState(page);
  assert.notDeepEqual(arrived.viewport, state.viewport, 'a new picture changes the viewport');
  const visible = await page.evaluate(() => {
    const node = window.__canvasStore.getState().nodesBySession.s1.find((item) => item.imageUrl.endsWith('/second'));
    const view = window.__canvasStore.getState().viewportBySession.s1;
    const area = document.querySelector('[data-canvas-drawing-area]');
    if (!node || !view || !area) return false;
    const left = node.x * view.zoom + view.panX;
    const top = node.y * view.zoom + view.panY;
    return left + node.width * view.zoom > 0 && top + node.height * view.zoom > 0 && left < area.clientWidth && top < area.clientHeight;
  });
  assert(visible, 'the new picture is visible');
}

async function checkNoViewport(page) {
  await page.waitForTimeout(600);
  const state = await readState(page);
  assert(state.viewport && state.viewport.zoom > 0, 'a canvas without a saved view gets an initial fit');
  const visible = await page.evaluate(() => {
    const node = window.__canvasStore.getState().nodesBySession.s1.find((item) => item.imageUrl);
    const view = window.__canvasStore.getState().viewportBySession.s1;
    const area = document.querySelector('[data-canvas-drawing-area]');
    if (!node || !view || !area) return false;
    const left = node.x * view.zoom + view.panX;
    const top = node.y * view.zoom + view.panY;
    const right = left + node.width * view.zoom;
    const bottom = top + node.height * view.zoom;
    return right > 0 && bottom > 0 && left < area.clientWidth && top < area.clientHeight;
  });
  assert(visible, 'the first picture stays visible without a saved viewport');
}

async function checkAba(page, fixture) {
  await page.waitForTimeout(500);
  assert.deepEqual((await readState(page)).viewport, SEED_VIEWPORT, 's1 keeps its own viewport');
  await page.evaluate(() => window.__chatStore.getState().setActiveSession('s2'));
  await waitLoaded(page, 's2');
  await page.waitForTimeout(500);
  assert.deepEqual((await readState(page, 's2')).viewport, SECOND_VIEWPORT, 's2 keeps its own viewport');
  await page.evaluate(() => window.__chatStore.getState().setActiveSession('s1'));
  await waitLoaded(page, 's1');
  await page.waitForTimeout(500);
  assert.deepEqual((await readState(page, 's1')).viewport, SEED_VIEWPORT, 'returning to s1 restores its viewport');
}

async function checkFailOnce(page, fixture) {
  await page.waitForFunction(() => Array.isArray(window.__canvasStore.getState().placedResultsBySession.s1));
  await page.waitForTimeout(1200);
  let state = await readState(page);
  assert.equal(fixture.assetAttempts, 1, 'the fixture refused the first picture load');
  assert.equal(state.nodes.length, 0, 'a failed picture adds no node');
  assert(!state.placed.includes('once_task#once_task_asset'), 'a failed picture leaves no permanent receipt');
  assert.equal(fixture.projects.project_1.placedResults.includes('once_task#once_task_asset'), false,
    'the failed receipt was not persisted either');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
    .some((node) => node.imageUrl.endsWith('/api/v1/assets/once')));
  state = await readState(page);
  assert.equal(state.nodes.length, 1, 'the retried picture lands once');
  assert(state.placed.includes('once_task#once_task_asset'), 'the landed picture is registered');
  // A user deletion is not resurrected by the receipt.
  await page.evaluate(() => {
    const node = window.__canvasStore.getState().nodesBySession.s1[0];
    window.__canvasStore.getState().removeNode('s1', node.id);
  });
  await page.waitForTimeout(1800);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForTimeout(800);
  state = await readState(page);
  assert.equal(state.nodes.length, 0, 'a deleted result is not auto-added again');
}

async function checkPendingHop(page, fixture) {
  await waitForPending(page, fixture);
  const key = 'pending_task#pending_task_asset';
  await page.evaluate(() => window.__chatStore.getState().setActiveSession('s2'));
  await waitLoaded(page, 's2');
  fixture.release?.();
  fixture.release = null;
  await page.waitForTimeout(600);
  const hidden = await readState(page);
  assert(!hidden.placed.includes(key), 'a picture that finished while its board was hidden is not registered yet');
  await page.evaluate(() => window.__chatStore.getState().setActiveSession('s1'));
  await waitLoaded(page, 's1');
  try {
    await page.waitForFunction((receipt) => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
      .some((node) => node.imageUrl.endsWith('/api/v1/assets/pending')) && window.__canvasStore.getState().placedResultsBySession.s1.includes(receipt), key);
  } catch (error) {
    console.error('pending-hop state', {
      state: await readState(page), pendingRequests: fixture.pendingRequests, released: fixture.released,
      projectReads: fixture.projectReads,
    });
    throw error;
  }
  let state = await readState(page);
  assert.equal(state.nodes.filter((node) => node.imageUrl.endsWith('/pending')).length, 1, 'returning to the session resumes the picture once');
  assert(state.placed.includes(key), 'the resumed picture is registered after landing');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForTimeout(800);
  state = await readState(page);
  assert.equal(state.nodes.filter((node) => node.imageUrl.endsWith('/pending')).length, 1, 'a reload does not duplicate it');
}

async function checkPendingRace(page, fixture) {
  await waitForPending(page, fixture);
  await page.evaluate(() => {
    window.__workspaceStore.getState().setCurrentObject({
      taskId: 'pending_task', kind: 'image', url: '/api/v1/assets/pending', assetId: 'pending_task_asset', sessionId: 's1',
    });
  });
  fixture.release?.();
  fixture.release = null;
  await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
    .some((node) => node.imageUrl.endsWith('/api/v1/assets/pending')));
  await page.waitForTimeout(600);
  const state = await readState(page);
  assert.equal(state.nodes.filter((node) => node.imageUrl.endsWith('/pending')).length, 1, 'explicit selection and auto-join add one node');
  assert.equal(state.placed.filter((key) => key === 'pending_task#pending_task_asset').length, 1, 'one receipt for both paths');
}

async function checkLegacy(page, fixture) {
  await page.waitForFunction(() => Array.isArray(window.__canvasStore.getState().placedResultsBySession.s1));
  await page.waitForTimeout(1200);
  const state = await readState(page);
  assert.equal(state.nodes.length, 0, 'a legacy seed never paints history');
  assert(state.placed.includes(SINCE + '2026-10-19T01:00:00Z'), 'the legacy seed writes the watermark');
  await page.waitForFunction(() => (window.__canvasStore.getState().placedResultsBySession.s1 ?? [])
    .includes('old_task#old_task_asset'));
  const deadline = Date.now() + 5000;
  while (fixture.projects.project_1.saves.length === 0 && Date.now() < deadline) await page.waitForTimeout(100);
  assert(fixture.projects.project_1.saves.length > 0, 'the seeded receipt is saved with the canvas');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForTimeout(600);
  const reloaded = await readState(page);
  assert.equal(reloaded.nodes.length, 0, 'the legacy picture is still not replayed after reload');
}

async function checkSuppress(page) {
  await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
    .some((node) => node.imageUrl.endsWith('/api/v1/assets/normal')));
  await page.waitForTimeout(600);
  const state = await readState(page);
  assert.equal(state.nodes.length, 1, 'only the ordinary result lands');
  assert(!state.nodes.some((node) => node.imageUrl.endsWith('/inpaint')), 'the raw inpaint result never lands');
  assert(state.placed.includes('inpaint_task#inpaint_task_asset'), 'the raw inpaint receipt is still registered');
  assert(state.placed.includes('normal_task#normal_task_asset'), 'the landed picture is registered');
}

async function waitFor(check, message) {
  const deadline = Date.now() + 10_000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error(message || 'timed out');
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
}

function releaseAsset(fixture, id) {
  const asset = fixture.assets[id];
  asset.released = true;
  for (const resolve of asset.waiters.splice(0)) resolve();
}

async function checkStageRestored(page, fixture) {
  await page.waitForSelector('[data-testid="studio-stage"]');
  await waitLoaded(page);
  await page.waitForFunction(() => {
    const workspace = window.__workspaceStore.getState();
    return workspace.currentObject?.restored === true &&
      window.__canvasStore.getState().selectedNodeIds.length > 0;
  });
  await page.waitForTimeout(400);
  let state = await readState(page);
  assert.deepEqual(state.viewport, SEED_VIEWPORT, 'the restored object keeps the saved viewport');
  assert.equal(state.nodes.length, 1, 'the restored picture is on the board');

  // The same restoration happens on a reload: the saved viewport still wins over the opening fit.
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForFunction(() => {
    const workspace = window.__workspaceStore.getState();
    return workspace.currentObject?.restored === true &&
      window.__canvasStore.getState().selectedNodeIds.length > 0;
  });
  await page.waitForTimeout(400);
  state = await readState(page);
  assert.deepEqual(state.viewport, SEED_VIEWPORT, 'a reload keeps the restored viewport');

  // A layout change is a later run of the fit effect for the same restored object; the next one must
  // still remember it was restored instead of turning into an explicit fit.
  await page.setViewportSize({ width: 1200, height: 800 });
  await page.waitForTimeout(500);
  state = await readState(page);
  assert.deepEqual(state.viewport, SEED_VIEWPORT, 'a layout change keeps the restored view');
  await page.setViewportSize({ width: 1100, height: 780 });
  await page.waitForTimeout(500);
  state = await readState(page);
  assert.deepEqual(state.viewport, SEED_VIEWPORT, 'a later layout change still keeps the restored view');

  // Picking the same object themselves is explicit, so it fits normally.
  await page.evaluate(() => window.__canvasStore.getState().setViewport('s1', { zoom: 0.91, panX: -400, panY: 500 }));
  await page.evaluate(() => {
    const current = window.__workspaceStore.getState().currentObject;
    const { restored: _restored, ...object } = current;
    window.__workspaceStore.getState().setCurrentObject(object);
  });
  await page.waitForFunction(() => {
    const view = window.__canvasStore.getState().viewportBySession.s1;
    return Boolean(view && Math.abs(view.zoom - 0.91) > 0.001);
  });
  state = await readState(page);
  assert.notDeepEqual(state.viewport, { zoom: 0.91, panX: -400, panY: 500 }, 'picking the same object fits it');
}

async function checkStageQuietFirst(page, fixture) {
  await page.waitForSelector('[data-testid="studio-stage"]');
  await waitLoaded(page);
  // The opening restoration ran for this session with nothing to restore, so Stage marked it opened.
  await waitFor(() => fixture.pickReads > 0 && fixture.lineageReads > 0, 'the opening lineage/pick reads');
  await page.waitForTimeout(300);
  assert.equal(await page.evaluate(() => window.__workspaceStore.getState().currentObject), null,
    'the opening has no restored object');

  // The person works on an existing layer while the result is on its way.
  const seed = await page.evaluate(() => {
    window.__canvasStore.getState().selectNode('seed');
    return window.__canvasStore.getState().selectedNodeIds;
  });
  assert.deepEqual(seed, ['seed'], 'the existing layer is the selected one');

  // A first result arrives after the session was opened without an object: restored and quiet at once.
  fixture.lineage.nodes = [lineageTask('late_task', '/api/v1/assets/late', '2026-10-21T03:00:00Z')];
  await page.evaluate(() => window.__studioStore.getState().bumpVersions());
  await waitFor(() => fixture.assets.late.requests >= 1, 'the late picture load starts');
  await page.waitForFunction(() => {
    const object = window.__workspaceStore.getState().currentObject;
    return object?.taskId === 'late_task' && object.quiet === true && object.restored === true;
  });

  // While its picture is held the automatic pick must not have taken the selection or the view.
  assert.deepEqual(await page.evaluate(() => window.__canvasStore.getState().selectedNodeIds), ['seed'],
    'the held result leaves the selected layer alone');
  assert.deepEqual((await readState(page)).viewport, SEED_VIEWPORT, 'the held result leaves the viewport alone');

  releaseAsset(fixture, 'late');
  await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
    .some((node) => node.imageUrl.endsWith('/api/v1/assets/late')));
  await page.waitForTimeout(400);
  let state = await readState(page);
  assert.deepEqual(await page.evaluate(() => window.__canvasStore.getState().selectedNodeIds), ['seed'],
    'the quiet result never takes the selected layer');
  assert.deepEqual(state.viewport, SEED_VIEWPORT, 'the quiet result never moves the viewport');
  const lateId = await page.evaluate(() => window.__canvasStore.getState().nodesBySession.s1
    .find((node) => node.imageUrl.endsWith('/api/v1/assets/late')).id);

  // Picking that result themselves is explicit: it selects the layer and fits it.
  await page.evaluate(() => window.__canvasStore.getState().setViewport('s1', { zoom: 0.91, panX: -400, panY: 500 }));
  await page.evaluate(() => {
    const object = window.__workspaceStore.getState().currentObject;
    window.__workspaceStore.getState().setCurrentObject({ ...object, restored: false, quiet: false });
  });
  await page.waitForFunction((id) => window.__canvasStore.getState().selectedNodeIds.includes(id), lateId);
  await page.waitForFunction(() => {
    const view = window.__canvasStore.getState().viewportBySession.s1;
    return Boolean(view && Math.abs(view.zoom - 0.91) > 0.001);
  });
  state = await readState(page);
  assert.deepEqual(await page.evaluate(() => window.__canvasStore.getState().selectedNodeIds), [lateId],
    'the explicit pick selects the result');
  assert.notDeepEqual(state.viewport, { zoom: 0.91, panX: -400, panY: 500 }, 'the explicit pick fits the result');
}

async function checkStalePlan(page, fixture) {
  await page.waitForFunction(() => Array.isArray(window.__canvasStore.getState().placedResultsBySession.s1));
  await waitFor(() => fixture.assets.newer.requests === 1, 'the first plan loads the newer picture');
  assert.equal(fixture.assets.newer.requests, 1, 'the first plan loads the newer picture once');

  // The lineage now also has the older picture. Its effect starts while the newer load is still held.
  fixture.lineage.nodes = [
    lineageTask('older_task', '/api/v1/assets/older', '2026-10-21T01:00:00Z'),
    lineageTask('newer_task', '/api/v1/assets/newer', '2026-10-21T02:00:00Z'),
  ];
  await page.evaluate(() => window.__studioStore.getState().bumpVersions());
  await waitFor(() => fixture.assets.older.requests === 1, 'the changed lineage loads the older picture');

  // The first plan lands the newer picture and registers its receipt, then the person deletes it.
  releaseAsset(fixture, 'newer');
  await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
    .some((node) => node.imageUrl.endsWith('/api/v1/assets/newer')));
  await page.evaluate(() => {
    const node = window.__canvasStore.getState().nodesBySession.s1
      .find((item) => item.imageUrl.endsWith('/api/v1/assets/newer'));
    window.__canvasStore.getState().removeNode('s1', node.id);
  });
  const newerRequests = fixture.assets.newer.requests;

  // The older picture now finishes. The second plan still lists the newer picture from before its receipt.
  releaseAsset(fixture, 'older');
  await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
    .some((node) => node.imageUrl.endsWith('/api/v1/assets/older')));
  await page.waitForFunction(() => (window.__canvasStore.getState().placedResultsBySession.s1 ?? [])
    .includes('newer_task#newer_task_asset'));
  await page.waitForTimeout(600);

  let state = await readState(page);
  assert(!state.nodes.some((node) => node.imageUrl.endsWith('/api/v1/assets/newer')),
    'a stale plan does not re-add a picture the person deleted');
  assert(state.nodes.some((node) => node.imageUrl.endsWith('/api/v1/assets/older')), 'the older picture lands');
  assert(state.placed.includes('newer_task#newer_task_asset'), 'the newer receipt stays');

  const newerSaved = (save) => save.nodes.some((node) => String(node.data ?? '').includes('/api/v1/assets/newer'));
  const deadline = Date.now() + 5000;
  while (!fixture.projects.project_1.saves.some((save) =>
    Array.isArray(save.placedResults) && save.placedResults.includes('newer_task#newer_task_asset') && !newerSaved(save)) &&
    Date.now() < deadline) {
    await page.waitForTimeout(100);
  }
  const saved = fixture.projects.project_1.saves.filter((save) =>
    Array.isArray(save.placedResults) && save.placedResults.includes('newer_task#newer_task_asset'));
  assert(saved.length > 0 && !saved.some(newerSaved), 'the deletion and the receipt are saved together');
  assert.equal(fixture.assets.newer.requests, newerRequests, 'the stale plan does not load the deleted picture again');
  // Settled means no plan is still retrying: another debounce window adds no save.
  const attempts = fixture.projects.project_1.saveAttempts;
  await page.waitForTimeout(1600);
  assert.equal(fixture.projects.project_1.saveAttempts, attempts, 'the stale plan does not keep saving');

  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitLoaded(page);
  await page.waitForTimeout(800);
  state = await readState(page);
  assert(!state.nodes.some((node) => node.imageUrl.endsWith('/api/v1/assets/newer')),
    'a reload keeps the deleted picture out');
  assert(state.placed.includes('newer_task#newer_task_asset'), 'a reload keeps the newer receipt');
  assert.equal(fixture.assets.newer.requests, newerRequests, 'a reload does not retry the deleted picture');
}

async function waitForPending(page, fixture) {
  const deadline = Date.now() + 5000;
  while (!fixture.pending && Date.now() < deadline) await page.waitForTimeout(25);
  assert(fixture.pending, 'the automatic picture load is in flight');
}
