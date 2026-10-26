#!/usr/bin/env node
// Mounted, loopback-only browser contract for canvas inpaint provenance and the "back to content" view.
// The real CanvasBoard, CanvasLayersPanel, InpaintMaskDialog and InpaintDialog run against the Product
// Schema in Chromium and WebKit. Every HTTP response is a fixture in this process: no app server,
// provider, paid call or deployment is involved. The final integrated page still needs the normal
// dev-test acceptance.
//
//   node scripts/test-canvas-inpaint-contract.mjs
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
const offerings = catalog.offerings;
const text = (key) => {
  const value = schema.content.messages[key];
  assert.equal(typeof value, 'string', `missing schema message ${key}`);
  return value;
};
const gpt = offerings.find((item) => item.id === 'openai/gpt-image-2/image-to-image');
assert(gpt, 'missing GPT image-to-image offering');
const offering = { ...gpt, available: true };

const OUT = process.env.OUT || await mkdtemp(path.join(tmpdir(), 'canvas-inpaint-contract-'));
assert(path.isAbsolute(OUT), 'OUT must be absolute');
await mkdir(OUT, { recursive: true, mode: 0o700 });
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = [[1366, 768], [1920, 1080]];
const IDS = {
  sourceWide: 'asset_00000000000000000000000000000000',
  sourceSquare: 'asset_00000000000000000000000000000001',
  sourcePortrait: 'asset_00000000000000000000000000000002',
  localMask: 'asset_00000000000000000000000000000003',
  layerGood: 'asset_00000000000000000000000000000010',
  layerMissing: 'asset_00000000000000000000000000000011',
  layerCorrupt: 'asset_00000000000000000000000000000012',
  layerSlow: 'asset_00000000000000000000000000000013',
  layerOther: 'asset_00000000000000000000000000000014',
};

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
function png(width, height, kind) {
  const cacheKey = `${width}x${height}:${kind}`;
  if (pngCache.has(cacheKey)) return pngCache.get(cacheKey);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const stride = width * 4 + 1;
  const raw = Buffer.alloc(stride * height);
  const left = Math.floor(width / 4);
  const right = Math.floor(width * 3 / 4);
  const top = Math.floor(height / 3);
  const bottom = Math.floor(height * 2 / 3);
  for (let y = 0; y < height; y += 1) {
    const row = y * stride;
    raw[row] = 0;
    for (let x = 0; x < width; x += 1) {
      const offset = row + 1 + x * 4;
      if (kind === 'mask') {
        raw[offset + 3] = x >= left && x < right && y >= top && y < bottom ? 0 : 255;
      } else {
        const marked = x >= left && x < right && y >= top && y < bottom;
        raw[offset] = marked ? 220 : 40;
        raw[offset + 1] = marked ? 40 : 120;
        raw[offset + 2] = marked ? 40 : 220;
        // A patch layer is the result cut by the mask: transparent outside the marked region.
        raw[offset + 3] = kind === 'patch' && !marked ? 0 : 255;
      }
    }
  }
  const body = Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', deflateSync(raw, { level: 6 })),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
  pngCache.set(cacheKey, body);
  return body;
}
function sourcePng(width, height) { return png(width, height, 'source'); }
function maskPng(width, height) { return png(width, height, 'mask'); }
function patchPng(width, height) { return png(width, height, 'patch'); }

const JOB_MASK_ID = `asset_${'4'.repeat(32)}`;
const fixture = {
  failPrepare: false,
  failSaves: 0,
  delayTask: false,
  releaseTask: null,
  delayGrant: false,
  releaseGrant: null,
  approvedByPolicy: false,
  // The fixture can accept a task and then lose the response, like a network failure after commit.
  // acceptedTaskKeys holds the only task identities the fixture ever accepted.
  dropTaskResponse: false,
  acceptedTaskKeys: [],
  acceptedTaskCount: 0,
  taskKeys: [],
  prepareBodies: [],
  grantBodies: [],
  taskBodies: [],
  assetRequests: [],
  externalRequests: [],
};
const results = [];
let currentStep = 'setup';

const VIRTUAL_REQUEST = 'virtual:canvas-inpaint-contract';
const VIRTUAL_ID = '\0canvas-inpaint-contract';
const fixtureApp = `
  import React, { StrictMode, useEffect, useState } from 'react';
  import { createRoot } from 'react-dom/client';
  import '${path.join(root, 'src/app/globals.css').replaceAll('\\', '/')}';
  import { ProductSchemaProvider } from '@/components/product-schema-provider';
  import { CanvasBoard } from '@/components/canvas/canvas-board';
  import { CanvasLayersPanel } from '@/components/canvas/layers-panel';
  import { InpaintDialog } from '@/components/workspace/canvas-image-actions';
  import { WorkspaceCanvasBoard } from '@/components/workspace/workspace-canvas-board';
  import { saveInpaintJob, readInpaintJobs } from '@/lib/canvas/inpaint-layer';
  import { canvasArtboard } from '@/lib/canvas/artboard';
  import { initialArtboardViewport, visibleContentBounds } from '@/lib/canvas/viewport';
  import { stageFitInsets } from '@/lib/workspace/tool-rail-layout';
  import { useCanvasStore } from '@/store/canvas-store';
  import { useChatStore } from '@/store/chat-store';
  import { useMaskStore } from '@/store/mask-store';
  import { useStudioStore } from '@/store/studio-store';
  import { useWorkspaceStore } from '@/store/workspace-store';

  const schema = window.__ART_AI_PRODUCT_SCHEMA__;
  const fixture = window.__FIXTURE__;
  window.__fixtureCase = fixture.workspace?.name ?? '';
  const query = new URLSearchParams(location.search);
  const scenario = query.get('scenario') || 'layers';
  const size = fixture.size;
  const board = canvasArtboard(schema.canvas.frontend.artboard, size);
  const node = (id, x, y, width, height, extra = {}) => ({
    id, type: 'shape', shapeType: 'line', imageUrl: '', taskId: '',
    x, y, width, height, zIndex: 1, strokeWidth: 2, ...extra,
  });
  const image = (id, maskId, width, height, version = 1) => ({
    id, type: 'image', imageUrl: '/api/v1/assets/' + fixture.sourceId, taskId: 'seed',
    x: 0, y: 0, width, height, zIndex: version,
    ...(maskId ? { inpaint: { maskAssetId: maskId, width, height } } : {}),
  });
  const nodesBySession = scenario === 'workspace' ? { s1: [] } : scenario === 'layers' ? {
    s1: [
      image('good', fixture.ids.layerGood, 3840, 2160),
      image('plain', null, 100, 100, 2),
      image('missing', fixture.ids.layerMissing, 3840, 2160, 3),
      image('corrupt', fixture.ids.layerCorrupt, 3840, 2160, 4),
      image('slow', fixture.ids.layerSlow, 3840, 2160, 5),
    ],
    s2: [image('other', fixture.ids.layerOther, 2048, 2048)],
  } : scenario === 'board-fit' ? {
    s1: [
      node('left', -500, -200, 700, 0),
      node('right', 2500, 1800, 0, 500),
      node('rotated', 1200, 900, 400, 0, { rotation: 35 }),
      node('hidden', 9000, 9000, 100, 100, { visible: false }),
      node('transparent', 8000, 8000, 100, 100, { opacity: 0 }),
      { ...node('blank', 7000, 7000, 100, 100), type: 'image', imageUrl: '', blank: true, fillColor: 'transparent' },
    ],
  } : {
    s1: [{ ...image('source', null, size.width, size.height), x: board.x, y: board.y }],
  };
  const viewportBySession = scenario === 'board-fit' ? { s1: { zoom: 3, panX: -7000, panY: 9000 } } : {};
  useCanvasStore.setState({
    ...(scenario === 'workspace' ? {} : { nodesBySession }),
    viewportBySession,
    selectedNodeIds: [],
    canvasSizeBySession: { s1: size, s2: { width: 2048, height: 2048 } },
  });
  useChatStore.setState({ activeSessionId: 's1' });
  if (scenario === 'workspace') {
    useChatStore.getState().setSessions(fixture.workspace.sessions);
    useChatStore.getState().setActiveSession('s1');
    useWorkspaceStore.setState({
      modelByDomain: fixture.workspace.modelByDomain,
      trackedTasks: fixture.workspace.trackedTasks,
      currentObject: fixture.workspace.currentObject ?? null,
      paintToolActive: false,
    });
    if (fixture.workspace.job && !sessionStorage.getItem('workspace-fixture-job')) {
      saveInpaintJob({ ...fixture.workspace.job, sessionId: 's1' });
      sessionStorage.setItem('workspace-fixture-job', '1');
    }
  }
  const draft = scenario === 'board-mask' ? {
    sessionId: 's1',
    frame: board,
    strokes: [{
      points: [size.width * 0.25, size.height / 3, size.width * 0.75, size.height / 3, size.width * 0.75, size.height * 2 / 3, size.width * 0.25, size.height * 2 / 3],
      size: 0, region: true,
    }],
    redo: [], current: null,
  } : null;
  useMaskStore.setState({ draft, question: null });
  window.__canvasStore = useCanvasStore;
  window.__board = board;
  window.__inpaintJobs = () => readInpaintJobs('s1').map((job) => job.taskId);
  window.__maskStore = useMaskStore;
  window.__chatStore = useChatStore;
  window.__workspaceStore = useWorkspaceStore;
  window.__screen = {
    contentBounds: () => visibleContentBounds(useCanvasStore.getState().nodesBySession.s1 || [], schema.canvas),
    emptyView: () => {
      const area = document.querySelector('[data-canvas-drawing-area]');
      return initialArtboardViewport(board, { width: area.clientWidth, height: area.clientHeight }, schema.canvas.frontend.viewport, stageFitInsets(useStudioStore.getState().railExpanded));
    },
    insets: () => stageFitInsets(useStudioStore.getState().railExpanded),
  };

  const h = React.createElement;
  let sourceUploads = 0;
  function LayersScenario() {
    const [session, setSession] = useState('s1');
    useEffect(() => { window.__setSession = setSession; }, []);
    return h('div', { className: 'h-dvh w-72' }, h(CanvasLayersPanel, { sessionId: session }));
  }

  function BoardScenario() {
    const [request, setRequest] = useState(null);
    const [modelKey, setModelKey] = useState('a');
    const [artboard, setArtboard] = useState(board);
    useEffect(() => { window.__setBoardModel = setModelKey; }, []);
    useEffect(() => { window.__setBoardArtboard = setArtboard; }, []);
    window.__boardModel = modelKey;
    const offeringKey = query.get('offering');
    const baseOffering = offeringKey === 'unknown' ? fixture.unknownOffering
      : offeringKey === 'fixed' ? fixture.fixedOffering
        : offeringKey === 'ratio' ? fixture.ratioOffering : fixture.offering;
    const currentOffering = modelKey === 'b' ? fixture.modelBOffering : baseOffering;
    if (!window.__capturedUploads) window.__capturedUploads = [];
    const uploadBlob = async (blob, filename) => {
      const mask = filename.includes('mask');
      const assetId = mask ? fixture.ids.localMask
        : fixture.reuseSourceUrl ? fixture.sourceId : fixture.sourceId + '-upload-' + (++sourceUploads);
      const url = '/api/v1/assets/' + assetId;
      window.__capturedUploads.push({ blob, filename, url });
      if (!mask) fixture.sourceUploadUrls.push(url);
      if (fixture.delayUpload && filename === 'inpaint-mask.png') {
        fixture.delayUpload = false;
        await new Promise((resolve) => { window.__releaseUpload = resolve; });
      }
      return url;
    };
    const onInpaintRequest = (next) => {
      window.__captured = next;
      setRequest(next);
    };
    return h('div', { className: 'relative h-dvh w-full' },
      h(CanvasBoard, {
        sessionId: 's1',
        activeTool: scenario === 'board-mask' ? 'mask' : 'select',
        onToolChange: () => {},
        onUploadImage: () => {},
        onImageGenerator: () => {},
        onUploadBlob: uploadBlob,
        onInpaintRequest: onInpaintRequest,
        maskSupported: true,
        artboard,
        deliveryPixels: size,
        className: 'h-full w-full',
      }),
      request ? h(InpaintDialog, { request, offerings: [currentOffering], modelId: currentOffering.model_id, uploadBlob, onClose: () => setRequest(null) }) : null);
  }

  function WorkspaceScenario() {
    // Mirrors the real Stage box: the board is a flex child, so flex-1 gives it the viewport height.
    // A plain block wrapper leaves it zero-height and every screenshot blank.
    return h('div', { className: 'flex h-dvh w-full' },
      h('section', { className: '@container relative flex min-h-0 min-w-0 flex-1 flex-col bg-canvas' },
        h('div', { className: 'relative flex min-h-0 flex-1' }, h(WorkspaceCanvasBoard))));
  }

  function App() {
    return h(ProductSchemaProvider, { initialSchema: schema },
      scenario === 'workspace' ? h(WorkspaceScenario) : scenario === 'layers' ? h(LayersScenario) : h(BoardScenario));
  }
  createRoot(document.getElementById('root')).render(h(StrictMode, null, h(App)));
`;

const server = await createServer({
  configFile: false,
  root: path.join(root, 'web'),
  publicDir: path.join(root, 'public'),
  cacheDir: path.join(OUT, '.vite'),
  resolve: { alias: { '@': path.join(root, 'src') } },
  logLevel: 'error',
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{
    name: 'canvas-inpaint-contract-fixture',
    resolveId(id) { if (id === VIRTUAL_REQUEST) return VIRTUAL_ID; },
    load(id) { return id === VIRTUAL_ID ? fixtureApp : null; },
    transformIndexHtml(html) {
      return html.replace('{{.Schema}}', JSON.stringify(schema)).replace('src="/src/main.tsx"', `src="/@id/${VIRTUAL_REQUEST}"`);
    },
  }],
});
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, '');
const origin = new URL(base).origin;


const SINCE = 'since:';
function workspaceCase(name) {
  const size = sizeFor('wide');
  // The session-hop case switches boards while the inpaint result is still loading; a mismatched result
  // makes the finished job reach the failure path, which is where a stale flush used to save the new board.
  const rawInpaintSize = name === 'mismatch' || name === 'session-hop' || name === 'storage-fail-mismatch'
    ? { width: 2048, height: 1152 } : size;
  const asset = (id, url, mediaType = 'image/png') => ({ id, role: 'image', variant: '', mediaType, ordinal: 0, url, kind: 'image' });
  const rawInpaint = { taskId: 'inpaint_task', title: 'inpaint', capability: 'image-to-image', operation: 'inpaint', createdAt: '2026-10-21T01:48:56Z', asset: asset('raw_inpaint', '/api/v1/assets/raw_inpaint') };
  const retryMatch = {
    taskId: 'retry_match', title: 'retry match', capability: 'image-to-image', operation: 'inpaint',
    createdAt: '2026-10-21T01:50:56Z', asset: asset('retry_match_raw', '/api/v1/assets/retry_match_raw'),
  };
  const retryMismatch = {
    taskId: 'retry_mismatch', title: 'retry mismatch', capability: 'image-to-image', operation: 'inpaint',
    createdAt: '2026-10-21T01:51:56Z', asset: asset('retry_mismatch_raw', '/api/v1/assets/retry_mismatch_raw'),
  };
  const normal = { taskId: 'normal_task', title: 'normal', capability: 'text-to-image', operation: '',
    // The restore case puts the picture before the watermark: only the first-open restore can add it, so
    // the test cannot pass through the ordinary auto-join path instead.
    createdAt: name === 'restored-new' ? '2026-10-19T01:49:56Z' : '2026-10-21T01:49:56Z',
    asset: asset('raw_normal', '/api/v1/assets/raw_normal') };
  // A duplicated lineage entry must not add a second raw picture or a second registration.
  // A new canvas restoring its ordinary first picture has no inpaint lineage at all.
  const tasks = name === 'duplicate' ? [rawInpaint, { ...rawInpaint }, normal]
    : name === 'restored-new' ? [normal]
      : name === 'restored-raw-seed' ? [rawInpaint]
        : name === 'retry-operation' ? [retryMatch, retryMismatch, normal]
          : [rawInpaint, normal];
  const tracked = tasks.map((task) => ({
    taskId: task.taskId, sessionId: 's1', capability: task.capability, prompt: task.title,
    state: 'SUCCEEDED', progress: 1, progressMaximum: 1, terminal: true, failure: null, liveError: '',
    assets: [task.asset], downloadables: [task.asset], resources: [], resourcesPending: false, notFound: false, updatedAt: Date.now(),
  }));
  const receipt = [`${SINCE}2026-10-20T00:00:00Z`];
  const rawKey = 'inpaint_task#raw_inpaint';
  const placedResults = name === 'legacy' || name === 'restored-seed' || name === 'restored-raw-seed' ? null
    : name === 'restored-receipt' || name === 'already-landed' ? [...receipt, rawKey]
      : receipt;
  const project = {
    id: 'project_1', name: 'Canvas fixture', sessionId: 's1', userId: 'fixture-user',
    width: size.width, height: size.height, revision: 1, nodes: [],
    created: name === 'restored' || name === 'restored-receipt' || name === 'restored-new'
      || name === 'restored-seed' || name === 'restored-raw-seed',
    placedResults,
    failSaves: 0,
    // A deterministic window: every save is refused until this case clears it, so a late autosave
    // cannot turn the refusal into a success and make "the job survived" pass for the wrong reason.
    holdSaveFailures: name === 'save-fail',
    saveAttempts: 0,
    saveFailures: 0,
    saves: [],
  };
  if (name === 'already-landed') {
    // The patch was saved by an earlier mount, but the job survived (for example storage refused the removal).
    project.nodes.push({
      id: 'landed_patch', type: 'image', x: 600 - size.width / 2, y: 400 - size.height / 2,
      width: size.width, height: size.height, zIndex: 1,
      data: JSON.stringify({
        imageUrl: '/api/v1/assets/patch_asset', taskId: schema.canvas.frontend.upload_task_id,
        inpaint: { maskAssetId: JOB_MASK_ID, width: size.width, height: size.height },
      }),
      visible: true, locked: false,
    });
  }
  const job = name === 'legacy' || name === 'restored-receipt' || name === 'restored-new'
    || name === 'restored-seed' || name === 'restored-raw-seed' || name === 'retry-operation' ? null : {
    taskId: 'inpaint_task', sessionId: 's1', maskUrl: '/api/v1/assets/mask_job', maskAssetId: JOB_MASK_ID,
    frame: { x: 600 - size.width / 2, y: 400 - size.height / 2, width: size.width, height: size.height },
    size: { width: size.width, height: size.height },
  };
  const currentObject = name === 'restored' || name === 'restored-receipt' ? {
    taskId: 'inpaint_task', kind: 'image', url: '/api/v1/assets/raw_inpaint', assetId: 'raw_inpaint',
    sessionId: 's1', restored: true,
  } : name === 'restored-new' ? {
    taskId: 'normal_task', kind: 'image', url: '/api/v1/assets/raw_normal', assetId: 'raw_normal',
    sessionId: 's1', restored: true,
  } : null;
  const secondProject = {
    id: 'project_2', name: 'Second canvas', sessionId: 's2', userId: 'fixture-user',
    width: size.width, height: size.height, revision: 1, nodes: [], created: false,
    placedResults: [...receipt], failSaves: 0, holdSaveFailures: false,
    saveAttempts: 0, saveFailures: 0, saves: [],
  };
  const secondSession = {
    id: 's2', title: 'fixture two', createdAt: '2026-10-20T00:00:00Z',
    updatedAt: '2026-10-21T00:00:00Z', agentScope: 'auto',
  };
  const session = { id: 's1', title: 'fixture', createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-21T00:00:00Z', agentScope: 'auto' };
  return {
    name, size, rawInpaintSize, tasks, tracked, trackedTasks: tracked, project, job, currentObject,
    storageFault: name.startsWith('storage-fail'),
    projects: { project_1: project, project_2: secondProject },
    sessions: [session, secondSession],
    rawDelay: name === 'session-hop', rawPending: false, releaseRaw: null,
    uploadDelay: name === 'session-hop-success' || name === 'session-hop-return',
    uploadPending: false, releaseUpload: null, uploadAttempts: 0, projectReads: [],
    lineageDelay: name === 'delayed' ? 1200 : 0,
    session,
    modelByDomain: { image: offering.model_id, 'model-3d': null },
  };
}

function projectWire(project) {
  const now = '2026-10-21T00:00:00Z';
  return {
    id: project.id, userId: project.userId, name: project.name, sessionId: project.sessionId,
    viewport: '{"zoom":1,"panX":0,"panY":0}', bgStyle: 'dots',
    createdAt: now, updatedAt: now, revision: project.revision,
    nodes: project.nodes.map((node) => ({ ...node, projectId: project.id, createdAt: now, updatedAt: now })),
    placedResults: project.placedResults,
    canvasWidth: project.width, canvasHeight: project.height, coordinateVersion: 2,
    created: project.created,
  };
}

function lineageFor(tasks) {
  return {
    nodes: tasks.map((task) => ({
      id: task.taskId, task_id: task.taskId, kind: 'task', state: 'SUCCEEDED', capability: task.capability,
      operation: task.operation, created_at: task.createdAt,
      assets: [{ ...task.asset, media_type: task.asset.mediaType, ordinal: 0 }],
    })),
    edges: [], groups: [], next_cursor: '',
  };
}

const sizeFor = (key) => ({
  wide: { width: 3840, height: 2160 },
  square: { width: 2048, height: 2048 },
  portrait: { width: 2160, height: 3840 },
})[key];

const sourceIdFor = (sizeKey) => sizeKey === 'square' ? IDS.sourceSquare : sizeKey === 'portrait' ? IDS.sourcePortrait : IDS.sourceWide;

function fixtureFor(scenario, sizeKey = 'wide', caseName = '') {
  const size = sizeFor(sizeKey);
  const withoutSize = offering.parameters.filter((parameter) => parameter.semantic !== 'output-size');
  const outputSize = (extra) => ({
    name: 'size', label: 'Size', type: 'select', semantic: 'output-size',
    ...extra,
  });
  return {
    scenario,
    size,
    sourceId: sourceIdFor(sizeKey),
    ids: IDS,
    offering,
    modelBOffering: { ...offering, model_id: 'model-b', model_name: 'Model B' },
    unknownOffering: { ...offering, parameters: withoutSize },
    fixedOffering: {
      ...offering,
      parameters: [...withoutSize, outputSize({
        fixed: true, default: '2048x2048', options: [{ value: '2048x2048', label: '2048' }],
      })],
    },
    ratioOffering: {
      ...offering,
      parameters: [...withoutSize, outputSize({ options: [{ value: '16:9', label: '16:9' }] })],
    },
    delayUpload: caseName === 'cancel-upload',
    storageFault: caseName.startsWith('storage-fail'),
    sourceUploadUrls: [],
    // A negative case keeps the upload URL stable while the exported board bytes change.
    reuseSourceUrl: caseName === 'same-url-source',
  };
}

async function openPage(browser, viewport, scenario, sizeKey = 'wide', caseName = '', extraQuery = '') {
  const workspace = scenario === 'workspace' ? workspaceCase(caseName) : null;
  fixture.failPrepare = false;
  fixture.delayTask = caseName === 'cancel-task';
  fixture.releaseTask = null;
  fixture.delayGrant = caseName === 'cancel-grant' || caseName.startsWith('cancel-grant-');
  fixture.releaseGrant = null;
  fixture.approvedByPolicy = caseName === 'cancel-grant' || caseName === 'cancel-task'
    || caseName.startsWith('cancel-grant-') || caseName === 'drop-task-policy';
  fixture.dropTaskResponse = caseName === 'drop-task-manual' || caseName === 'drop-task-policy';
  fixture.acceptedTaskKeys.length = 0;
  fixture.acceptedTaskCount = 0;
  fixture.taskKeys.length = 0;
  fixture.prepareBodies.length = 0;
  fixture.grantBodies.length = 0;
  fixture.taskBodies.length = 0;
  fixture.uploadedAssets = new Map();
  const page = await browser.newPage({ viewport, serviceWorkers: 'block' });
  page.setDefaultTimeout(40_000);
  const errors = [];
  page.on('pageerror', (error) => errors.push(`pageerror:${error.stack || error.message}`));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(`console:${message.text()}`); });
  await page.addInitScript(({ initialSchema, fixture }) => {
    window.__ART_AI_PRODUCT_SCHEMA__ = initialSchema;
    window.__FIXTURE__ = fixture;
    window.__objectUrls = { created: 0, revoked: 0 };
    window.__abortedFetches = 0;
    if (fixture.storageFault) {
      // The page refuses inpaint job storage; the module's in-memory copy must still drive this page.
      const proto = Storage.prototype;
      const prefix = 'art-ai:inpaint-job:';
      const original = { set: proto.setItem, get: proto.getItem, key: proto.key, remove: proto.removeItem };
      proto.setItem = function (name, value) {
        if (String(name).startsWith(prefix)) throw new Error('storage denied');
        return original.set.call(this, name, value);
      };
      proto.getItem = function (name) {
        if (String(name).startsWith(prefix)) throw new Error('storage denied');
        return original.get.call(this, name);
      };
      proto.key = function () { throw new Error('storage denied'); };
      proto.removeItem = function (name) {
        if (String(name).startsWith(prefix)) throw new Error('storage denied');
        return original.remove.call(this, name);
      };
    }
    const create = URL.createObjectURL.bind(URL);
    const revoke = URL.revokeObjectURL.bind(URL);
    URL.createObjectURL = (blob) => { window.__objectUrls.created += 1; return create(blob); };
    URL.revokeObjectURL = (url) => { window.__objectUrls.revoked += 1; return revoke(url); };
    const originalFetch = window.fetch.bind(window);
    window.fetch = (input, init) => {
      if (init?.signal) init.signal.addEventListener('abort', () => { window.__abortedFetches += 1; });
      return originalFetch(input, init);
    };
  }, { initialSchema: schema, fixture: {
    ...fixtureFor(scenario, sizeKey, caseName),
    ...(workspace ? { workspace: { ...workspace, inpaintJob: workspace.job } } : {}),
  } });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) {
      fixture.externalRequests.push(url.href);
      await route.abort();
      return;
    }
    if (!url.pathname.startsWith('/api/')) {
      await route.continue();
      return;
    }
    if (url.pathname === schema.runtime.http.routes.product_schema) {
      await route.fulfill({ json: { schema } });
      return;
    }
    if (url.pathname === schema.runtime.http.legacy_routes.version) {
      // WebKit applies CORS to this fixture fetch; Chromium does not need it.
      await route.fulfill({ json: { version: schema.revision || 'fixture' },
        headers: { 'access-control-allow-origin': origin } });
      return;
    }
    if (url.pathname === schema.runtime.http.routes.client_errors) {
      await route.fulfill({ status: 204 });
      return;
    }
    if (workspace) {
      const projectIDOf = (template) => {
        const [before, after] = template.split('{projectID}');
        if (!url.pathname.startsWith(before) || !url.pathname.endsWith(after)) return '';
        const id = url.pathname.slice(before.length, url.pathname.length - after.length);
        return id && !id.includes('/') ? decodeURIComponent(id) : '';
      };
      const projectFor = (template) => workspace.projects[projectIDOf(template)];
      const lineageOf = (sessionID) => schema.runtime.http.routes.chat_session_lineage.replace('{sessionID}', sessionID);
      const agentScopeOf = (sessionID) => schema.runtime.http.routes.chat_session_agent_scope.replace('{sessionID}', sessionID);
      const threeViewsOf = (sessionID) => schema.runtime.http.routes.chat_session_three_views.replace('{sessionID}', sessionID);
      if (url.pathname === schema.runtime.http.legacy_routes.canvas_projects && request.method() === 'POST') {
        const body = JSON.parse(request.postData() || '{}');
        const created = workspace.projects[body[schema.canvas.api.fields.session_id] === 's2' ? 'project_2' : 'project_1'];
        await route.fulfill({ json: projectWire(created) });
        return;
      }
      if (url.pathname === schema.runtime.http.legacy_routes.canvas_projects && request.method() === 'GET') {
        await route.fulfill({ json: [] });
        return;
      }
      const readProject = projectFor(schema.runtime.http.legacy_routes.canvas_project);
      if (readProject && request.method() === 'GET') {
        workspace.projectReads.push(readProject.id);
        await route.fulfill({ json: projectWire(readProject) });
        return;
      }
      const writeProject = projectFor(schema.runtime.http.legacy_routes.canvas_nodes);
      if (writeProject && request.method() === 'PUT') {
        const body = JSON.parse(request.postData() || '{}');
        const fields = schema.canvas.api.fields;
        writeProject.saveAttempts += 1;
        if (writeProject.holdSaveFailures || writeProject.failSaves > 0) {
          if (!writeProject.holdSaveFailures) writeProject.failSaves -= 1;
          writeProject.saveFailures += 1;
          await route.fulfill({ status: 500, json: { detail: 'fixture save failure' } });
          return;
        }
        writeProject.nodes = (body[fields.nodes] ?? []).map((node) => ({ ...node }));
        if (Array.isArray(body[fields.placed_results])) writeProject.placedResults = body[fields.placed_results];
        if (typeof body[fields.canvas_width] === 'number') writeProject.width = body[fields.canvas_width];
        if (typeof body[fields.canvas_height] === 'number') writeProject.height = body[fields.canvas_height];
        writeProject.revision += 1;
        writeProject.saves.push({
          nodes: writeProject.nodes.map((node) => node[fields.id]),
          placedResults: writeProject.placedResults,
        });
        await route.fulfill({ json: { ok: true, count: writeProject.nodes.length } });
        return;
      }
      if (url.pathname === lineageOf('s1') && request.method() === 'GET') {
        if (workspace.lineageDelay) await new Promise((resolve) => setTimeout(resolve, workspace.lineageDelay));
        await route.fulfill({ json: lineageFor(workspace.tasks) });
        return;
      }
      if (url.pathname === lineageOf('s2') && request.method() === 'GET') {
        await route.fulfill({ json: { nodes: [], edges: [], groups: [], next_cursor: '' } });
        return;
      }
      if ((url.pathname === agentScopeOf('s1') || url.pathname === agentScopeOf('s2')) && request.method() === 'GET') {
        await route.fulfill({ json: { agent_scope: 'auto' } });
        return;
      }
      if ((url.pathname === threeViewsOf('s1') || url.pathname === threeViewsOf('s2')) && request.method() === 'GET') {
        await route.fulfill({ json: { items: [] } });
        return;
      }
      if (url.pathname === schema.runtime.http.routes.agent_params && request.method() === 'GET') {
        await route.fulfill({ json: { params: {}, parameter_states: {} } });
        return;
      }
      if (url.pathname === schema.runtime.http.routes.offerings && request.method() === 'GET') {
        const capability = url.searchParams.get('capability');
        await route.fulfill({ json: { offerings: capability === 'image-to-image' ? [offering] : [] } });
        return;
      }
      if (url.pathname === schema.runtime.http.routes.assets && request.method() === 'POST') {
        workspace.uploadAttempts += 1;
        if (workspace.uploadDelay) {
          workspace.uploadDelay = false;
          workspace.uploadPending = true;
          await new Promise((resolve) => { workspace.releaseUpload = resolve; });
        }
        // Serve the uploaded patch bytes when the engine exposes them. WebKit does not give the route a
        // body buffer, so GET falls back to an equivalent clipped fixture picture below.
        const body = request.postDataBuffer();
        const boundary = /boundary=(?:"([^"]+)"|([^;]+))/.exec(request.headers()['content-type'] || '');
        const marker = boundary ? `--${boundary[1] || boundary[2]}` : '';
        const start = marker && body ? body.indexOf('\r\n\r\n') + 4 : -1;
        const end = marker && body ? body.indexOf(`\r\n${marker}`, start) : -1;
        if (start > 3 && end > start) fixture.uploadedAssets.set('patch_asset', body.subarray(start, end));
        await route.fulfill({ json: { id: 'patch_asset' } });
        return;
      }
    }
    const assetPrefix = schema.runtime.http.routes.asset_item.replace('{assetID}', '');
    if (url.pathname.startsWith(assetPrefix) && request.method() === 'GET') {
      const id = decodeURIComponent(url.pathname.slice(assetPrefix.length));
      fixture.assetRequests.push(id);
      if (id === IDS.layerMissing) {
        await route.fulfill({ status: 404, body: 'missing' });
        return;
      }
      if (id === IDS.layerCorrupt) {
        await route.fulfill({ status: 200, contentType: 'text/plain', body: 'not an image' });
        return;
      }
      if (id === IDS.layerSlow) await new Promise((resolve) => setTimeout(resolve, 900));
      if (id === IDS.layerGood || id === IDS.layerSlow) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: maskPng(3840, 2160) });
        return;
      }
      if (id === IDS.layerOther) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: maskPng(2048, 2048) });
        return;
      }
      if (id === 'raw_inpaint' && workspace) {
        if (workspace.rawDelay) {
          workspace.rawDelay = false;
          workspace.rawPending = true;
          await new Promise((resolve) => { workspace.releaseRaw = resolve; });
        }
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(workspace.rawInpaintSize.width, workspace.rawInpaintSize.height) });
        return;
      }
      if (id === 'retry_match_raw' && workspace) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(workspace.size.width, workspace.size.height) });
        return;
      }
      if (id === 'retry_mismatch_raw' && workspace) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(2048, 1152) });
        return;
      }
      if (id === 'patch_asset' && workspace) {
        // Uploaded bytes when available; otherwise the same masked fixture picture the upload produces.
        const uploaded = fixture.uploadedAssets.get(id);
        await route.fulfill({ status: 200, contentType: 'image/png',
          body: uploaded ?? patchPng(workspace.size.width, workspace.size.height) });
        return;
      }
      if (id === 'raw_normal' && workspace) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(workspace.size.width, workspace.size.height) });
        return;
      }
      if (id === 'mask_job' && workspace) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: maskPng(workspace.size.width, workspace.size.height) });
        return;
      }
      // Every reopen re-uploads the board under a fresh asset id; its bytes stay the same picture.
      if (id === IDS.sourceWide || id.startsWith(`${IDS.sourceWide}-upload-`)) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(3840, 2160) });
        return;
      }
      if (id === IDS.sourceSquare || id.startsWith(`${IDS.sourceSquare}-upload-`)) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(2048, 2048) });
        return;
      }
      if (id === IDS.sourcePortrait || id.startsWith(`${IDS.sourcePortrait}-upload-`)) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: sourcePng(2160, 3840) });
        return;
      }
      await route.fulfill({ status: 404, body: 'unknown fixture asset' });
      return;
    }
    if (url.pathname === schema.runtime.http.routes.task_prepare && request.method() === 'POST') {
      const body = JSON.parse(request.postData() || '{}');
      fixture.prepareBodies.push(body);
      if (fixture.failPrepare) {
        await route.fulfill({ status: 500, json: { detail: 'fixture preparation failure' } });
        return;
      }
      await route.fulfill({ json: { prepared_input_id: 'prepared_fixture' } });
      return;
    }
    if (url.pathname === schema.runtime.http.routes.task_grants && request.method() === 'POST') {
      fixture.grantBodies.push(JSON.parse(request.postData() || '{}'));
      if (fixture.delayGrant) {
        fixture.delayGrant = false;
        await new Promise((resolve) => { fixture.releaseGrant = resolve; });
      }
      await route.fulfill({ json: {
        request_id: 'request_fixture',
        status: fixture.approvedByPolicy ? 'approved' : 'pending',
        ...(fixture.approvedByPolicy ? { approved_by_policy: true } : {}),
      } });
      return;
    }
    if (url.pathname === schema.runtime.http.routes.task_grant_decision.replace('{requestID}', 'request_fixture') && request.method() === 'PUT') {
      await route.fulfill({ json: { request_id: 'request_fixture', status: 'approved' } });
      return;
    }
    if (url.pathname === schema.runtime.http.routes.tasks && request.method() === 'POST') {
      fixture.taskBodies.push(JSON.parse(request.postData() || '{}'));
      const key = request.headers()['idempotency-key'] || '';
      fixture.taskKeys.push(key);
      if (fixture.delayTask) {
        fixture.delayTask = false;
        await new Promise((resolve) => { fixture.releaseTask = resolve; });
      }
      if (fixture.dropTaskResponse) {
        // Commit the task, then lose the response. The page sees a network failure while the task exists;
        // a later POST with the same key must replay it instead of creating a second task.
        fixture.dropTaskResponse = false;
        fixture.acceptedTaskKeys.push(key);
        fixture.acceptedTaskCount += 1;
        await route.abort('failed');
        return;
      }
      if (!fixture.acceptedTaskKeys.includes(key)) {
        fixture.acceptedTaskKeys.push(key);
        fixture.acceptedTaskCount += 1;
      }
      await route.fulfill({ json: { task_id: 'task_fixture' } });
      return;
    }
    if (url.pathname === '/api/__test/release-task') {
      fixture.releaseTask?.();
      fixture.releaseTask = null;
      await route.fulfill({ status: 204 });
      return;
    }
    if (url.pathname === '/api/__test/release-grant') {
      fixture.releaseGrant?.();
      fixture.releaseGrant = null;
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({ status: 404, json: {} });
  });
  await page.goto(`${base}/?scenario=${scenario}&case=${encodeURIComponent(caseName)}${extraQuery}`, { waitUntil: 'domcontentloaded' });
  return { page, errors, workspace };
}


async function workspaceContract(browser, engine, viewport, caseName) {
  const { page, errors, workspace } = await openPage(browser, viewport, 'workspace', 'wide', caseName);
  try {
    await page.getByTestId('canvas-zoom-controls').waitFor();
  } catch (error) {
    console.error('workspace debug', { caseName, errors, text: await page.locator('body').innerText().catch(() => '') });
    throw error;
  }
  const read = async () => ({
    ...(await page.evaluate(() => ({
      nodes: (window.__canvasStore.getState().nodesBySession.s1 ?? []).map((node) => ({
        id: node.id, taskId: node.taskId, imageUrl: node.imageUrl, name: node.name,
        maskAssetId: node.inpaint?.maskAssetId ?? null,
      })),
      placed: window.__canvasStore.getState().placedResultsBySession.s1,
      jobs: typeof window.__inpaintJobs === 'function' ? window.__inpaintJobs() : Object.keys(sessionStorage).filter((key) => key.startsWith('art-ai:inpaint-job:')),
    }))),
    saves: workspace.project.saves.slice(),
  });
  const waitStable = async (ms = 1000) => {
    await page.waitForTimeout(ms);
    return read();
  };
  const noRawInpaint = (state) => !state.nodes.some((node) => node.taskId === 'inpaint_task' || node.imageUrl.includes('raw_inpaint'));
  const patchNodes = (state) => state.nodes.filter((node) => node.taskId === schema.canvas.frontend.upload_task_id);
  const receiptRaw = (state) => (state.placed ?? []).filter((key) => key === 'inpaint_task#raw_inpaint');
  const waitNodes = async (count) => {
    await page.waitForFunction((expected) => (window.__canvasStore.getState().nodesBySession.s1 ?? []).length >= expected, count);
  };
  const waitPlaced = async () => {
    await page.waitForFunction(() => Array.isArray(window.__canvasStore.getState().placedResultsBySession.s1));
  };
  // The autosave debounce is 1.5s; poll the fixture's accepted writes instead of guessing.
  const waitSaved = async (predicate, timeout = 5000) => {
    const deadline = Date.now() + timeout;
    while (!predicate(workspace.project) && Date.now() < deadline) await page.waitForTimeout(100);
    return predicate(workspace.project);
  };
  // The Stage box in the real app gives the board a nonzero flex height. Check the real drawing area
  // and its Konva canvas first, so a blank surface cannot make the store assertions below pass.
  await page.waitForFunction(() => {
    const area = document.querySelector('[data-canvas-drawing-area]');
    return Boolean(area && area.clientWidth > 0 && area.clientHeight > 0 &&
      [...area.querySelectorAll('canvas')].some((canvas) => canvas.width > 0 && canvas.height > 0));
  }, null, { timeout: 5000 });
  const surface = await page.evaluate(() => {
    const area = document.querySelector('[data-canvas-drawing-area]');
    if (!area) return null;
    const rect = area.getBoundingClientRect();
    return {
      width: area.clientWidth,
      height: area.clientHeight,
      rectWidth: rect.width,
      rectHeight: rect.height,
      canvases: [...area.querySelectorAll('canvas')].map((canvas) => ({ width: canvas.width, height: canvas.height })),
    };
  });
  assert(surface, 'the workspace board renders a drawing area');
  assert(surface.width > 0 && surface.height > 0, 'the drawing area has a nonzero visible size');
  assert(surface.rectWidth > 0 && surface.rectHeight > 0, 'the drawing area has a nonzero box');
  assert(surface.canvases.some((canvas) => canvas.width > 0 && canvas.height > 0), 'the board paints on a sized canvas');
  // The patch sits on the board as a Konva image. Read the real drawing canvases instead of store state:
  // remove the ordinary result layer first, so only the patch's red centre can satisfy the check, then
  // count result-coloured pixels on the rendered surface. This works the same in Chromium and WebKit.
  const paintedPatch = async () => {
    await page.evaluate(() => {
      const taskId = window.__ART_AI_PRODUCT_SCHEMA__.canvas.frontend.upload_task_id;
      for (const node of window.__canvasStore.getState().nodesBySession.s1 ?? []) {
        if (node.taskId !== taskId) window.__canvasStore.getState().removeNode('s1', node.id);
      }
    });
    await page.waitForFunction(() => (window.__canvasStore.getState().nodesBySession.s1 ?? [])
      .every((node) => node.taskId === window.__ART_AI_PRODUCT_SCHEMA__.canvas.frontend.upload_task_id));
    const deadline = Date.now() + 5000;
    let painted = { canvases: 0, red: 0 };
    while (Date.now() < deadline) {
      painted = await page.evaluate(() => {
        const canvases = [...document.querySelectorAll('[data-canvas-drawing-area] canvas')];
        let red = 0;
        for (const canvas of canvases) {
          const data = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data;
          for (let index = 0; index < data.length; index += 4) {
            if (data[index] > 190 && data[index + 1] < 90 && data[index + 2] < 90 && data[index + 3] > 200) red += 1;
          }
        }
        return { canvases: canvases.length, red };
      });
      if (painted.red > 0) break;
      await page.waitForTimeout(50);
    }
    return painted;
  };
  if (caseName === 'legacy') {
    let state = await waitStable();
    assert(Array.isArray(state.placed), 'legacy canvas is seeded');
    assert.equal(state.nodes.length, 0, 'legacy seed never paints history');
    assert(state.placed.some((key) => key.includes('inpaint_task#raw_inpaint')));
    assert(state.placed.some((key) => key.includes('normal_task#raw_normal')));
  } else if (caseName === 'mismatch' || caseName === 'storage-fail-mismatch') {
    await waitNodes(1);
    let state = await waitStable();
    assert.equal(state.nodes.length, 1, 'mismatch leaves only the normal result');
    assert.equal(state.nodes[0].taskId, 'normal_task');
    assert.equal(patchNodes(state).length, 0, 'mismatch lands no patch');
    assert(noRawInpaint(state), 'mismatch never auto-adds the raw inpaint');
    assert.equal(state.jobs.length, 0, 'the rejected job is removed');
    assert.deepEqual(receiptRaw(state), ['inpaint_task#raw_inpaint']);
    const beforeReload = state.nodes.map((node) => node.id).sort();
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(1);
    state = await waitStable();
    assert.equal(state.nodes.map((node) => node.id).sort().join(','), beforeReload.join(','), 'reload keeps the normal layer only');
    assert.equal(patchNodes(state).length, 0);
    assert(noRawInpaint(state));
  } else if (caseName === 'save-fail') {
    await waitNodes(2);
    let state = await waitStable(1500);
    assert.equal(patchNodes(state).length, 1, 'the patch is on the board');
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1);
    assert(noRawInpaint(state), 'the raw inpaint never joins automatically');
    assert.equal(state.jobs.length, 1, 'a failed save keeps the job');
    assert(await waitSaved((project) => project.saveFailures > 0), 'the server refused at least one save');
    assert.equal(workspace.project.saves.length, 0, 'nothing was saved while the server refused');
    workspace.project.holdSaveFailures = false;
    workspace.project.failSaves = 0;
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(2);
    state = await waitStable(1200);
    assert.equal(patchNodes(state).length, 1, 'the retried patch lands exactly once');
    assert(noRawInpaint(state), 'the retried job still suppresses the raw result');
    assert.equal(state.jobs.length, 0, 'the receipt is durable before the job is dropped');
    assert(await waitSaved((project) => project.saves.length > 0), 'the retried save reaches the server');
    assert.deepEqual(receiptRaw(state), ['inpaint_task#raw_inpaint']);
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(2);
    state = await waitStable();
    assert.equal(patchNodes(state).length, 1, 'a later reload does not duplicate the patch');
    assert(noRawInpaint(state));
  } else if (caseName === 'already-landed') {
    await waitNodes(2);
    let state = await waitStable();
    assert.equal(patchNodes(state).length, 1, 'the saved patch is not landed again');
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1);
    assert(noRawInpaint(state));
    assert.equal(state.jobs.length, 0, 'the stale job is dropped once its patch is proven');
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(2);
    state = await waitStable();
    assert.equal(patchNodes(state).length, 1);
    assert(noRawInpaint(state));
  } else if (caseName === 'restored-seed') {
    // The null receipt is seeded by the lineage before Stage restores the selection: the ordinary picture
    // must still be the first layer, not lost because its key was just registered.
    await waitPlaced();
    await page.evaluate(() => {
      window.__workspaceStore.getState().setCurrentObject({
        taskId: 'normal_task', kind: 'image', url: '/api/v1/assets/raw_normal', assetId: 'raw_normal',
        sessionId: 's1', restored: true,
      });
    });
    await waitNodes(1);
    let state = await waitStable(1200);
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1,
      'the delayed restored picture still lands after the receipt was seeded');
    assert(noRawInpaint(state));
    assert(state.placed.includes('normal_task#raw_normal'));
    assert.equal(state.jobs.length, 0);
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(1);
    state = await waitStable();
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1, 'a reload does not duplicate it');
  } else if (caseName === 'restored-raw-seed') {
    // The same race for an inpaint result: the server operation, not the just-seeded receipt, refuses it.
    await waitPlaced();
    await page.evaluate(() => {
      window.__workspaceStore.getState().setCurrentObject({
        taskId: 'inpaint_task', kind: 'image', url: '/api/v1/assets/raw_inpaint', assetId: 'raw_inpaint',
        sessionId: 's1', restored: true,
      });
    });
    await page.waitForTimeout(1200);
    const state = await read();
    assert.equal(state.nodes.length, 0, 'an inpaint raw result is never the first-open picture');
    assert(state.placed.includes('inpaint_task#raw_inpaint'));
    assert.equal(state.jobs.length, 0);
  } else if (caseName === 'restored-receipt') {
    await waitNodes(1);
    let state = await waitStable();
    assert.equal(patchNodes(state).length, 0, 'a receipt without a job never restores the raw picture');
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1);
    assert(noRawInpaint(state), 'the saved receipt suppresses the first-open restore');
    assert.equal(state.jobs.length, 0);
  } else if (caseName === 'restored-new') {
    // #302 guard: only a registered receipt suppresses the restore. A brand-new canvas with an ordinary
    // (non-inpaint) restored picture and no receipt for it must still restore it once.
    await waitNodes(1);
    let state = await waitStable();
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1, 'an ordinary first picture is restored');
    assert(noRawInpaint(state));
    assert.equal(state.jobs.length, 0);
    assert(state.placed.includes('normal_task#raw_normal'), 'the restored picture is registered once');
    assert(await waitSaved((project) => project.saves.some((save) => save.placedResults.includes('normal_task#raw_normal'))),
      'the restore receipt is saved with the canvas');
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(1);
    state = await waitStable();
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1, 'a reload does not duplicate it');
  } else if (caseName === 'session-hop') {
    // Hold the inpaint result in flight, show another session, then let it fail. The finished job must stay
    // with its own session: flushing the visible board would report a receipt as durable that was never saved.
    const pendingDeadline = Date.now() + 5000;
    while (!workspace.rawPending && Date.now() < pendingDeadline) await page.waitForTimeout(25);
    assert(workspace.rawPending, 'the inpaint result request is in flight');
    await page.evaluate(() => window.__chatStore.getState().setActiveSession('s2'));
    await page.waitForFunction(() => window.__chatStore.getState().activeSessionId === 's2');
    // Wait until the second board has a loaded project and a save function of its own.
    await page.waitForFunction(() => window.__canvasStore.getState().projectBySession.s2 === 'project_2' &&
      Boolean(window.__canvasStore.getState().canvasSizeBySession.s2));
    await page.waitForTimeout(300);
    workspace.releaseRaw?.();
    workspace.releaseRaw = null;
    await page.waitForTimeout(800);
    const keptJobs = await page.evaluate(() => Object.keys(sessionStorage).filter((key) => key.startsWith('art-ai:inpaint-job:')));
    assert.equal(keptJobs.length, 1, 'the job stays with its own session instead of the visible one');
    assert.equal(workspace.projects.project_2.saves.every((save) => !save.placedResults.includes('inpaint_task#raw_inpaint')),
      true, 'the hidden canvas receipt is never written to the visible one');
    // Returning to its own session finishes the job against the right board.
    await page.evaluate(() => window.__chatStore.getState().setActiveSession('s1'));
    await page.getByTestId('canvas-zoom-controls').waitFor();
    await page.waitForFunction(() => !Object.keys(sessionStorage).some((key) => key.startsWith('art-ai:inpaint-job:')));
    const receiptSaved = await waitSaved((project) => project.saves.some((save) => save.placedResults.includes('inpaint_task#raw_inpaint')));
    assert(receiptSaved, 'the retried job persists its receipt on its own canvas');
    const onOwnBoard = await waitStable();
    assert(noRawInpaint(onOwnBoard), 'the raw result never joins the board');
  } else if (caseName === 'session-hop-success') {
    // The matching-size result renders and uploads, but the board switches while the upload is in flight.
    // The stale scope check drops that attempt; returning to its session must retry it instead of keeping
    // the task id marked as handled forever.
    const uploadDeadline = Date.now() + 5000;
    while (!workspace.uploadPending && Date.now() < uploadDeadline) await page.waitForTimeout(25);
    assert(workspace.uploadPending, 'the patch upload is in flight');
    await page.evaluate(() => window.__chatStore.getState().setActiveSession('s2'));
    await page.waitForFunction(() => window.__chatStore.getState().activeSessionId === 's2');
    await page.waitForFunction(() => window.__canvasStore.getState().projectBySession.s2 === 'project_2' &&
      Boolean(window.__canvasStore.getState().canvasSizeBySession.s2));
    await page.waitForTimeout(300);
    workspace.releaseUpload?.();
    workspace.releaseUpload = null;
    await page.waitForTimeout(800);
    const hiddenJobs = await page.evaluate(() => Object.keys(sessionStorage).filter((key) => key.startsWith('art-ai:inpaint-job:')));
    assert.equal(hiddenJobs.length, 1, 'the interrupted job stays with its own session');
    assert.equal(workspace.projects.project_2.saves.every((save) => !save.placedResults.includes('inpaint_task#raw_inpaint')),
      true, 'the hidden canvas receipt is never written to the visible one');
    await page.evaluate(() => window.__chatStore.getState().setActiveSession('s1'));
    await page.waitForFunction(() => window.__canvasStore.getState().projectBySession.s1 === 'project_1');
    await page.waitForFunction(() => !Object.keys(sessionStorage).some((key) => key.startsWith('art-ai:inpaint-job:')),
      null, { timeout: 5000 });
    const retriedReceipt = await waitSaved((project) => project.saves.some((save) => save.placedResults.includes('inpaint_task#raw_inpaint')));
    assert(retriedReceipt, 'the retried attempt persists its receipt on its own canvas');
    const retried = await waitStable();
    assert.equal(patchNodes(retried).length, 1, 'the retried attempt lands exactly one patch');
    assert(noRawInpaint(retried), 'the raw result still never joins the board');
  } else if (caseName === 'session-hop-return') {
    // A -> B -> A while the first patch upload is still held. The returning board loads first; the stale
    // attempt ends second, when its own session is already active again. Dropping the handled marker alone
    // does not re-run the effect, so the job would stay stranded until another switch.
    const uploadDeadline = Date.now() + 5000;
    while (!workspace.uploadPending && Date.now() < uploadDeadline) await page.waitForTimeout(25);
    assert(workspace.uploadPending, 'the patch upload is in flight');
    assert((await read()).jobs.includes('inpaint_task'), 'the job is remembered while its upload is in flight');
    await page.evaluate(() => window.__chatStore.getState().setActiveSession('s2'));
    await page.waitForFunction(() => window.__chatStore.getState().activeSessionId === 's2');
    await page.waitForFunction(() => window.__canvasStore.getState().projectBySession.s2 === 'project_2' &&
      Boolean(window.__canvasStore.getState().canvasSizeBySession.s2));
    const ownerReadsBeforeReturn = workspace.projectReads.filter((id) => id === 'project_1').length;
    await page.evaluate(() => window.__chatStore.getState().setActiveSession('s1'));
    // A new read of the owner project means its load settled before the stale upload is released.
    assert(await waitFor(() => workspace.projectReads.filter((id) => id === 'project_1').length > ownerReadsBeforeReturn, 5000),
      'the owner board is loaded again while the old upload is still held');
    await page.getByTestId('canvas-zoom-controls').waitFor();
    const returned = await read();
    assert.equal(patchNodes(returned).length, 0, 'the returning board has no patch before the stale attempt ends');
    assert.equal(workspace.projects.project_2.saves.every((save) => !save.placedResults.includes('inpaint_task#raw_inpaint')),
      true, 'the visible other canvas never receives the hidden receipt');
    workspace.releaseUpload?.();
    workspace.releaseUpload = null;
    await page.waitForFunction(() => !Object.keys(sessionStorage).some((key) => key.startsWith('art-ai:inpaint-job:')),
      null, { timeout: 5000 });
    const retried = await waitStable(300);
    assert.equal(patchNodes(retried).length, 1, 'the stale attempt wakes one fresh attempt that lands exactly one patch');
    assert(noRawInpaint(retried), 'the raw result never joins the board');
    assert.equal(retried.jobs.length, 0, 'the job is dropped only after its receipt is durable');
    assert.equal(workspace.uploadAttempts, 2, 'one stale upload and one retry; the wake makes no retry loop');
    assert.deepEqual(receiptRaw(retried), ['inpaint_task#raw_inpaint']);
    assert(await waitSaved((project) => project.saves.some((save) => save.placedResults.includes('inpaint_task#raw_inpaint'))),
      'the receipt is saved on the owner canvas');
    const painted = await paintedPatch();
    assert(painted.canvases > 0, 'the board still paints after removing the ordinary layer');
    assert(painted.red > 0, 'the landed patch draws result-coloured pixels on the visible canvas');
  } else if (caseName === 'retry-operation') {
    // Retried inpaint tasks have no local job, but the server keeps their frozen operation. Neither a
    // matching-size nor a mismatched raw result may join automatically; an explicit choice still may.
    await waitNodes(1);
    let state = await waitStable(1500);
    assert.equal(state.nodes.length, 1, 'no retried raw result joins automatically');
    assert.equal(state.nodes[0].taskId, 'normal_task');
    assert.equal(patchNodes(state).length, 0, 'a retry without a local frame makes no patch');
    assert(state.placed.includes('retry_match#retry_match_raw'));
    assert(state.placed.includes('retry_mismatch#retry_mismatch_raw'));
    assert.equal(state.jobs.length, 0);
    await page.evaluate(() => {
      window.__workspaceStore.getState().setCurrentObject({
        taskId: 'retry_match', kind: 'image', url: '/api/v1/assets/retry_match_raw', assetId: 'retry_match_raw',
        sessionId: 's1', quiet: true,
      });
    });
    const quiet = await waitStable(600);
    assert.equal(quiet.nodes.length, 1, 'a quiet retried raw result stays off the board');
    await page.evaluate(() => {
      window.__workspaceStore.getState().setCurrentObject({
        taskId: 'retry_match', kind: 'image', url: '/api/v1/assets/retry_match_raw', assetId: 'retry_match_raw',
        sessionId: 's1',
      });
    });
    await waitNodes(2);
    state = await waitStable(800);
    assert.equal(state.nodes.filter((node) => node.taskId === 'retry_match').length, 1,
      'an explicit task-centre choice can still place the raw result');
  } else {
    const expectedNodes = caseName === 'delayed' || caseName === 'duplicate' || caseName === 'already-landed' ? 2 : 2;
    await waitNodes(expectedNodes);
    let state = await waitStable(caseName === 'delayed' ? 2500 : 1000);
    assert(noRawInpaint(state), `${caseName} never auto-adds the raw inpaint`);
    assert.equal(patchNodes(state).length, 1, `${caseName} lands exactly one patch`);
    assert.equal(state.nodes.filter((node) => node.taskId === 'normal_task').length, 1);
    assert.deepEqual(receiptRaw(state), ['inpaint_task#raw_inpaint'], `${caseName} registers the raw result once`);
    assert.equal(state.jobs.length, 0);
    const saved = workspace.project.saves.at(-1);
    assert(saved && saved.placedResults.some((key) => key === 'inpaint_task#raw_inpaint'), 'raw placement receipt is saved with the canvas');
    if (caseName === 'delayed') {
      assert(workspace.project.saves.some((save) => save.placedResults.includes('inpaint_task#raw_inpaint')));
    }
    const beforeReload = state.nodes.map((node) => node.id).sort();
    await page.reload();
    await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
    await waitNodes(2);
    const afterReload = await waitStable();
    assert.equal(afterReload.nodes.map((node) => node.id).sort().join(','), beforeReload.join(','), 'reload keeps exactly the patch and normal layer');
    assert.equal(patchNodes(afterReload).length, 1);
    assert(noRawInpaint(afterReload), 'reload does not bring the raw inpaint back');
    if (caseName === 'quiet') {
      const beforeQuiet = afterReload.nodes.length;
      await page.evaluate(() => {
        window.__workspaceStore.getState().setCurrentObject({
          taskId: 'inpaint_task', kind: 'image', url: '/api/v1/assets/raw_inpaint', assetId: 'raw_inpaint',
          sessionId: 's1', quiet: true,
        });
      });
      const quiet = await waitStable(600);
      assert.equal(quiet.nodes.length, beforeQuiet, 'a quiet selection stays off the board after the job is gone');
      assert(noRawInpaint(quiet));
      await page.evaluate(() => {
        window.__workspaceStore.getState().setCurrentObject({
          taskId: 'inpaint_task', kind: 'image', url: '/api/v1/assets/raw_inpaint', assetId: 'raw_inpaint',
          sessionId: 's1',
        });
      });
      await waitNodes(beforeQuiet + 1);
      const explicit = await waitStable();
      assert(explicit.nodes.some((node) => node.imageUrl.includes('raw_inpaint')), 'an explicit task-centre choice can still place the raw picture');
    }
    if (caseName === 'match' || caseName === 'storage-fail-match') {
      await page.evaluate(() => {
        for (const node of [...window.__canvasStore.getState().nodesBySession.s1]) window.__canvasStore.getState().removeNode('s1', node.id);
      });
      await page.waitForTimeout(1200);
      await page.reload();
      await page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') }).waitFor();
      await page.waitForTimeout(800);
      const deleted = await read();
      assert.equal(deleted.nodes.length, 0, 'a user deletion is not resurrected');
      assert(noRawInpaint(deleted));
    }
  }
  // The save-fail case asks the fixture to refuse every write; the browser then logs that refused request.
  const unexpected = unexpectedErrors(errors);
  assert.deepEqual(caseName === 'save-fail' ? unexpected.filter((error) => !error.includes('500')) : unexpected, []);
  await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-workspace-${caseName}.png`) });
  await page.close();
}

// CASES=<regex> runs only the matching cases (for example CASES='brush' while iterating on one case).
const CASES = process.env.CASES ? new RegExp(process.env.CASES) : null;

async function record(engine, viewport, name, fn) {
  if (CASES && !CASES.test(name)) return;
  currentStep = name;
  const started = Date.now();
  console.error(`START ${name} ${viewport}`);
  try {
    const note = await fn();
    results.push({ engine, viewport, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
  } catch (error) {
    results.push({ engine, viewport, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
    console.error(`FAIL ${name} ${viewport}`);
    console.error(error);
  }
}

function unexpectedErrors(errors) {
  return errors.filter((error) => !error.includes('404') && !error.includes('ERR_ABORTED'));
}

async function layerMaskContract(browser, engine, viewport) {
  const { page, errors } = await openPage(browser, viewport, 'layers');
  const label = text('ui.canvas.layers.inpaint_mask_view');
  const row = (id) => page.locator(`[data-testid=layer-row][data-node="${id}"]`);
  const image = () => page.getByTestId('inpaint-mask-image');
  const open = async (id) => {
    await row(id).getByRole('button', { name: label }).click();
    await image().waitFor();
  };
  await row('good').waitFor();
  await open('good');
  const first = await image().evaluate((img) => {
    const canvas = document.createElement('canvas');
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    const context = canvas.getContext('2d');
    context.drawImage(img, 0, 0);
    const alpha = (x, y) => context.getImageData(x, y, 1, 1).data[3];
    return { width: img.naturalWidth, height: img.naturalHeight, inside: alpha(Math.floor(img.naturalWidth / 2), Math.floor(img.naturalHeight / 2)), outside: alpha(2, 2) };
  });
  assert.deepEqual(first, { width: 3840, height: 2160, inside: 0, outside: 255 });
  const checker = await page.evaluate(() => {
    const tile = document.querySelector('[data-testid="inpaint-mask-checker-tile"]');
    const style = getComputedStyle(tile);
    return { image: style.backgroundImage, size: style.backgroundSize, opacity: style.opacity };
  });
  const transparency = schema.canvas.frontend.artboard.transparency;
  assert(checker.image.includes('data:image/png'), 'the viewer uses the Schema checker tile');
  assert.equal(checker.size, `${transparency.cell_pixels * 2}px ${transparency.cell_pixels * 2}px`);
  assert.equal(checker.opacity, String(transparency.opacity));
  await page.keyboard.press('Escape');
  await image().waitFor({ state: 'detached' });
  await page.reload();
  await row('good').waitFor();
  await page.waitForFunction(() => typeof window.__setSession === 'function');
  await open('good');
  await page.keyboard.press('Escape');

  await row('missing').getByRole('button', { name: label }).click();
  await page.getByTestId('inpaint-mask-unavailable').waitFor();
  await page.keyboard.press('Escape');
  await row('corrupt').getByRole('button', { name: label }).click();
  await page.getByTestId('inpaint-mask-unavailable').waitFor();
  await page.keyboard.press('Escape');
  await open('good');
  await page.keyboard.press('Escape');
  await row('plain').getByRole('button', { name: text('ui.canvas.layers.visibility_hide') }).click();
  assert.equal(await page.evaluate(() => window.__canvasStore.getState().nodesBySession.s1.find((item) => item.id === 'plain')?.visible), false);
  await row('plain').getByRole('button', { name: text('ui.canvas.layers.visibility_show') }).click();

  await row('slow').getByRole('button', { name: label }).click();
  await page.getByTestId('inpaint-mask-loading').waitFor();
  await page.keyboard.press('Escape');
  await page.waitForTimeout(1200);
  assert.equal(await image().count(), 0);
  const cancellation = await page.evaluate(() => ({ ...window.__objectUrls, aborted: window.__abortedFetches }));
  assert(cancellation.aborted > 0, 'closing the viewer aborts its request');
  assert.equal(cancellation.created, cancellation.revoked, 'the viewer releases every object URL');

  await page.waitForFunction(() => typeof window.__setSession === 'function');
  await page.evaluate(() => window.__setSession('s2'));
  await row('other').waitFor();
  await open('other');
  assert.equal(await image().evaluate((img) => img.naturalWidth), 2048);
  await page.keyboard.press('Escape');
  await page.evaluate(() => window.__setSession('s1'));
  await row('good').waitFor();
  await open('good');
  await page.evaluate(() => window.__setSession('s2'));
  await page.waitForTimeout(100);
  assert.equal(await image().count(), 0, 'a session switch does not keep the old mask');
  await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-layers.png`) });
  assert.deepEqual(unexpectedErrors(errors), []);
  await page.close();
}

async function boardFitContract(browser, engine, viewport) {
  const { page, errors } = await openPage(browser, viewport, 'board-fit');
  await page.getByTestId('canvas-zoom-controls').waitFor();
  const recenter = page.getByRole('button', { name: text('ui.canvas.toolbar.recenter') });
  const read = async () => page.evaluate(() => ({
    viewport: window.__canvasStore.getState().viewportBySession.s1,
    nodes: JSON.stringify(window.__canvasStore.getState().nodesBySession.s1),
    content: window.__screen.contentBounds(),
    insets: window.__screen.insets(),
    initial: window.__screen.emptyView(),
    stage: (() => { const area = document.querySelector('[data-canvas-drawing-area]'); return { width: area.clientWidth, height: area.clientHeight }; })(),
  }));
  const before = await read();
  await recenter.click();
  await page.waitForTimeout(100);
  const first = await read();
  await recenter.click();
  await page.waitForTimeout(100);
  const second = await read();
  assert.deepEqual(second.viewport, first.viewport, 'repeated content fit is stable');
  await page.evaluate(() => {
    window.__canvasStore.setState({ viewportBySession: { s1: { zoom: 0.3, panX: 2000, panY: -3000 } } });
  });
  await recenter.click();
  await page.waitForTimeout(100);
  const fromZoomedOut = await read();
  assert.deepEqual(fromZoomedOut.viewport, first.viewport, 'content fit restores both zoomed-in and zoomed-out views');
  assert.equal(first.nodes, before.nodes, 'content fit does not change nodes');
  assert(first.content && first.content.width > 0 && first.content.height > 0);
  const pad = schema.canvas.frontend.viewport.fit_padding_pixels;
  const clearX = Math.max(first.insets.left, first.insets.right);
  const clearY = Math.max(first.insets.top, first.insets.bottom);
  const left = first.content.x * first.viewport.zoom + first.viewport.panX;
  const top = first.content.y * first.viewport.zoom + first.viewport.panY;
  const width = first.content.width * first.viewport.zoom;
  const height = first.content.height * first.viewport.zoom;
  assert(left >= clearX + pad - 1 && top >= clearY + pad - 1);
  assert(left + width <= first.stage.width - clearX - pad + 1);
  assert(top + height <= first.stage.height - clearY - pad + 1);
  assert(Math.abs(left + width / 2 - first.stage.width / 2) < 2);
  assert(Math.abs(top + height / 2 - first.stage.height / 2) < 2);

  await page.evaluate(() => {
    window.__canvasStore.setState({
      nodesBySession: { s1: [{ id: 'one', type: 'shape', shapeType: 'line', imageUrl: '', taskId: '', x: 0, y: 0, width: 100, height: 0, zIndex: 1, strokeWidth: 2 }] },
    });
  });
  await page.waitForTimeout(100);
  const singleBefore = await read();
  await recenter.click();
  await page.waitForTimeout(100);
  const single = await read();
  assert(single.content?.height === 2, 'a horizontal line is content with its stroke');
  assert.equal(single.nodes, singleBefore.nodes, 'a single-line fit does not change nodes');

  await page.evaluate(() => {
    window.__canvasStore.setState({
      nodesBySession: { s1: [] },
      viewportBySession: { s1: { zoom: 3, panX: -4000, panY: 7000 } },
    });
  });
  await page.waitForTimeout(100);
  await recenter.click();
  await page.waitForTimeout(100);
  const empty = await read();
  assert.equal(empty.content, null);
  assert.deepEqual(empty.viewport, empty.initial, 'an empty canvas returns to the default artboard view');
  await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-fit.png`) });
  assert.deepEqual(unexpectedErrors(errors), []);
  await page.close();
}

async function openMask(page) {
  await page.getByTestId('mask-done').waitFor();
  await page.getByTestId('mask-done').click();
  await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).waitFor();
}

async function maskExportContract(browser, engine, viewport, sizeKey) {
  const { page, errors } = await openPage(browser, viewport, 'board-mask', sizeKey);
  const expected = sizeFor(sizeKey);
  await page.locator('[data-testid=mask-done]').waitFor();
  await page.locator('[data-testid=mask-done]').click();
  await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).waitFor();
  const captured = await page.evaluate(async () => {
    const request = window.__captured;
    const maskBitmap = await createImageBitmap(request.mask);
    const canvas = document.createElement('canvas');
    canvas.width = maskBitmap.width;
    canvas.height = maskBitmap.height;
    const context = canvas.getContext('2d');
    context.drawImage(maskBitmap, 0, 0);
    const alpha = (x, y) => context.getImageData(x, y, 1, 1).data[3];
    const sourceResponse = await fetch(request.imageUrl);
    const sourceBlob = await sourceResponse.blob();
    const sourceBitmap = await createImageBitmap(sourceBlob);
    return {
      requestSize: request.size,
      maskSignature: [...new Uint8Array(await request.mask.arrayBuffer()).slice(0, 8)],
      mask: [maskBitmap.width, maskBitmap.height],
      inside: alpha(Math.floor(maskBitmap.width / 2), Math.floor(maskBitmap.height / 2)),
      outside: alpha(2, 2),
      source: [sourceBitmap.width, sourceBitmap.height],
      sourceType: sourceBlob.type,
    };
  });
  assert.deepEqual(captured.requestSize, expected);
  assert.deepEqual(captured.mask, [expected.width, expected.height]);
  assert.equal(captured.inside, 0);
  assert.equal(captured.outside, 255);
  assert.deepEqual(captured.source, [expected.width, expected.height]);
  assert.equal(captured.sourceType, 'image/png');
  assert.deepEqual(captured.maskSignature, [137, 80, 78, 71, 13, 10, 26, 10]);
  await page.locator('#inpaint-prompt').fill('fixture repaint');
  await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
  await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).waitFor();
  const prepared = fixture.prepareBodies.at(-1);
  const sourceUpload = await page.evaluate(() => (window.__capturedUploads ?? [])
    .find((entry) => !entry.filename.includes('mask')));
  assert(sourceUpload, 'the board export upload is captured with its fresh URL');
  const sourceAssetId = sourceUpload.url.split('/').pop();
  assert.equal(prepared.input.size, `${expected.width}x${expected.height}`);
  assert.equal(prepared.input.prompt, 'fixture repaint');
  assert.deepEqual(prepared.input.reference_assets, [sourceAssetId]);
  assert.equal(prepared.input.mask, IDS.localMask);
  assert.equal(prepared.offering_id, offering.id);
  assert.equal(prepared.capability, 'image-to-image');
  assert.equal(prepared.session_id, 's1');
  const uploads = await page.evaluate(async () => {
    const entries = window.__capturedUploads ?? [];
    const result = [];
    for (const entry of entries) {
      const bitmap = await createImageBitmap(entry.blob);
      const canvas = document.createElement('canvas');
      canvas.width = bitmap.width;
      canvas.height = bitmap.height;
      const context = canvas.getContext('2d');
      context.drawImage(bitmap, 0, 0);
      const pixel = (x, y) => [...context.getImageData(x, y, 1, 1).data];
      result.push({
        filename: entry.filename,
        url: entry.url,
        width: bitmap.width,
        height: bitmap.height,
        center: pixel(Math.floor(bitmap.width / 2), Math.floor(bitmap.height / 2)),
        outside: pixel(2, 2),
      });
    }
    return result;
  });
  assert.equal(uploads.length, 2, 'the actual board export and the actual mask upload are both captured');
  const boardUpload = uploads.find((entry) => !entry.filename.includes('mask'));
  const maskUpload = uploads.find((entry) => entry.filename.includes('mask'));
  assert(boardUpload && maskUpload, 'both uploads are captured');
  assert.equal(boardUpload.filename.includes('inpaint'), true);
  assert.deepEqual([boardUpload.width, boardUpload.height], [expected.width, expected.height]);
  assert.deepEqual(boardUpload.center.slice(0, 3), [220, 40, 40]);
  assert.deepEqual(boardUpload.outside.slice(0, 3), [40, 120, 220]);
  assert.equal(maskUpload.filename, 'inpaint-mask.png');
  assert.deepEqual([maskUpload.width, maskUpload.height], [expected.width, expected.height]);
  assert.equal(maskUpload.center[3], 0);
  assert.equal(maskUpload.outside[3], 255);
  await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-${sizeKey}-mask.png`) });
  assert.deepEqual(unexpectedErrors(errors), []);
  await page.close();
}


// #310: the mask brush shows its real footprint as a cursor before and while painting (at any zoom), and
// overlapping or repeated strokes form ONE tinted area: the overlap is exactly as strong as a single pass.
// The submitted mask keeps its bytes: an overlap is as clear as a single stroke, and painting the same
// stroke twice changes no pixel outside the antialiased rim of that stroke.
async function installBrushProbe(page) {
  await page.evaluate(() => {
    // Konva registers itself as window.Konva; importing it again in the fixture could load a second copy.
    const stage = () => window.Konva.stages[window.Konva.stages.length - 1];
    const composite = () => {
      const layers = stage().getLayers().filter((layer) => layer.isVisible());
      const first = layers[0].getCanvas();
      const canvas = document.createElement('canvas');
      canvas.width = first._canvas.width;
      canvas.height = first._canvas.height;
      const context = canvas.getContext('2d', { willReadFrequently: true });
      for (const layer of layers) context.drawImage(layer.getCanvas()._canvas, 0, 0);
      return { context, ratio: first.getPixelRatio() };
    };
    const toStage = (world) => stage().getAbsoluteTransform().point(world);
    window.__probe = {
      toClient(world) {
        const point = toStage(world);
        const rect = stage().container().getBoundingClientRect();
        return { x: rect.left + point.x, y: rect.top + point.y };
      },
      zoom: () => stage().getAbsoluteScale().x,
      /** The Schema mask tint (canvas.frontend.mask): its CSS token resolved to sRGB, and its opacity. */
      maskTint() {
        const mask = window.__ART_AI_PRODUCT_SCHEMA__.canvas.frontend.mask;
        const color = getComputedStyle(document.documentElement).getPropertyValue(mask.tint_color_token).trim();
        const canvas = document.createElement('canvas');
        canvas.width = 1;
        canvas.height = 1;
        const context = canvas.getContext('2d', { willReadFrequently: true });
        context.fillStyle = color;
        context.fillRect(0, 0, 1, 1);
        return { token: mask.tint_color_token, color, rgb: [...context.getImageData(0, 0, 1, 1).data].slice(0, 3), opacity: mask.tint_opacity };
      },
      /** The Schema selection tint (canvas.frontend.selection), kept by a pure marquee / lasso selection. */
      selectionTint() {
        const selection = window.__ART_AI_PRODUCT_SCHEMA__.canvas.frontend.selection;
        const canvas = document.createElement('canvas');
        canvas.width = 1;
        canvas.height = 1;
        const context = canvas.getContext('2d', { willReadFrequently: true });
        context.fillStyle = selection.tint_color;
        context.fillRect(0, 0, 1, 1);
        return { color: selection.tint_color, rgb: [...context.getImageData(0, 0, 1, 1).data].slice(0, 3), opacity: selection.tint_opacity };
      },
      frame: () => window.__maskStore.getState().draft?.frame ?? null,
      /** Average colour (3x3 device pixels) of the composited board under each world point. */
      sample(points) {
        const { context, ratio } = composite();
        return points.map((world) => {
          const point = toStage(world);
          const data = context.getImageData(Math.round(point.x * ratio) - 1, Math.round(point.y * ratio) - 1, 3, 3).data;
          const sum = [0, 0, 0];
          for (let index = 0; index < data.length; index += 4) for (let channel = 0; channel < 3; channel += 1) sum[channel] += data[index + channel];
          return sum.map((value) => Math.round(value / 9));
        });
      },
      /**
       * Painted thickness (CSS px) of a horizontal stroke: the run of device pixels down the column at world x whose
       * colour moved at least half way from `bare` to `tinted` (the 50% coverage edge of the antialiased stroke).
       */
      thickness(world, reach, bare, tinted) {
        const { context, ratio } = composite();
        const top = toStage({ x: world.x, y: world.y - reach });
        const bottom = toStage({ x: world.x, y: world.y + reach });
        const x = Math.round(top.x * ratio);
        const y0 = Math.round(top.y * ratio);
        const y1 = Math.round(bottom.y * ratio);
        const data = context.getImageData(x, y0, 1, y1 - y0).data;
        const full = Math.abs(tinted[0] - bare[0]) + Math.abs(tinted[1] - bare[1]) + Math.abs(tinted[2] - bare[2]);
        let count = 0;
        for (let index = 0; index < data.length; index += 4) {
          const moved = Math.abs(data[index] - bare[0]) + Math.abs(data[index + 1] - bare[1]) + Math.abs(data[index + 2] - bare[2]);
          if (moved * 2 >= full) count += 1;
        }
        return count / ratio;
      },
      cursor() {
        const node = stage().findOne('.mask-brush-cursor');
        if (!node || !node.isVisible()) return null;
        const rect = node.getClientRect({ skipStroke: true });
        const box = stage().container().getBoundingClientRect();
        return { width: rect.width, height: rect.height, x: box.left + rect.x + rect.width / 2, y: box.top + rect.y + rect.height / 2 };
      },
      async mask() {
        const request = window.__captured;
        const bitmap = await createImageBitmap(request.mask);
        const canvas = document.createElement('canvas');
        canvas.width = bitmap.width;
        canvas.height = bitmap.height;
        const context = canvas.getContext('2d', { willReadFrequently: true });
        context.drawImage(bitmap, 0, 0);
        const data = context.getImageData(0, 0, bitmap.width, bitmap.height).data;
        const alpha = new Uint8Array(bitmap.width * bitmap.height);
        for (let index = 0; index < alpha.length; index += 1) alpha[index] = data[index * 4 + 3];
        return { width: bitmap.width, height: bitmap.height, alpha };
      },
    };
  });
}

async function maskBrushContract(browser, engine, viewport) {
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
  try {
    await page.locator('[data-testid=mask-done]').waitFor();
    await installBrushProbe(page);
    // Canvas units (the artboard is in canvas pixels): about 30-50 screen pixels in the fitted view.
    const SIZE = 120;
    await page.evaluate((size) => {
      window.__maskStore.setState({ draft: null });
      window.__canvasStore.getState().setBrushSize(size);
    }, SIZE);
    const board = await page.evaluate(() => window.__board);
    const at = (box, fx, fy) => ({ x: box.x + box.width * fx, y: box.y + box.height * fy });
    const client = (world) => page.evaluate((point) => window.__probe.toClient(point), world);
    const drag = async (box, points, steps = 12) => {
      const first = await client(at(box, ...points[0]));
      await page.mouse.move(first.x, first.y);
      await page.mouse.down();
      for (const point of points.slice(1)) {
        const next = await client(at(box, ...point));
        await page.mouse.move(next.x, next.y, { steps });
      }
      await page.mouse.up();
    };
    assert(board && board.width > 0 && board.height > 0, 'the artboard frame is known');

    // Before painting: the cursor is a circle whose on-screen diameter is the brush size at this zoom.
    const zoom = await page.evaluate(() => window.__probe.zoom());
    const hover = await client(at(board, 0.1, 0.15));
    await page.mouse.move(hover.x - 3, hover.y);
    await page.mouse.move(hover.x, hover.y);
    await page.waitForTimeout(50);
    const before = await page.evaluate(() => window.__probe.cursor());
    assert(before, 'the mask brush shows its footprint before painting');
    assert(Math.abs(before.width - SIZE * zoom) <= 1, `cursor diameter ${before.width} matches brush ${SIZE * zoom}`);
    assert(Math.abs(before.x - hover.x) <= 1 && Math.abs(before.y - hover.y) <= 1, 'the cursor is centred on the pointer');

    // While painting: the cursor follows the pointer with the same diameter.
    await page.mouse.down();
    const mid = await client(at(board, 0.3, 0.15));
    await page.mouse.move(mid.x, mid.y, { steps: 8 });
    await page.waitForTimeout(50);
    const during = await page.evaluate(() => window.__probe.cursor());
    assert(during, 'the mask brush shows its footprint while painting');
    assert(Math.abs(during.width - SIZE * zoom) <= 1, 'the cursor keeps the brush diameter while painting');
    assert(Math.abs(during.x - mid.x) <= 1 && Math.abs(during.y - mid.y) <= 1, 'the cursor follows the pointer while painting');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-brush-cursor-painting.png`) });
    const end = await client(at(board, 0.6, 0.15));
    await page.mouse.move(end.x, end.y, { steps: 8 });
    await page.mouse.up();

    // Stroke B crosses A, stroke A is then painted again, and stroke D goes back and forth over itself.
    await drag(board, [[0.35, 0.04], [0.35, 0.28]]);
    await drag(board, [[0.1, 0.15], [0.6, 0.15]]);
    await drag(board, [[0.7, 0.15], [0.9, 0.15], [0.72, 0.15], [0.88, 0.15]]);
    const strokes = await page.evaluate(() => window.__maskStore.getState().draft.strokes.length);
    assert.equal(strokes, 4, 'four strokes were recorded');
    // Park the pointer far from the samples so the cursor ring is not under them.
    const away = await client(at(board, 0.95, 0.9));
    await page.mouse.move(away.x, away.y);
    await page.waitForTimeout(80);

    const check = async (label) => {
      const [bare, aOnly, bOnly, overlap, repeatedEnd, zigzag] = await page.evaluate((points) => window.__probe.sample(points), [
        at(board, 0.2, 0.3), at(board, 0.2, 0.15), at(board, 0.35, 0.07), at(board, 0.35, 0.15), at(board, 0.55, 0.15), at(board, 0.75, 0.15),
      ]);
      const distance = (left, right) => Math.max(...left.map((value, index) => Math.abs(value - right[index])));
      assert(distance(aOnly, bare) > 10, `${label}: the painted area is tinted (${aOnly} vs ${bare})`);
      // A single pass shows exactly the Schema mask tint once over the picture (canvas.frontend.mask.*).
      const tint = await page.evaluate(() => window.__probe.maskTint());
      assert(tint.color !== '' && tint.opacity > 0, `${label}: the mask tint token ${tint.token} resolves`);
      const expected = bare.map((value, index) => Math.round(value * (1 - tint.opacity) + tint.rgb[index] * tint.opacity));
      assert(distance(aOnly, expected) <= 3, `${label}: a single pass ${aOnly} is the Schema mask tint ${tint.color} at ${tint.opacity} over ${bare} (${expected})`);
      for (const [name, value] of [['B only', bOnly], ['A×B overlap', overlap], ['A painted twice', repeatedEnd], ['self-overlapping stroke', zigzag]]) {
        assert(distance(value, aOnly) <= 3, `${label}: ${name} ${value} is as strong as a single pass ${aOnly}`);
      }
      const currentZoom = await page.evaluate(() => window.__probe.zoom());
      const thickness = await page.evaluate(({ world, reach, bare, tinted }) => window.__probe.thickness(world, reach, bare, tinted),
        { world: at(board, 0.2, 0.15), reach: SIZE * 1.5, bare, tinted: aOnly });
      assert(Math.abs(thickness - SIZE * currentZoom) <= 2, `${label}: painted width ${thickness} matches the brush ${SIZE * currentZoom}`);
      return { zoom: currentZoom, thickness, bare, aOnly, overlap };
    };
    const fitted = await check('fitted view');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-brush-overlap.png`) });

    const fittedViewport = await page.evaluate(() => window.__canvasStore.getState().viewportBySession.s1);
    // Zoomed in: the cursor and the painted width both scale with the view, the overlap stays single strength.
    await page.evaluate((anchor) => {
      // Zoom about the crossing so every sample stays on screen.
      const state = window.__canvasStore.getState();
      const current = state.viewportBySession.s1;
      const zoom = current.zoom * 1.6;
      const screen = { x: anchor.x * current.zoom + current.panX, y: anchor.y * current.zoom + current.panY };
      state.setViewport('s1', { ...current, zoom, panX: screen.x - anchor.x * zoom, panY: screen.y - anchor.y * zoom });
    }, at(board, 0.35, 0.15));
    await page.waitForTimeout(80);
    const zoomedHover = await client(at(board, 0.3, 0.22));
    await page.mouse.move(zoomedHover.x, zoomedHover.y);
    await page.waitForTimeout(50);
    const zoomedZoom = await page.evaluate(() => window.__probe.zoom());
    const zoomedCursor = await page.evaluate(() => window.__probe.cursor());
    assert(zoomedCursor && Math.abs(zoomedCursor.width - SIZE * zoomedZoom) <= 1, 'the cursor scales with the zoom');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-brush-zoomed.png`) });
    const awayZoomed = await client(at(board, 0.6, 0.5));
    await page.mouse.move(awayZoomed.x, awayZoomed.y);
    await page.waitForTimeout(80);
    const zoomed = await check('zoomed view');

    // The submitted mask: overlap, repeat and single pass are all fully editable (alpha 0); outside stays kept.
    await page.getByTestId('mask-done').click();
    await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).waitFor();
    const size = sizeFor('wide');
    const maskPixel = (world) => [Math.round((world.x - board.x) * size.width / board.width), Math.round((world.y - board.y) * size.height / board.height)];
    const sampled = await page.evaluate(async (points) => {
      const mask = await window.__probe.mask();
      return { width: mask.width, height: mask.height, alpha: points.map(([x, y]) => mask.alpha[y * mask.width + x]) };
    }, [at(board, 0.2, 0.15), at(board, 0.35, 0.07), at(board, 0.35, 0.15), at(board, 0.55, 0.15), at(board, 0.75, 0.15), at(board, 0.95, 0.5)].map(maskPixel));
    assert.deepEqual([sampled.width, sampled.height], [size.width, size.height]);
    assert.deepEqual(sampled.alpha, [0, 0, 0, 0, 0, 255], 'overlaps export exactly like a single pass');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.cancel') }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });

    // The same stroke once vs twice: the editable footprint is identical; only the antialiased rim may differ.
    const exportWith = async (copies) => {
      await page.evaluate((count) => {
        const store = window.__maskStore;
        const draft = store.getState().draft;
        const first = draft.strokes[0];
        store.setState({ draft: { ...draft, strokes: Array.from({ length: count }, () => ({ ...first, points: [...first.points] })), steps: undefined, redo: [], redoSteps: undefined, current: null } });
      }, copies);
      await page.getByTestId('mask-done').click();
      await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).waitFor();
      await page.evaluate(async (tag) => { window[tag] = await window.__probe.mask(); }, `__mask${copies}`);
      await page.getByRole('button', { name: text('ui.canvas.inpaint.cancel') }).click();
      await page.getByRole('dialog').waitFor({ state: 'detached' });
    };
    await exportWith(1);
    await exportWith(2);
    const twice = await page.evaluate(() => {
      const once = window.__mask1.alpha;
      const again = window.__mask2.alpha;
      let footprintOnce = 0;
      let footprintTwice = 0;
      let differing = 0;
      let differingOutsideRim = 0;
      for (let index = 0; index < once.length; index += 1) {
        if (once[index] < 255) footprintOnce += 1;
        if (again[index] < 255) footprintTwice += 1;
        if (once[index] !== again[index]) {
          differing += 1;
          if (once[index] === 0 || once[index] === 255) differingOutsideRim += 1;
        }
      }
      return { footprintOnce, footprintTwice, differing, differingOutsideRim };
    });
    assert(twice.footprintOnce > 0);
    assert.equal(twice.footprintTwice, twice.footprintOnce, 'a repeated stroke does not widen or shrink the editable area');
    assert.equal(twice.differingOutsideRim, 0, 'a repeated stroke changes no fully editable or fully kept pixel');

    // Mixed steps (a brush stroke, a marquee region over it, an erase stroke, then an inversion) still show ONE
    // tint over the merged editable area: the same strength as a single pass, nothing where it was taken back.
    await page.evaluate((viewport) => window.__canvasStore.getState().setViewport('s1', viewport), fittedViewport);
    const mixed = async (invert, regionOnly = false) => {
      await page.evaluate(({ box, invert, regionOnly }) => {
        const store = window.__maskStore;
        const draft = store.getState().draft;
        const local = (fx, fy) => [box.width * fx, box.height * fy];
        const brush = draft.strokes[0];
        const region = { points: [...local(0.15, 0.1), ...local(0.3, 0.1), ...local(0.3, 0.25), ...local(0.15, 0.25)], size: 0, region: true };
        const erase = { points: [...local(0.5, 0.05), ...local(0.5, 0.3)], size: brush.size, erase: true };
        const strokes = regionOnly ? [region] : [brush, region, erase, ...(invert ? [{ points: [], size: 0, invert: true }] : [])];
        store.setState({ draft: { ...draft, strokes, steps: undefined, redo: [], redoSteps: undefined, current: null } });
      }, { box: board, invert, regionOnly });
      await page.mouse.move(away.x, away.y);
      await page.waitForTimeout(80);
      return page.evaluate((points) => window.__probe.sample(points), [
        at(board, 0.2, 0.3), at(board, 0.4, 0.15), at(board, 0.2, 0.15), at(board, 0.2, 0.22), at(board, 0.5, 0.15), at(board, 0.9, 0.3),
      ]);
    };
    const near = (left, right) => Math.max(...left.map((value, index) => Math.abs(value - right[index]))) <= 3;
    const tint = await page.evaluate(() => window.__probe.maskTint());
    const tinted = (bare) => bare.map((value, index) => Math.round(value * (1 - tint.opacity) + tint.rgb[index] * tint.opacity));
    {
      const [bare, brushOnly, brushAndRegion, regionOnly, erased, untouched] = await mixed(false);
      assert(near(brushOnly, tinted(bare)), `mixed: brush ${brushOnly} is the mask tint over ${bare}`);
      assert(near(brushAndRegion, brushOnly) && near(regionOnly, brushOnly), `mixed: region ${regionOnly} and overlap ${brushAndRegion} equal one pass ${brushOnly}`);
      assert(near(erased, bare) && near(untouched, bare), `mixed: erased ${erased} and untouched ${untouched} show the picture ${bare}`);
      await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-brush-mixed.png`) });
    }
    {
      // Inverted, the point used as "bare" above is editable too; the picture now shows inside the former selection.
      const [nowTinted, brushOnly, brushAndRegion, regionOnly, erased, outside] = await mixed(true);
      const bare = brushAndRegion;
      assert(near(brushOnly, bare) && near(regionOnly, bare), `inverted: the former selection shows the picture (${brushOnly}, ${regionOnly}, ${bare})`);
      assert(near(outside, tinted(bare)) && near(erased, outside) && near(nowTinted, outside), `inverted: outside ${outside}, erased ${erased} and ${nowTinted} carry one tint over ${bare}`);
      await page.screenshot({ path: path.join(OUT, `${engine}-${viewport.width}-brush-inverted.png`) });
    }
    {
      // A pure marquee selection (no brush stroke) keeps the selection tint, laid once.
      const selectionTint = await page.evaluate(() => window.__probe.selectionTint());
      const [bare, , , regionOnly] = await mixed(false, true);
      const expected = bare.map((value, index) => Math.round(value * (1 - selectionTint.opacity) + selectionTint.rgb[index] * selectionTint.opacity));
      assert(near(regionOnly, expected), `marquee only: ${regionOnly} is the selection tint ${selectionTint.color} at ${selectionTint.opacity} over ${bare} (${expected})`);
    }
    assert.deepEqual(unexpectedErrors(errors), []);
    return `zoom ${fitted.zoom.toFixed(3)}→${zoomed.zoom.toFixed(3)}; width ${fitted.thickness.toFixed(1)}/${zoomed.thickness.toFixed(1)}px; tint ${fitted.aOnly} overlap ${fitted.overlap}; repeat rim px ${twice.differing}`;
  } finally {
    await page.close();
  }
}

async function maskUnknownContract(browser, engine, viewport) {
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide', '', '&offering=unknown');
  await page.getByTestId('mask-done').click();
  await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).waitFor();
  assert(await page.getByTestId('inpaint-output-size').innerText().then((value) => value.includes('无法确认这个模型的输出尺寸')));
  const prepare = page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') });
  assert(await prepare.isDisabled(), 'unknown output size disables preparation');
  const before = { prepare: fixture.prepareBodies.length, grants: fixture.grantBodies.length, tasks: fixture.taskBodies.length };
  await prepare.click({ force: true }).catch(() => undefined);
  await page.waitForTimeout(100);
  assert.equal(fixture.prepareBodies.length, before.prepare, 'unknown output size creates no preparation');
  assert.equal(fixture.grantBodies.length, before.grants, 'unknown output size creates no grant');
  assert.equal(fixture.taskBodies.length, before.tasks, 'unknown output size creates no task');
  assert.deepEqual(unexpectedErrors(errors), []);
  await page.close();
}

async function maskBlockedContract(browser, engine, viewport, offeringKey, expected) {
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide', '', `&offering=${offeringKey}`);
  await page.getByTestId('mask-done').click();
  await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).waitFor();
  assert(await page.getByTestId('inpaint-output-size').innerText().then((value) => value.includes(expected)));
  const prepare = page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') });
  assert(await prepare.isDisabled(), `${offeringKey} output size disables preparation`);
  const before = { prepare: fixture.prepareBodies.length, grants: fixture.grantBodies.length, tasks: fixture.taskBodies.length };
  await prepare.click({ force: true }).catch(() => undefined);
  await page.waitForTimeout(100);
  assert.equal(fixture.prepareBodies.length, before.prepare, `${offeringKey} output size creates no preparation`);
  assert.equal(fixture.grantBodies.length, before.grants, `${offeringKey} output size creates no grant`);
  assert.equal(fixture.taskBodies.length, before.tasks, `${offeringKey} output size creates no task`);
  assert.deepEqual(unexpectedErrors(errors), []);
  await page.close();
}

async function maskLifecycleContract(browser, engine, viewport) {
  const checkDraft = async (page) => page.evaluate(() => Boolean(window.__maskStore.getState().draft));
  const noErrors = (label, errors, allowed = []) => {
    const unexpected = unexpectedErrors(errors).filter((error) => !allowed.some((part) => error.includes(part)));
    if (unexpected.length) console.error('LIFECYCLE ERRORS', label, unexpected);
    assert.deepEqual(unexpected, []);
  };
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.getByRole('button', { name: text('ui.canvas.inpaint.cancel') }).click();
    await page.waitForTimeout(50);
    assert(await checkDraft(page), 'cancel keeps the draft');
    noErrors('cancel', errors);
    await page.close();
  }
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('fixture failure');
    fixture.failPrepare = true;
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    await page.getByText(text('ui.canvas.inpaint.failed'), { exact: false }).waitFor();
    assert(await checkDraft(page), 'prepare failure keeps the draft');
    fixture.failPrepare = false;
    noErrors('failure', errors, ['500 (Internal Server Error)', 'console:inpaint failed']);
    await page.close();
  }
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('fixture accepted');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    assert.equal(await page.evaluate(() => window.__maskStore.getState().draft), null, 'accepted task clears the submitted draft');
    assert(fixture.taskBodies.length > 0);
    noErrors('accepted', errors);
    await page.close();
  }
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('fixture changed');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).waitFor();
    await page.evaluate(() => {
      const store = window.__maskStore;
      const current = store.getState().draft;
      store.getState().setDraft({ ...current, strokes: [...current.strokes, { points: [10, 10, 20, 20], size: 8 }] });
    });
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    assert.equal(await page.evaluate(() => window.__maskStore.getState().draft?.strokes.length), 2, 'acceptance does not clear a newer draft');
    noErrors('updated', errors);
    await page.close();
  }
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('fixture remapped');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).waitFor();
    const remapped = await page.evaluate(() => {
      const store = window.__maskStore;
      const draft = store.getState().draft;
      const frame = draft.frame;
      return {
        outcome: store.getState().remapFrame('s1', { x: frame.x + 40, y: frame.y + 40, width: frame.width, height: frame.height }),
        same: (() => {
          const current = store.getState().draft;
          return current === draft;
        })(),
      };
    });
    assert.notEqual(remapped.same, true, 'remapping creates a new draft object');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    const afterRemap = await page.evaluate(() => {
      const draft = window.__maskStore.getState().draft;
      return { present: Boolean(draft), strokes: draft?.strokes.length ?? 0 };
    });
    assert.deepEqual(afterRemap, { present: true, strokes: 1 }, 'acceptance does not clear a remapped draft');
    noErrors('remapped', errors);
    await page.close();
  }
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.evaluate(() => {
      const store = window.__maskStore;
      const draft = store.getState().draft;
      store.getState().beginStroke('s1', { x: draft.frame.x + 100, y: draft.frame.y + 100 }, 8, draft.frame);
      store.getState().extendStroke('s1', { x: draft.frame.x + 140, y: draft.frame.y + 140 });
      store.getState().endStroke('s1');
    });
    await page.locator('#inpaint-prompt').fill('fixture undone');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).waitFor();
    await page.evaluate(() => window.__maskStore.getState().undoStroke('s1'));
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    const afterUndo = await page.evaluate(() => {
      const draft = window.__maskStore.getState().draft;
      return { present: Boolean(draft), strokes: draft?.strokes.length ?? 0 };
    });
    assert.deepEqual(afterUndo, { present: true, strokes: 1 }, 'acceptance does not clear a draft changed by undo');
    noErrors('undone', errors);
    await page.close();
  }
  {
    const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('fixture delayed');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).waitFor();
    fixture.delayTask = true;
    await page.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') }).click();
    await page.waitForTimeout(100);
    assert.equal(await page.evaluate(() => Boolean(window.__maskStore.getState().draft)), true, 'the in-flight submission does not clear the draft');
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    await page.evaluate(() => {
      const store = window.__maskStore;
      const draft = store.getState().draft;
      store.getState().beginStroke('s1', { x: 200, y: 220 }, 8, draft.frame);
      store.getState().extendStroke('s1', { x: 240, y: 260 });
    });
    await page.evaluate(() => fetch('/api/__test/release-task'));
    await page.waitForTimeout(100);
    const live = await page.evaluate(() => {
      const draft = window.__maskStore.getState().draft;
      return { strokes: draft?.strokes.length ?? 0, current: Boolean(draft?.current) };
    });
    assert.deepEqual(live, { strokes: 1, current: true }, 'a delayed old receipt does not clear a newer in-progress stroke');
    noErrors('delayed', errors);
    await page.close();
  }
}

async function waitFor(predicate, timeout = 5000) {
  const deadline = Date.now() + timeout;
  while (!predicate() && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 25));
  return predicate();
}

async function sourceFingerprints(page) {
  return page.evaluate(async () => {
    const uploads = (window.__capturedUploads ?? []).filter((entry) => !entry.filename.includes('mask'));
    return Promise.all(uploads.map(async (entry) => {
      const bytes = new Uint8Array(await entry.blob.arrayBuffer());
      let first = 2166136261;
      let second = 0x9e3779b9;
      for (const byte of bytes) {
        first = Math.imul(first ^ byte, 16777619);
        second = Math.imul(second ^ byte, 2246822519);
      }
      return { url: entry.url, size: entry.blob.size, hash: `${bytes.length}-${first >>> 0}-${second >>> 0}` };
    }));
  });
}

async function maskSameUrlChangedSourceContract(browser, engine, viewport) {
  // The fixture reuses one upload URL, while the exported board bytes change between the two opens.
  // A frozen request may only be restored by the actual raster, never by the URL it was uploaded under.
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide', 'same-url-source');
  const labels = {
    prepare: text('ui.canvas.inpaint.prepare'),
    confirm: text('ui.canvas.inpaint.confirm_execution'),
    cancel: text('ui.canvas.inpaint.cancel'),
  };
  try {
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('changed source bytes');
    await page.getByRole('button', { name: labels.prepare }).click();
    await page.getByRole('button', { name: labels.confirm }).waitFor();
    await page.getByRole('button', { name: labels.cancel }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    await page.evaluate(() => {
      const sourceId = window.__FIXTURE__.ids.sourceWide;
      const store = window.__canvasStore.getState();
      const node = (store.nodesBySession.s1 ?? []).find((item) => item.imageUrl.includes(sourceId));
      store.updateNode('s1', node.id, { x: node.x + 40 });
    });
    await openMask(page);
    await page.getByRole('button', { name: labels.prepare }).waitFor();
    assert.equal(await page.getByRole('button', { name: labels.confirm }).count(), 0,
      'changed source bytes do not restore the frozen request');
    assert.equal(await page.locator('#inpaint-prompt').inputValue(), '');
    assert.equal(fixture.prepareBodies.length, 1, 'the reopen prepares nothing by itself');
    assert.equal(fixture.grantBodies.length, 1);
    assert.equal(fixture.taskBodies.length, 0);
    const sources = await sourceFingerprints(page);
    assert.equal(sources.length, 2, 'the reopen exported the board once more');
    assert.equal(sources[0].url, sources[1].url, 'the fixture kept the same upload URL');
    assert.notEqual(sources[0].hash, sources[1].hash, 'the exported raster bytes changed');
    assert.deepEqual(unexpectedErrors(errors), []);
    await page.getByRole('button', { name: labels.cancel }).click();
  } finally {
    await page.close();
  }
}

async function maskChangedFrameContract(browser, engine, viewport) {
  // The mask and the board move with the dashed frame. A request prepared for another frame must not be
  // offered again even when both pictures happen to encode the same bytes.
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide');
  const labels = {
    prepare: text('ui.canvas.inpaint.prepare'),
    confirm: text('ui.canvas.inpaint.confirm_execution'),
    cancel: text('ui.canvas.inpaint.cancel'),
  };
  try {
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('changed frame');
    await page.getByRole('button', { name: labels.prepare }).click();
    await page.getByRole('button', { name: labels.confirm }).waitFor();
    await page.getByRole('button', { name: labels.cancel }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
    const frameX = await page.evaluate(() => window.__maskStore.getState().draft.frame.x);
    await page.evaluate(() => window.__setBoardArtboard((value) => ({ ...value, x: value.x + 40 })));
    await page.waitForFunction((x) => window.__maskStore.getState().draft?.frame?.x === x, frameX + 40);
    await openMask(page);
    await page.getByRole('button', { name: labels.prepare }).waitFor();
    assert.equal(await page.getByRole('button', { name: labels.confirm }).count(), 0,
      'a request prepared for another frame is not restored');
    assert.equal(await page.locator('#inpaint-prompt').inputValue(), '');
    assert.equal(fixture.prepareBodies.length, 1, 'the reopen prepares nothing by itself');
    assert.equal(fixture.grantBodies.length, 1);
    assert.equal(fixture.taskBodies.length, 0);
    assert.deepEqual(unexpectedErrors(errors), []);
    await page.getByRole('button', { name: labels.cancel }).click();
  } finally {
    await page.close();
  }
}

async function maskDroppedTaskContract(browser, engine, viewport, mode) {
  // The fixture accepts the first Task POST and drops its response. The page cannot know whether the task
  // exists; the next click must retry the same prepared input, grant and key, not prepare and charge again.
  const policy = mode === 'policy';
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide', `drop-task-${mode}`);
  const dialog = page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') });
  const labels = {
    prepare: text('ui.canvas.inpaint.prepare'),
    confirm: text('ui.canvas.inpaint.confirm_execution'),
    cancel: text('ui.canvas.inpaint.cancel'),
  };
  try {
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('dropped response');
    await page.getByRole('button', { name: labels.prepare }).click();
    if (!policy) {
      await page.getByRole('button', { name: labels.confirm }).waitFor();
      await page.getByRole('button', { name: labels.confirm }).click();
    }
    await page.getByText(text('ui.canvas.inpaint.failed'), { exact: false }).waitFor();
    assert.equal(fixture.taskBodies.length, 1, 'the accepted task POST reached the fixture');
    assert.equal(fixture.acceptedTaskCount, 1, 'the fixture accepted exactly one task');
    // Close and reopen: the frozen identity is restored, so the button already confirms execution.
    await page.getByRole('button', { name: labels.cancel }).click();
    await dialog.waitFor({ state: 'detached' });
    await openMask(page);
    await page.getByRole('button', { name: labels.confirm }).waitFor();
    assert.equal(await page.getByRole('button', { name: labels.prepare }).count(), 0,
      'the reopened dialog retries the frozen identity instead of preparing again');
    assert.equal(await page.locator('#inpaint-prompt').inputValue(), 'dropped response');
    await page.getByRole('button', { name: labels.confirm }).click();
    await dialog.waitFor({ state: 'detached' });
    assert.equal(fixture.prepareBodies.length, 1, 'the retry reuses the frozen preparation');
    assert.equal(fixture.grantBodies.length, 1, 'the retry reuses the grant');
    assert.equal(fixture.taskBodies.length, 2, 'one accepted POST and its replay');
    assert(fixture.taskKeys[0], 'the task POST carries an idempotency key');
    assert.equal(fixture.taskKeys[0], fixture.taskKeys[1], 'the replay keeps the same idempotency key');
    assert.deepEqual(fixture.taskBodies[1], fixture.taskBodies[0], 'the replay posts the same prepared input and grant');
    assert.equal(fixture.acceptedTaskCount, 1, 'the retry never creates a second task');
    const sources = await sourceFingerprints(page);
    assert.equal(sources.length, 2, 'closing and reopening exports the board once more');
    assert.notEqual(sources[0].url, sources[1].url, 'the reopen uploads under a fresh asset URL');
    assert.deepEqual({ size: sources[0].size, hash: sources[0].hash }, { size: sources[1].size, hash: sources[1].hash },
      'the actual exported bytes stayed identical across the reopen');
    assert.equal(fixture.prepareBodies[0].input.reference_assets[0], sources[0].url.split('/').pop(),
      'the frozen preparation still points at the first upload asset');
    const state = await page.evaluate(() => ({
      jobs: window.__inpaintJobs(),
      tracked: window.__workspaceStore.getState().trackedTasks.map((task) => task.taskId),
      draft: window.__maskStore.getState().draft,
    }));
    assert(state.jobs.includes('task_fixture'), 'the accepted task keeps its inpaint job');
    assert.equal(state.tracked.filter((taskId) => taskId === 'task_fixture').length, 1,
      'the accepted task is tracked exactly once');
    assert.equal(state.draft, null, 'the confirmed submission clears its draft');
    const allowed = ['inpaint failed', 'Failed to fetch', 'ERR_FAILED'];
    const unexpected = unexpectedErrors(errors).filter((error) => !allowed.some((part) => error.includes(part)));
    assert.deepEqual(unexpected, []);
  } finally {
    await page.close();
  }
}

async function maskCancelContract(browser, engine, viewport, mode) {
  const { page, errors } = await openPage(browser, viewport, 'board-mask', 'wide', `cancel-${mode}`);
  const grantMode = mode === 'grant' || mode === 'grant-roundtrip' || mode === 'grant-model-roundtrip';
  const roundTrip = mode === 'grant-roundtrip' || mode === 'grant-model-roundtrip';
  try {
    await openMask(page);
    await page.locator('#inpaint-prompt').fill('cancel fixture');
    await page.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    if (mode === 'upload') {
      await page.waitForFunction(() => typeof window.__releaseUpload === 'function');
    } else if (grantMode) {
      assert(await waitFor(() => Boolean(fixture.releaseGrant)), 'the grant request is in flight');
    } else {
      assert(await waitFor(() => Boolean(fixture.releaseTask)), 'the task request is in flight');
    }
    if (mode === 'grant-roundtrip') {
      await page.evaluate(() => window.__chatStore.getState().setActiveSession('s2'));
      await page.waitForFunction(() => window.__chatStore.getState().activeSessionId === 's2');
      await page.evaluate(() => window.__chatStore.getState().setActiveSession('s1'));
      await page.waitForFunction(() => window.__chatStore.getState().activeSessionId === 's1');
    } else if (mode === 'grant-model-roundtrip') {
      await page.evaluate(() => window.__setBoardModel('b'));
      await page.waitForFunction(() => window.__boardModel === 'b');
      await page.evaluate(() => window.__setBoardModel('a'));
      await page.waitForFunction(() => window.__boardModel === 'a');
    } else {
      await page.getByRole('button', { name: text('ui.canvas.inpaint.cancel') }).click();
    }
    if (mode === 'upload') {
      await page.evaluate(() => window.__releaseUpload());
    } else if (grantMode) {
      await page.evaluate(() => fetch('/api/__test/release-grant'));
    } else {
      await page.evaluate(() => fetch('/api/__test/release-task'));
    }
    await page.waitForTimeout(700);
    assert(await page.evaluate(() => Boolean(window.__maskStore.getState().draft)), 'cancel keeps the draft');
    assert.equal(await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).count(),
      roundTrip ? 1 : 0, roundTrip ? 'the dialog stays open on a round trip' : 'cancel closes the dialog');
    if (roundTrip) {
      assert.equal(fixture.prepareBodies.length, 1);
      assert.equal(fixture.grantBodies.length, 1);
      assert.equal(fixture.taskBodies.length, 0, 'a session/model round trip does not revive the stale submission');
    } else if (mode === 'upload') {
      assert.equal(fixture.prepareBodies.length, 0, 'a cancelled upload sends no preparation');
      assert.equal(fixture.grantBodies.length, 0);
      assert.equal(fixture.taskBodies.length, 0);
    } else if (mode === 'grant') {
      assert.equal(fixture.prepareBodies.length, 1);
      assert.equal(fixture.grantBodies.length, 1);
      assert.equal(fixture.taskBodies.length, 0, 'an approved grant is not followed by a task after cancel');
    } else {
      assert.equal(fixture.taskBodies.length, 1, 'the task POST was already in flight');
      const jobs = await page.evaluate(() => window.__inpaintJobs());
      assert(jobs.includes('task_fixture'), 'the accepted task still remembers its inpaint job');
      await openMask(page);
      assert.equal(await page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') }).count(), 1,
        'a new dialog is not closed by the old callback');
      assert.equal(await page.locator('#inpaint-prompt').inputValue(), '');
      assert.equal(fixture.taskBodies.length, 1, 'the reopened dialog posts nothing by itself');
    }
    assert.deepEqual(unexpectedErrors(errors), []);
  } finally {
    await page.close();
  }
}

try {
  for (const engine of ENGINES) {
    const engineApi = pw[engine];
    if (!engineApi) throw new Error(`unknown engine ${engine}`);
    for (const [width, height] of VIEWPORTS) {
      const browser = await engineApi.launch({ headless: true });
      try {
        await record(engine, `${width}x${height}`, 'layer mask viewer, reload, errors, session', () => layerMaskContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'back-to-content fit, lines, empty, repeat', () => boardFitContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'wide mask/source bytes and prepare', () => maskExportContract(browser, engine, { width, height }, 'wide'));
        await record(engine, `${width}x${height}`, 'workspace inpaint raw suppression and normal task', () => workspaceContract(browser, engine, { width, height }, 'match'));
        await record(engine, `${width}x${height}`, 'workspace mismatched inpaint raw rejection', () => workspaceContract(browser, engine, { width, height }, 'mismatch'));
        await record(engine, `${width}x${height}`, 'workspace legacy placement seed', () => workspaceContract(browser, engine, { width, height }, 'legacy'));
        await record(engine, `${width}x${height}`, 'workspace restored selection suppression', () => workspaceContract(browser, engine, { width, height }, 'restored'));
        await record(engine, `${width}x${height}`, 'workspace saved receipt without job', () => workspaceContract(browser, engine, { width, height }, 'restored-receipt'));
        await record(engine, `${width}x${height}`, 'workspace new canvas restores an ordinary first picture', () => workspaceContract(browser, engine, { width, height }, 'restored-new'));
        await record(engine, `${width}x${height}`, 'workspace seeded receipt still restores the ordinary picture', () => workspaceContract(browser, engine, { width, height }, 'restored-seed'));
        await record(engine, `${width}x${height}`, 'workspace seeded receipt never restores an inpaint raw result', () => workspaceContract(browser, engine, { width, height }, 'restored-raw-seed'));
        await record(engine, `${width}x${height}`, 'workspace retried inpaint raw results never auto-join', () => workspaceContract(browser, engine, { width, height }, 'retry-operation'));
        await record(engine, `${width}x${height}`, 'workspace matching storage failure keeps the patch', () => workspaceContract(browser, engine, { width, height }, 'storage-fail-match'));
        await record(engine, `${width}x${height}`, 'workspace mismatched storage failure keeps the job receipt', () => workspaceContract(browser, engine, { width, height }, 'storage-fail-mismatch'));
        await record(engine, `${width}x${height}`, 'workspace finished job never flushes another session', () => workspaceContract(browser, engine, { width, height }, 'session-hop'));
        await record(engine, `${width}x${height}`, 'workspace interrupted upload retries on its own session', () => workspaceContract(browser, engine, { width, height }, 'session-hop-success'));
        await record(engine, `${width}x${height}`, 'workspace stale upload returns to a settled owner board', () => workspaceContract(browser, engine, { width, height }, 'session-hop-return'));
        await record(engine, `${width}x${height}`, 'workspace save failure keeps the job', () => workspaceContract(browser, engine, { width, height }, 'save-fail'));
        await record(engine, `${width}x${height}`, 'workspace landed patch is not duplicated', () => workspaceContract(browser, engine, { width, height }, 'already-landed'));
        await record(engine, `${width}x${height}`, 'workspace delayed and duplicated lineage', async () => {
          await workspaceContract(browser, engine, { width, height }, 'delayed');
          await workspaceContract(browser, engine, { width, height }, 'duplicate');
        });
        await record(engine, `${width}x${height}`, 'workspace quiet selection after job removal', () => workspaceContract(browser, engine, { width, height }, 'quiet'));
        await record(engine, `${width}x${height}`, 'mask brush cursor, single-layer overlap and mask bytes', () => maskBrushContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'square mask/source bytes and prepare', () => maskExportContract(browser, engine, { width, height }, 'square'));
        await record(engine, `${width}x${height}`, 'portrait mask/source bytes and prepare', () => maskExportContract(browser, engine, { width, height }, 'portrait'));
        await record(engine, `${width}x${height}`, 'unknown output size is refused', () => maskUnknownContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'fixed output size is refused', () => maskBlockedContract(browser, engine, { width, height }, 'fixed', '无法确认这个模型的输出尺寸'));
        await record(engine, `${width}x${height}`, 'non-canvas ratio is refused', () => maskBlockedContract(browser, engine, { width, height }, 'ratio', '最接近的合法尺寸'));
        await record(engine, `${width}x${height}`, 'mask cancel/prepare failure/accepted/updated', () => maskLifecycleContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'mask cancel during upload sends no prepare', () => maskCancelContract(browser, engine, { width, height }, 'upload'));
        await record(engine, `${width}x${height}`, 'mask cancel during grant sends no task', () => maskCancelContract(browser, engine, { width, height }, 'grant'));
        await record(engine, `${width}x${height}`, 'mask cancel during task keeps draft and job', () => maskCancelContract(browser, engine, { width, height }, 'task'));
        await record(engine, `${width}x${height}`, 'same source URL with changed bytes does not restore the frozen request', () => maskSameUrlChangedSourceContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'a changed board frame does not restore the frozen request', () => maskChangedFrameContract(browser, engine, { width, height }));
        await record(engine, `${width}x${height}`, 'manual inpaint retries a lost task response with the same identity', () => maskDroppedTaskContract(browser, engine, { width, height }, 'manual'));
        await record(engine, `${width}x${height}`, 'policy-approved inpaint retries a lost task response with the same identity', () => maskDroppedTaskContract(browser, engine, { width, height }, 'policy'));
        await record(engine, `${width}x${height}`, 'mask grant session round trip does not revive the stale task', () => maskCancelContract(browser, engine, { width, height }, 'grant-roundtrip'));
        await record(engine, `${width}x${height}`, 'mask grant model round trip does not revive the stale task', () => maskCancelContract(browser, engine, { width, height }, 'grant-model-roundtrip'));
      } finally {
        await browser.close();
      }
    }
  }
} finally {
  if (fixture.externalRequests.length > 0) {
    results.push({ engine: 'all', viewport: 'all', name: 'no external requests', passed: false, error: fixture.externalRequests.join(',') });
  }
  await server.close();
  await writeFile(path.join(OUT, 'report.json'), JSON.stringify({
    scope: 'mounted loopback fixture; real components + fixture API; no app server, provider, paid call or deployment claim',
    results,
    externalRequests: fixture.externalRequests,
  }, null, 2), { mode: 0o600 });
  const failed = results.filter((item) => !item.passed);
  for (const item of results) console.log(`${item.passed ? 'PASS' : 'FAIL'} ${item.engine} ${item.viewport} ${item.name}${item.error ? `: ${item.error}` : ''}`);
  console.log(`Evidence: ${OUT}`);
  if (failed.length > 0) process.exitCode = 1;
}
