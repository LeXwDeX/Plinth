#!/usr/bin/env node
/**
 * Local measurement for #290: page cost of a workspace with many large pictures, at the scale users reach
 * (ten 4K layers on the board, twenty chat turns with image results), through the real UI in Chromium.
 * Not a gate: it prints numbers and writes report.json; it fails only when the page cannot be driven.
 *
 * Needs a local server with mock providers and mock login (no credentials, no paid calls); see
 * scripts/measure-canvas-image-cache.mjs for the environment, plus a fixed mock user so every run (and a
 * restarted server) signs in as the same person, e.g. DEV_MOCK_USER='{"id":"mock_perf","name":"perf-qa",
 * "email":"perf-qa@example.test"}'. Two phases share one data folder, so a baseline build and a changed
 * build can be measured on the same data (copy the server's database and storage after seeding and start
 * each measured build on a fresh copy):
 *
 *   BASE=http://127.0.0.1:18479 STATE=<dir> PHASE=seed    node scripts/measure-image-heavy-workspace.mjs
 *   BASE=http://127.0.0.1:18479 STATE=<dir> PHASE=measure node scripts/measure-image-heavy-workspace.mjs
 *
 * seed: runs TURNS image turns in session A and TURNS_B in
 *   session B through the composer, uploads LAYERS 3840x2160 PNGs (LAYERS_B 2048x2048 for B) and makes
 *   them the boards' layers through the canvas API.
 * measure: opens A, then reports the time until its chat results and layers are shown, decoded picture
 *   bytes (layer pictures, their reduced display copies (#291) also listed apart, and chat/strip <img>
 *   elements that finished loading), renderer + GPU process
 *   memory, frame intervals and main-thread time while panning and zooming the board and scrolling the
 *   chat, long tasks during one streamed turn, and the time to switch to B and back.
 *
 * Optional env: OUT (report folder, default STATE), CPU_THROTTLE (CDP CPU slow-down factor, default 1),
 * HEADLESS_SHELL=1 (old headless shell with a software GPU instead of the full headless browser),
 * PLAYWRIGHT_MODULE_PATH. Every request outside BASE is refused.
 */
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
function loadPlaywright() {
  const candidates = [process.env.PLAYWRIGHT_MODULE_PATH, path.join(root, 'package.json'), path.join(process.cwd(), 'package.json'), path.join(process.env.HOME || '', 'package.json')];
  for (const candidate of candidates.filter(Boolean)) {
    try {
      return candidate.endsWith('package.json') ? createRequire(candidate)('playwright') : createRequire(path.join(root, 'package.json'))(candidate);
    } catch { /* try the next place */ }
  }
  throw new Error('playwright not found: set PLAYWRIGHT_MODULE_PATH');
}
const pw = loadPlaywright();

const BASE = (process.env.BASE || '').replace(/\/$/, '');
if (!/^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):\d+$/.test(BASE)) {
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18479');
  process.exit(2);
}
const PHASE = process.env.PHASE || 'measure';
const STATE = process.env.STATE;
if (!STATE || !path.isAbsolute(STATE)) {
  console.error('STATE must be an absolute folder (shared by the seed and measure phases)');
  process.exit(2);
}
mkdirSync(STATE, { recursive: true, mode: 0o700 });
const OUT = process.env.OUT || STATE;
mkdirSync(OUT, { recursive: true, mode: 0o700 });
const TURNS = Number(process.env.TURNS || 20);
const TURNS_B = Number(process.env.TURNS_B || 4);
const LAYERS = Number(process.env.LAYERS || 10);
const LAYERS_B = Number(process.env.LAYERS_B || 9);
const CPU_THROTTLE = Number(process.env.CPU_THROTTLE || 1);
const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const canvasFields = application.canvas.api.fields;
const uploadField = application.runtime.input_assets.upload_field_name;

// Every `new Image()` of the page is remembered weakly; frame intervals, long tasks and drawImage calls are recorded.
const INSTRUMENT = `(() => {
  const Native = window.Image;
  const refs = [];
  window.Image = new Proxy(Native, {
    construct(target, args, newTarget) {
      const image = Reflect.construct(target, args, newTarget === window.Image ? target : newTarget);
      refs.push(new WeakRef(image));
      return image;
    },
  });
  // Pictures decoded with createImageBitmap(blob) are tracked too, named by the address the blob came from;
  // a reduced display copy made from such a bitmap (#291) is tracked under the same address as \`reduced\`.
  const blobSource = new WeakMap();
  const nativeBlob = Response.prototype.blob;
  Response.prototype.blob = async function () {
    const blob = await nativeBlob.call(this);
    blobSource.set(blob, this.url);
    return blob;
  };
  const bitmaps = [];
  const bitmapSource = new WeakMap();
  if (window.createImageBitmap) {
    const nativeBitmap = window.createImageBitmap.bind(window);
    window.createImageBitmap = async (source, ...rest) => {
      const bitmap = await nativeBitmap(source, ...rest);
      const url = blobSource.get(source) ?? bitmapSource.get(source);
      if (url) {
        bitmaps.push({ url, ref: new WeakRef(bitmap), reduced: bitmapSource.has(source) });
        bitmapSource.set(bitmap, url);
      }
      return bitmap;
    };
  }
  // Each entry: { src, width, height } for a live <img>-free picture or bitmap that holds decoded pixels.
  window.__trackedImages = () => [
    ...refs.map((ref) => ref.deref()).filter(Boolean).map((image) => ({ src: image.src, width: image.complete ? image.naturalWidth : 0, height: image.complete ? image.naturalHeight : 0 })),
    ...bitmaps.map((entry) => ({ src: entry.url, reduced: entry.reduced, bitmap: entry.ref.deref() })).filter((entry) => entry.bitmap).map((entry) => ({ src: entry.src, reduced: entry.reduced, width: entry.bitmap.width, height: entry.bitmap.height })),
  ];
  window.__longTasks = [];
  try {
    new PerformanceObserver((list) => { for (const entry of list.getEntries()) window.__longTasks.push(entry.duration); })
      .observe({ type: 'longtask', buffered: false });
  } catch { /* not supported */ }
  const draw = CanvasRenderingContext2D.prototype.drawImage;
  window.__draw = { calls: 0, ms: 0, pixels: 0 };
  CanvasRenderingContext2D.prototype.drawImage = function (...args) {
    const start = performance.now();
    const result = draw.apply(this, args);
    window.__draw.ms += performance.now() - start;
    window.__draw.calls += 1;
    const source = args[0];
    if (source instanceof HTMLImageElement) window.__draw.pixels += source.naturalWidth * source.naturalHeight;
    else if (source instanceof ImageBitmap) window.__draw.pixels += source.width * source.height;
    return result;
  };
  window.__frameStart = () => {
    window.__frames = [];
    let last = performance.now();
    window.__frameLoop = true;
    const tick = (now) => {
      window.__frames.push(now - last);
      last = now;
      if (window.__frameLoop) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  };
  window.__frameStop = () => { window.__frameLoop = false; return window.__frames.slice(2); };
})()`;

const round = (value, digits = 1) => Math.round(value * 10 ** digits) / 10 ** digits;
function summarize(frames) {
  const sorted = [...frames].sort((a, b) => a - b);
  const pick = (q) => round(sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))] ?? 0);
  return { frames: sorted.length, p50: pick(0.5), p95: pick(0.95), max: round(sorted.at(-1) ?? 0), over33ms: sorted.filter((value) => value > 33.4).length };
}

async function launch() {
  const browser = await pw.chromium.launch({ headless: true, channel: process.env.HEADLESS_SHELL ? undefined : 'chromium', args: ['--js-flags=--expose-gc'] });
  const context = await browser.newContext({ viewport: { width: 1600, height: 1000 } });
  await context.addInitScript(INSTRUMENT);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.route('**/*', (route) => (new URL(route.request().url()).origin === BASE ? route.continue() : route.abort()));
  // The server's DEV_MOCK_USER: the same person on every run.
  const login = await page.request.post(`${BASE}${legacy.auth_mock}`, { data: {} });
  if (!login.ok()) throw new Error(`mock login ${login.status()}: set DEV_MOCK_USER on the server`);
  return { browser, context, page, errors };
}

async function approveIfAsked(page, done) {
  for (let i = 0; i < 240; i += 1) {
    const approve = page.locator('[data-testid=studio-approval-approve]');
    if (await approve.count()) { await approve.first().click(); return; }
    if (await done()) return;
    await page.waitForTimeout(250);
  }
}

const resultCount = (page) => page.locator('[data-testid=chat-image-result]').count();

async function imageTurn(page, prompt) {
  const before = await resultCount(page);
  const box = page.locator('[data-testid=studio-composer] textarea');
  await box.click();
  await box.fill(prompt);
  await page.keyboard.press('Enter');
  await approveIfAsked(page, async () => (await resultCount(page)) > before);
  await page.waitForFunction((count) => document.querySelectorAll('[data-testid=chat-image-result]').length > count, before, { timeout: 120000 });
  // The composer takes the next turn once the stream has settled.
  await page.waitForTimeout(800);
}

async function pickImageModel(page) {
  await page.click('[data-testid=workspace-model-picker]');
  await page.locator('[data-testid=workspace-model-picker-image] [role=menu] [role=menuitemradio]').first().click();
  await page.keyboard.press('Escape');
}

/** Draws a busy picture in the page (gradients, shapes and grain, so the PNG is several MB like a real one) and uploads it. */
async function uploadPicture(page, width, height, seed) {
  return page.evaluate(async ({ width, height, seed, assets, field }) => {
    const canvas = new OffscreenCanvas(width, height);
    const context = canvas.getContext('2d');
    const gradient = context.createLinearGradient(0, 0, width, height);
    gradient.addColorStop(0, `hsl(${(seed * 47) % 360} 60% 30%)`);
    gradient.addColorStop(1, `hsl(${(seed * 47 + 120) % 360} 55% 70%)`);
    context.fillStyle = gradient;
    context.fillRect(0, 0, width, height);
    let state = seed * 9301 + 49297;
    const random = () => { state = (state * 9301 + 49297) % 233280; return state / 233280; };
    for (let index = 0; index < 400; index += 1) {
      context.fillStyle = `hsla(${random() * 360} 70% ${30 + random() * 50}% / 0.5)`;
      context.beginPath();
      context.arc(random() * width, random() * height, 20 + random() * width / 8, 0, Math.PI * 2);
      context.fill();
    }
    const grain = context.getImageData(0, 0, width, height);
    for (let index = 0; index < grain.data.length; index += 4) {
      const noise = (random() - 0.5) * 18;
      grain.data[index] += noise; grain.data[index + 1] += noise; grain.data[index + 2] += noise;
    }
    context.putImageData(grain, 0, 0);
    const blob = await canvas.convertToBlob({ type: 'image/png' });
    const form = new FormData();
    form.append(field, new File([blob], `layer-${seed}.png`, { type: 'image/png' }));
    const response = await fetch(assets, { method: 'POST', body: form });
    if (!response.ok) throw new Error(`upload ${response.status}`);
    const body = await response.json();
    return { id: body.id, bytes: blob.size };
  }, { width, height, seed, assets: routes.assets, field: uploadField });
}

async function projectFor(page, sessionId) {
  const projects = await (await page.request.get(`${BASE}${legacy.canvas_projects}`)).json();
  const summary = projects.find((project) => project.sessionId === sessionId);
  if (!summary) throw new Error(`no canvas project for ${sessionId}`);
  return (await page.request.get(`${BASE}${legacy.canvas_project.replace('{projectID}', summary.id)}`)).json();
}

/** Replaces the board of a session with the blank layer and `count` uploaded pictures covering the frame. */
async function seedBoard(page, sessionId, count, width, height) {
  const project = await projectFor(page, sessionId);
  const blank = project.nodes.find((node) => JSON.parse(node.data || '{}').blank === true);
  const frameX = blank ? blank.x : 0;
  const frameY = blank ? blank.y : 0;
  const nodes = [];
  const uploaded = [];
  if (blank) nodes.push({ ...blank, width, height });
  for (let index = 0; index < count; index += 1) {
    const picture = await uploadPicture(page, width, height, index + 1 + (width === 3840 ? 0 : 100));
    uploaded.push(picture);
    const url = routes.asset_item.replace('{assetID}', picture.id);
    nodes.push({
      id: `perf-layer-${index + 1}`, type: 'image', x: frameX + index * 24, y: frameY + index * 16, width, height, zIndex: index + 1,
      data: JSON.stringify({ imageUrl: url, taskId: 'upload', name: `layer ${index + 1}` }), visible: true, locked: false,
    });
  }
  const body = {
    [canvasFields.nodes]: nodes.map((node) => ({
      [canvasFields.id]: node.id, [canvasFields.type]: node.type, [canvasFields.x]: node.x, [canvasFields.y]: node.y,
      [canvasFields.width]: node.width, [canvasFields.height]: node.height, [canvasFields.z_index]: node.zIndex,
      [canvasFields.data]: node.data, [canvasFields.visible]: node.visible, [canvasFields.locked]: node.locked,
    })),
    [canvasFields.revision]: project.revision,
    [canvasFields.canvas_width]: width,
    [canvasFields.canvas_height]: height,
    [canvasFields.coordinate_version]: project.coordinateVersion,
  };
  // From the page, so the request carries the page origin the server checks.
  const status = await page.evaluate(async ({ url, body }) => {
    const response = await fetch(url, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    return response.ok ? 'ok' : `${response.status}: ${await response.text()}`;
  }, { url: legacy.canvas_nodes.replace('{projectID}', project.id), body });
  if (status !== 'ok') throw new Error(`replace nodes ${status}`);
  return { project: project.id, layers: uploaded.map((picture) => routes.asset_item.replace('{assetID}', picture.id)), pngMiB: round(uploaded.reduce((sum, picture) => sum + picture.bytes, 0) / 1048576) };
}

const activeSession = (page) => page.evaluate(() => new URL(location.href).searchParams.get('session'));

async function seedTurns(page) {
  await page.goto(`${BASE}/`);
  await page.waitForSelector('[data-testid=canvas-tool-rail]', { timeout: 30000 });
  await pickImageModel(page);
  for (let turn = 0; turn < TURNS; turn += 1) {
    await imageTurn(page, `第 ${turn + 1} 张：角色设定，工作室光效`);
    console.log(`A turn ${turn + 1}/${TURNS}`);
  }
  const sessionA = await activeSession(page);
  await page.waitForTimeout(3000);
  // A new session for the switch measurement.
  await page.click('[data-testid=studio-session-menu]');
  await page.click('[data-testid=studio-session-new]');
  await page.waitForFunction((previous) => new URL(location.href).searchParams.get('session') !== previous, sessionA, { timeout: 30000 }).catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(1000);
  await pickImageModel(page);
  for (let turn = 0; turn < TURNS_B; turn += 1) {
    await imageTurn(page, `第 ${turn + 1} 张：场景原画`);
    console.log(`B turn ${turn + 1}/${TURNS_B}`);
  }
  const sessionB = await activeSession(page);
  await page.waitForTimeout(3000);
  return { sessionA, sessionB };
}

async function seed() {
  const { browser, page, errors } = await launch();
  // The turns are kept once made; a later seed run (same STATE) only rebuilds the boards.
  const sessionsFile = path.join(STATE, 'sessions.json');
  let sessions;
  if (existsSync(sessionsFile)) {
    sessions = JSON.parse(readFileSync(sessionsFile, 'utf8'));
  } else {
    sessions = await seedTurns(page);
    writeFileSync(sessionsFile, JSON.stringify(sessions), { mode: 0o600 });
  }
  const { sessionA, sessionB } = sessions;
  // Leave the workspace (for a same-origin JSON page, so uploads still work) before rewriting the boards, so
  // its autosave cannot write over them.
  await page.goto(`${BASE}${routes.product_schema}`);
  const boardA = await seedBoard(page, sessionA, LAYERS, 3840, 2160);
  const boardB = await seedBoard(page, sessionB, LAYERS_B, 2048, 2048);
  const state = { sessionA, sessionB, turnsA: TURNS, turnsB: TURNS_B, boardA, boardB, errors };
  writeFileSync(path.join(STATE, 'state.json'), JSON.stringify(state, null, 2), { mode: 0o600 });
  console.log(JSON.stringify(state, null, 2));
  await browser.close();
}

async function processMemory(browser) {
  // Resident memory of the renderer and GPU processes (macOS/Linux `ps`), the closest local figure to "decoded pictures".
  try {
    const session = await browser.newBrowserCDPSession();
    const { processInfo } = await session.send('SystemInfo.getProcessInfo');
    const pick = (type) => processInfo.filter((entry) => entry.type === type).map((entry) => entry.id);
    const rss = (ids) => ids.reduce((sum, id) => {
      try { return sum + Number(execFileSync('ps', ['-o', 'rss=', '-p', String(id)]).toString().trim()) * 1024; } catch { return sum; }
    }, 0);
    return { rendererMiB: round(rss(pick('renderer')) / 1048576), gpuMiB: round(rss(pick('GPU')) / 1048576) };
  } catch (error) {
    return { error: String(error) };
  }
}

async function pictureStats(page, layerUrls) {
  return page.evaluate((layers) => {
    const decoded = (image) => (image.complete && image.naturalWidth > 0 ? image.naturalWidth * image.naturalHeight * 4 : 0);
    const mib = (bytes) => Math.round(bytes / 1048576 * 10) / 10;
    const wanted = new Set(layers.map((url) => new URL(url, location.origin).href));
    const tracked = window.__trackedImages().filter((image) => wanted.has(image.src));
    const bytes = (picture) => picture.width * picture.height * 4;
    const loadedLayers = new Set(tracked.filter((picture) => bytes(picture) > 0).map((picture) => picture.src)).size;
    const elements = [...document.querySelectorAll('img')].filter((image) => image.src.includes('/api/'));
    const shown = elements.filter((image) => decoded(image) > 0);
    const chat = shown.filter((image) => image.closest('[data-testid=chat-messages]'));
    const strip = shown.filter((image) => image.closest('[data-testid=version-strip]'));
    const panel = shown.filter((image) => image.closest('[data-testid=right-panel]'));
    return {
      layerPictures: tracked.length, layersLoaded: loadedLayers, layerDecodedMiB: mib(tracked.reduce((sum, picture) => sum + bytes(picture), 0)),
      layerReducedCopies: tracked.filter((picture) => picture.reduced).length,
      layerReducedMiB: mib(tracked.filter((picture) => picture.reduced).reduce((sum, picture) => sum + bytes(picture), 0)),
      imgElements: elements.length, imgLoaded: shown.length, imgDecodedMiB: mib(shown.reduce((sum, image) => sum + decoded(image), 0)),
      chatImgLoaded: chat.length, chatImgDecodedMiB: mib(chat.reduce((sum, image) => sum + decoded(image), 0)),
      stripImgLoaded: strip.length, stripImgDecodedMiB: mib(strip.reduce((sum, image) => sum + decoded(image), 0)),
      panelImgLoaded: panel.length, panelImgDecodedMiB: mib(panel.reduce((sum, image) => sum + decoded(image), 0)),
      largestImg: shown.reduce((best, image) => Math.max(best, image.naturalWidth), 0),
    };
  }, layerUrls);
}

async function metrics(client) {
  const { metrics: list } = await client.send('Performance.getMetrics');
  return Object.fromEntries(list.map((entry) => [entry.name, entry.value]));
}

async function measured(page, client, action) {
  await page.evaluate(() => { window.__draw = { calls: 0, ms: 0, pixels: 0 }; window.__longTasks = []; window.__frameStart(); });
  const before = await metrics(client);
  await action();
  await page.waitForTimeout(300);
  const after = await metrics(client);
  const { frames, draw, longTasks } = await page.evaluate(() => ({ frames: window.__frameStop(), draw: window.__draw, longTasks: window.__longTasks }));
  return {
    ...summarize(frames),
    taskMs: round((after.TaskDuration - before.TaskDuration) * 1000),
    scriptMs: round((after.ScriptDuration - before.ScriptDuration) * 1000),
    longTasks: longTasks.length, longTaskMs: round(longTasks.reduce((sum, value) => sum + value, 0)),
    drawImageCalls: draw.calls, drawImageMs: round(draw.ms), drawImageMegapixels: round(draw.pixels / 1e6),
  };
}

async function waitShown(page, layerUrls, results, timeout = 60000) {
  const started = Date.now();
  for (;;) {
    const stats = await pictureStats(page, layerUrls);
    const count = await resultCount(page);
    if (stats.layersLoaded >= layerUrls.length && count >= results) return Date.now() - started;
    if (Date.now() - started > timeout) return -1;
    await page.waitForTimeout(50);
  }
}

async function pan(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  await page.click('[data-testid=canvas-tool-hand]');
  const start = { x: area.x + area.width / 2, y: area.y + area.height / 2 };
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  for (let step = 0; step <= 120; step += 1) {
    const angle = (step / 120) * Math.PI * 4;
    await page.mouse.move(start.x + Math.cos(angle) * 160, start.y + Math.sin(angle) * 100);
  }
  await page.mouse.up();
  await page.click('[data-testid=canvas-tool-select]');
}

async function zoom(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  await page.mouse.move(area.x + area.width / 2, area.y + area.height / 2);
  const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
  await page.keyboard.down(modifier);
  for (let step = 0; step < 30; step += 1) { await page.mouse.wheel(0, -40); await page.waitForTimeout(16); }
  for (let step = 0; step < 30; step += 1) { await page.mouse.wheel(0, 40); await page.waitForTimeout(16); }
  await page.keyboard.up(modifier);
}

async function scrollChat(page) {
  const messages = page.locator('[data-testid=chat-messages]');
  const box = await messages.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  for (let step = 0; step < 40; step += 1) { await page.mouse.wheel(0, -400); await page.waitForTimeout(16); }
  for (let step = 0; step < 40; step += 1) { await page.mouse.wheel(0, 400); await page.waitForTimeout(16); }
}

async function switchTo(page, sessionId) {
  await page.click('[data-testid=studio-session-menu]');
  await page.waitForSelector('[data-testid=studio-session-row]');
  // The seeded user has two sessions: the other one is the row that is not current.
  const target = page.locator('[data-testid=studio-session-row] > div > button:not([aria-current])').first();
  const started = Date.now();
  await target.click();
  await page.waitForFunction((id) => new URL(location.href).searchParams.get('session') === id, sessionId, { timeout: 30000 });
  return started;
}

async function measure() {
  const state = JSON.parse(readFileSync(path.join(STATE, 'state.json'), 'utf8'));
  const { browser, page, errors } = await launch();
  const client = await page.context().newCDPSession(page);
  await client.send('Performance.enable');
  if (CPU_THROTTLE > 1) await client.send('Emulation.setCPUThrottlingRate', { rate: CPU_THROTTLE });
  const report = { base: 'loopback mock server', browser: browser.version(), cpuThrottle: CPU_THROTTLE, scale: { turnsA: state.turnsA, layersA: state.boardA.layers.length, layerPngMiBA: state.boardA.pngMiB, turnsB: state.turnsB, layersB: state.boardB.layers.length } };
  report.gpu = await page.evaluate(() => {
    const gl = document.createElement('canvas').getContext('webgl');
    const info = gl && gl.getExtension('WEBGL_debug_renderer_info');
    return info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : 'no webgl';
  });

  const started = Date.now();
  await page.goto(`${BASE}/?session=${state.sessionA}`);
  await page.waitForSelector('[data-testid=canvas-tool-rail]', { timeout: 30000 });
  const shown = await waitShown(page, state.boardA.layers, state.turnsA);
  report.openA = { msUntilLayersAndResultsShown: shown < 0 ? -1 : Date.now() - started };
  await page.waitForTimeout(2000);
  await client.send('HeapProfiler.collectGarbage');
  report.openA.pictures = await pictureStats(page, state.boardA.layers);
  report.openA.memory = await processMemory(browser);
  report.openA.jsHeapMiB = round((await client.send('Runtime.getHeapUsage')).usedSize / 1048576);

  report.panFit = await measured(page, client, () => pan(page));
  report.zoom = await measured(page, client, () => zoom(page));
  report.chatScroll = await measured(page, client, () => scrollChat(page));
  await page.waitForTimeout(1000);
  await client.send('HeapProfiler.collectGarbage');
  report.afterScroll = { pictures: await pictureStats(page, state.boardA.layers), memory: await processMemory(browser) };

  // One streamed turn on top of the history.
  await pickImageModel(page);
  report.streamTurn = await measured(page, client, () => imageTurn(page, '再来一张：角色设定'));

  // Switch to B and back to A through the session menu.
  const toB = await switchTo(page, state.sessionB);
  const shownB = await waitShown(page, state.boardB.layers, state.turnsB);
  report.switchToB = { ms: shownB < 0 ? -1 : Date.now() - toB };
  await page.waitForTimeout(1500);
  const toA = await switchTo(page, state.sessionA);
  const shownA = await waitShown(page, state.boardA.layers, state.turnsA);
  report.switchToA = { ms: shownA < 0 ? -1 : Date.now() - toA };
  await page.waitForTimeout(1500);
  await client.send('HeapProfiler.collectGarbage');
  await page.waitForTimeout(500);
  report.afterSwitch = { jsHeapMiB: round((await client.send('Runtime.getHeapUsage')).usedSize / 1048576), pictures: await pictureStats(page, state.boardA.layers), memory: await processMemory(browser) };
  report.errors = errors;
  const file = path.join(OUT, `report-${Date.now()}.json`);
  writeFileSync(file, JSON.stringify(report, null, 2), { mode: 0o600 });
  console.log(JSON.stringify(report, null, 2));
  console.log(`report: ${file}`);
  await browser.close();
}

if (PHASE === 'seed') await seed();
else await measure();
