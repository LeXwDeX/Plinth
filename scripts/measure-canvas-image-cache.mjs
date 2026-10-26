#!/usr/bin/env node
/**
 * Local measurement for #287: how many decoded pictures the 2D board keeps alive over many image turns, through
 * the real UI in Chromium. Not a gate: it prints numbers (and writes report.json) and fails only when the page
 * cannot be driven.
 *
 * Needs a local server with mock providers and mock login (no credentials, no paid calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18479 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18479 node scripts/measure-canvas-image-cache.mjs
 *
 * Optional env: TURNS (image turns, default 8), OUT (report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH.
 * Every request outside BASE is refused.
 *
 * Each turn generates one picture (it becomes a layer), paints one brush stroke on the top layer and leaves the
 * brush (the stroke is uploaded and replaces that layer's picture), then deletes the oldest picture layer once
 * more than KEEP are on the board. After each turn, and after deleting every layer at the end, it forces a
 * garbage collection and counts the `Image` objects and `createImageBitmap` pictures created by the page that are
 * still alive and come from an asset address, with their decoded size (width x height x 4). It also reports the
 * JS heap and the frame interval while the view is panned, as an indication only (headless Chromium, mock
 * pictures of about 1024 px).
 */
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
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
const TURNS = Number(process.env.TURNS || 8);
const KEEP = 4;
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'canvas-image-cache-'));
mkdirSync(OUT, { recursive: true });
const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const MOCK_LOGIN = application.runtime.http.legacy_routes.auth_mock;
const MOD = process.platform === 'darwin' ? 'Meta' : 'Control';

// Every `new Image()` and every createImageBitmap() picture (#290: the board decodes its layers into bitmaps) of
// the page is remembered weakly, so only the ones something still holds are counted. A bitmap is named by the
// address its bytes were downloaded from.
const TRACK_IMAGES = `(() => {
  const Native = window.Image;
  const refs = [];
  window.Image = new Proxy(Native, {
    construct(target, args, newTarget) {
      const image = Reflect.construct(target, args, newTarget === window.Image ? target : newTarget);
      refs.push(new WeakRef(image));
      return image;
    },
  });
  const blobSource = new WeakMap();
  const nativeBlob = Response.prototype.blob;
  Response.prototype.blob = async function () {
    const blob = await nativeBlob.call(this);
    blobSource.set(blob, this.url);
    return blob;
  };
  const bitmaps = [];
  if (window.createImageBitmap) {
    const nativeBitmap = window.createImageBitmap.bind(window);
    window.createImageBitmap = async (source, ...rest) => {
      const bitmap = await nativeBitmap(source, ...rest);
      bitmaps.push({ url: blobSource.get(source) || '', ref: new WeakRef(bitmap) });
      return bitmap;
    };
  }
  window.__imageStats = (origin) => {
    let alive = 0, assets = 0, decoded = 0, created = refs.length + bitmaps.length;
    for (const ref of refs) {
      const image = ref.deref();
      if (!image) continue;
      alive += 1;
      if (!image.src.startsWith(origin + '/api/')) continue;
      assets += 1;
      if (image.complete && image.naturalWidth > 0) decoded += image.naturalWidth * image.naturalHeight * 4;
    }
    for (const entry of bitmaps) {
      const bitmap = entry.ref.deref();
      // A closed bitmap has no pixels left.
      if (!bitmap || bitmap.width === 0) continue;
      alive += 1;
      if (!entry.url.startsWith(origin + '/api/')) continue;
      assets += 1;
      decoded += bitmap.width * bitmap.height * 4;
    }
    return { created, alive, assets, decodedMiB: Math.round(decoded / 1048576 * 10) / 10 };
  };
})()`;

async function approveIfAsked(page, done) {
  for (let i = 0; i < 120; i += 1) {
    const approve = page.locator('[data-testid=studio-approval-approve]');
    if (await approve.count()) { await approve.first().click(); return; }
    if (await done()) return;
    await page.waitForTimeout(500);
  }
}

const layerCount = (page) => page.locator('[data-testid=layer-row]').count();
const resultCount = (page) => page.locator('[data-testid=chat-image-result]').count();

async function imageTurn(page, index) {
  const before = await resultCount(page);
  const layersBefore = await layerCount(page);
  const box = page.locator('[data-testid=studio-composer] textarea');
  await box.click();
  await box.fill(`第 ${index + 1} 张：一只猫，工作室光效`);
  await page.keyboard.press('Enter');
  await approveIfAsked(page, async () => (await resultCount(page)) > before);
  await page.waitForFunction((count) => document.querySelectorAll('[data-testid=chat-image-result]').length > count, before, { timeout: 120000 });
  await page.waitForFunction((count) => document.querySelectorAll('[data-testid=layer-row]').length > count, layersBefore, { timeout: 30000 });
  // Wait for the stream to settle (the composer accepts the next turn).
  await page.waitForTimeout(1200);
}

async function brushStroke(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  const centre = { x: area.x + area.width / 2, y: area.y + area.height / 2 };
  await page.click('[data-testid=canvas-tool-brush]');
  await page.mouse.move(centre.x - 30, centre.y - 30);
  await page.mouse.down();
  await page.mouse.move(centre.x + 30, centre.y + 30, { steps: 8 });
  await page.mouse.up();
  // Leaving the brush uploads the bitmap; the layer then shows the uploaded picture.
  await page.click('[data-testid=canvas-tool-select]');
  await page.waitForTimeout(2000);
}

async function deleteOldestPicture(page) {
  const rows = page.locator('[data-testid=layer-row]');
  // The panel lists the top layer first; the last row is the blank canvas layer, the one above it the oldest picture.
  const count = await rows.count();
  if (count < 2) return;
  await rows.nth(count - 2).locator('button').first().click();
  await page.keyboard.press('Delete');
  await page.waitForTimeout(300);
}

async function deleteAllLayers(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  await page.mouse.click(area.x + 8, area.y + area.height - 8);
  await page.keyboard.press(`${MOD}+a`);
  await page.keyboard.press('Delete');
  await page.waitForTimeout(500);
}

async function panFrames(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  await page.click('[data-testid=canvas-tool-hand]');
  await page.evaluate(() => {
    window.__frames = [];
    let last = performance.now();
    window.__frameLoop = true;
    const tick = (now) => {
      window.__frames.push(now - last);
      last = now;
      if (window.__frameLoop) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  const start = { x: area.x + area.width / 2, y: area.y + area.height / 2 };
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  for (let step = 0; step < 60; step += 1) {
    const angle = (step / 60) * Math.PI * 2;
    await page.mouse.move(start.x + Math.cos(angle) * 120, start.y + Math.sin(angle) * 80);
  }
  await page.mouse.up();
  const frames = await page.evaluate(() => { window.__frameLoop = false; return window.__frames.slice(2); });
  await page.click('[data-testid=canvas-tool-select]');
  frames.sort((a, b) => a - b);
  const pick = (q) => Math.round(frames[Math.min(frames.length - 1, Math.floor(frames.length * q))] * 10) / 10;
  return { frames: frames.length, p50: pick(0.5), p95: pick(0.95) };
}

async function sample(page, client, label) {
  await client.send('HeapProfiler.collectGarbage');
  await page.waitForTimeout(200);
  await client.send('HeapProfiler.collectGarbage');
  const images = await page.evaluate((origin) => window.__imageStats(origin), BASE);
  const { usedSize } = await client.send('Runtime.getHeapUsage');
  const heap = Math.round(usedSize / 1048576 * 10) / 10;
  // Listeners on window and document (getEventListeners is a DevTools console helper), to see that turns do not pile them up.
  const { result } = await client.send('Runtime.evaluate', {
    expression: 'Object.values(getEventListeners(window)).flat().length + Object.values(getEventListeners(document)).flat().length',
    includeCommandLineAPI: true, returnByValue: true,
  });
  const domCounters = await client.send('Memory.getDOMCounters');
  const layers = await layerCount(page);
  const chatPictures = await page.locator('[data-chat-picture]').count();
  return { label, layers, chatPictures, liveAssetImages: images.assets, decodedMiB: images.decodedMiB, imagesCreated: images.created, jsHeapMiB: heap,
    windowDocumentListeners: result.value, domNodes: domCounters.nodes, jsEventListeners: domCounters.jsEventListeners };
}

const browser = await pw.chromium.launch({ headless: true, args: ['--js-flags=--expose-gc'] });
const context = await browser.newContext({ viewport: { width: 1600, height: 1000 } });
await context.addInitScript(TRACK_IMAGES);
const page = await context.newPage();
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));
await page.route('**/*', (route) => (new URL(route.request().url()).origin === BASE ? route.continue() : route.abort()));
await page.request.post(`${BASE}${MOCK_LOGIN}`, { data: { name: 'cache-qa', email: 'cache-qa@example.test' } });
const client = await context.newCDPSession(page);

await page.goto(`${BASE}/`);
await page.waitForSelector('[data-testid=canvas-tool-rail]', { timeout: 30000 });
await page.click('[data-testid=workspace-model-picker]');
await page.locator('[data-testid=workspace-model-picker-image] [role=menu] [role=menuitemradio]').first().click();
await page.keyboard.press('Escape');

const samples = [];
for (let turn = 0; turn < TURNS; turn += 1) {
  await imageTurn(page, turn);
  await brushStroke(page);
  // The board keeps KEEP pictures, like a session whose older pictures were deleted or replaced.
  while ((await layerCount(page)) > KEEP + 1) await deleteOldestPicture(page);
  const entry = await sample(page, client, `turn ${turn + 1}`);
  entry.pan = await panFrames(page);
  samples.push(entry);
  console.log(JSON.stringify(entry));
}
await deleteAllLayers(page);
// React keeps the previous render's props (and so the deleted layers' pictures) until the board renders again.
await page.click('[data-testid=canvas-tool-hand]');
await page.click('[data-testid=canvas-tool-select]');
const cleared = await sample(page, client, 'all layers deleted');
samples.push(cleared);
console.log(JSON.stringify(cleared));

if (process.env.HEAP_SNAPSHOT) {
  // A heap snapshot at the end, to find what still holds a picture (DevTools Memory panel, Retainers).
  const chunks = [];
  client.on('HeapProfiler.addHeapSnapshotChunk', ({ chunk }) => chunks.push(chunk));
  await client.send('HeapProfiler.takeHeapSnapshot', { reportProgress: false });
  writeFileSync(path.join(OUT, 'end.heapsnapshot'), chunks.join(''), { mode: 0o600 });
}
writeFileSync(path.join(OUT, 'report.json'), JSON.stringify({ base: 'loopback mock server', turns: TURNS, keep: KEEP, samples, errors }, null, 2), { mode: 0o600 });
console.log(`report: ${path.join(OUT, 'report.json')}`);
if (errors.length) console.log(`page errors: ${errors.join(' | ')}`);
await browser.close();
