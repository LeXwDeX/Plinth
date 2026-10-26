#!/usr/bin/env node
/**
 * Real-browser regression for #311: a picture already on the 2D canvas stays selectable with the select tool
 * after the canvas is resized (参数 → 输出尺寸), in Chromium and WebKit, through the real UI.
 *
 * Needs a local server with mock providers and mock login (no credentials, no paid calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18311 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18311 node scripts/test-canvas-resize-select.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1440x900,1920x1080"),
 * OUT (screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH. Every request outside BASE
 * is refused and counted.
 *
 * Order checked per engine and viewport, each click a real pointer click on the picture where no other layer
 * and no page element covers it: select before resizing; shrink, then click; grow, then click; a top-left
 * anchored shrink that leaves the picture partly outside the canvas, then a click on the part inside and on the
 * part outside; undo and redo, each followed by a click; a click after the autosave and after a reload; a locked
 * picture stays unselectable after a resize and becomes selectable again once unlocked; two grouped pictures are
 * selected together after a resize. "Selected" means both the layers panel (the store) and the Konva
 * Transformer (the handles) name exactly the expected layers.
 *
 * Pixel checks (the stage's layer canvases composited in drawing order and read back): with the picture partly
 * and then fully outside the canvas, its selection handles are drawn exactly as when it is selected inside the
 * canvas (above the outside dim, #311), while the picture's own pixels outside the canvas stay dimmed and those
 * inside do not. Screenshots of the inside, partly outside and fully outside selections go to OUT.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { deflateSync } from 'node:zlib';
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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18311');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1440x900,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'canvas-resize-select-'));
mkdirSync(OUT, { recursive: true });
/**
 * The pictures: a solid 256x256 colour, so a patch of it reads the same at any zoom and the outside dim shows as a
 * plain factor; the second (grouped) picture is the repository's noise fixture.
 */
function solidPng(width, height, [r, g, b]) {
  const table = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
  const crc = (bytes) => { let c = 0xffffffff; for (const byte of bytes) c = table[(c ^ byte) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const head = Buffer.alloc(4); head.writeUInt32BE(data.length);
    const tail = Buffer.alloc(4); tail.writeUInt32BE(crc(body));
    return Buffer.concat([head, body, tail]);
  };
  const header = Buffer.alloc(13); header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 2;
  const row = Buffer.alloc(1 + width * 3);
  for (let x = 0; x < width; x += 1) row.set([r, g, b], 1 + x * 3);
  const raw = Buffer.concat(Array.from({ length: height }, () => row));
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', header), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}
const SOLID = path.join(OUT, 'solid-256.png');
writeFileSync(SOLID, solidPng(256, 256, [200, 120, 60]));
const NOISE = path.join(root, 'internal/assets/local/testdata/raster-noise.png');
const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const MOCK_LOGIN = application.runtime.http.legacy_routes.auth_mock;
const results = [];

async function record(engine, viewport, name, fn) {
  const started = Date.now();
  try {
    const note = await fn();
    results.push({ engine, viewport, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
  } catch (error) {
    results.push({ engine, viewport, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
  }
}

/** The session's layers, canvas size and view as the page keeps them. */
const board = (page) => page.evaluate(() => {
  const id = new URLSearchParams(location.search).get('session');
  const state = JSON.parse(localStorage.getItem('canvas-store') || '{}').state || {};
  return {
    size: state.canvasSizeBySession?.[id],
    sizeDirty: state._canvasSizeDirtyBySession?.[id],
    view: state.viewportBySession?.[id],
    nodes: (state.nodesBySession?.[id] ?? []).map((n) => ({ id: n.id, x: n.x, y: n.y, width: n.width, height: n.height, zIndex: n.zIndex, blank: n.blank === true, visible: n.visible !== false, locked: n.locked === true, groupId: n.groupId ?? null, rotation: n.rotation ?? 0 })),
  };
});
const pictures = async (page) => (await board(page)).nodes.filter((n) => !n.blank).sort((a, b) => a.zIndex - b.zIndex);

/** What is selected: the layers panel rows (the store) and the Transformer's nodes (the handles on the stage). */
const selection = (page) => page.evaluate(() => {
  const rows = [...document.querySelectorAll('[data-testid=layer-row]')]
    .filter((row) => row.querySelector('button[aria-pressed]')?.getAttribute('aria-pressed') === 'true')
    .map((row) => row.getAttribute('data-node'));
  const stage = window.Konva?.stages?.find((item) => item.container().closest('[data-canvas-drawing-area]'));
  const handles = stage?.find('Transformer')[0]?.nodes().map((node) => node.id().replace(/^node-/, '')) ?? null;
  return { rows: rows.sort(), handles: handles ? handles.sort() : null };
});

async function expectSelected(page, ids, label) {
  const { rows, handles } = await selection(page);
  const want = [...ids].sort();
  assert.deepEqual(rows, want, `${label}: layers panel shows ${JSON.stringify(rows)}`);
  assert.deepEqual(handles, want, `${label}: handles on ${JSON.stringify(handles)}`);
}

const CORNERS = { 'top-left': [0, 0], 'top-right': [1, 0], 'bottom-left': [0, 1], 'bottom-right': [1, 1] };

/**
 * What the stage shows in each rectangle (stage container pixels): the layer canvases composited in their DOM
 * (= drawing) order, read back pixel for pixel. `max` is the brightest channel of the brightest pixel, `mean` the
 * average brightness; colours are weighted by alpha.
 */
const stagePixels = (page, rects) => page.evaluate((list) => {
  const stage = window.Konva.stages.find((item) => item.container().closest('[data-canvas-drawing-area]'));
  const canvases = [...stage.content.querySelectorAll('canvas')];
  return list.map((rect) => {
    const scale = canvases[0].width / canvases[0].getBoundingClientRect().width;
    const x = Math.floor(rect.x * scale); const y = Math.floor(rect.y * scale);
    const w = Math.max(1, Math.ceil(rect.width * scale)); const h = Math.max(1, Math.ceil(rect.height * scale));
    const out = document.createElement('canvas');
    out.width = w; out.height = h;
    const context = out.getContext('2d');
    for (const canvas of canvases) context.drawImage(canvas, x, y, w, h, 0, 0, w, h);
    const data = context.getImageData(0, 0, w, h).data;
    let max = 0; let sum = 0;
    for (let i = 0; i < data.length; i += 4) {
      const value = Math.max(data[i], data[i + 1], data[i + 2]) * data[i + 3] / 255;
      max = Math.max(max, value);
      sum += value;
    }
    return { max, mean: sum / (w * h) };
  });
}, rects);

/**
 * The Transformer's corner handles as drawn: the middle 3x3 pixels of each handle, which are its opaque fill
 * whatever the sub-pixel position (the 1 px border and the pixels around it vary with that, so they are left out).
 */
async function handlePixels(page) {
  const rects = await page.evaluate((names) => {
    const stage = window.Konva.stages.find((item) => item.container().closest('[data-canvas-drawing-area]'));
    const handles = stage.find('Transformer')[0];
    return Object.fromEntries(names.map((name) => {
      const rect = handles.findOne(`.${name}`)?.getClientRect();
      return [name, rect ? { x: rect.x + rect.width / 2 - 1.5, y: rect.y + rect.height / 2 - 1.5, width: 3, height: 3 } : null];
    }));
  }, Object.keys(CORNERS));
  const names = Object.keys(rects).filter((name) => rects[name]);
  const pixels = await stagePixels(page, names.map((name) => rects[name]));
  return Object.fromEntries(names.map((name, index) => [name, { rect: rects[name], ...pixels[index] }]));
}

/** A small patch of the picture at fractions (fx, fy) of its box, as drawn (stage container pixels). */
async function picturePatch(page, id, fx, fy) {
  const state = await board(page);
  const node = state.nodes.find((n) => n.id === id);
  const x = state.view.panX + (node.x + node.width * fx) * state.view.zoom;
  const y = state.view.panY + (node.y + node.height * fy) * state.view.zoom;
  const [pixels] = await stagePixels(page, [{ x: x - 5, y: y - 5, width: 10, height: 10 }]);
  return pixels;
}

/**
 * Which side of the canvas frame a world point is on: 'inside', 'outside', or 'edge' when it is within `margin`
 * screen pixels of the frame (a patch there straddles the dim's edge).
 */
async function frameSide(page, id, fx, fy, margin = 0) {
  const state = await board(page);
  const node = state.nodes.find((n) => n.id === id);
  const frame = state.nodes.find((n) => n.blank);
  const wx = node.x + node.width * fx; const wy = node.y + node.height * fy;
  const pad = margin / state.view.zoom;
  const within = (inset) => wx > frame.x + inset && wx < frame.x + frame.width - inset && wy > frame.y + inset && wy < frame.y + frame.height - inset;
  if (within(pad)) return 'inside';
  return within(-pad) ? 'edge' : 'outside';
}
const inFrame = async (page, id, fx, fy) => (await frameSide(page, id, fx, fy)) === 'inside';

/**
 * #311: with the picture selected, every corner handle is as bright as the handles of the same picture selected
 * inside the canvas (the outside dim must not cover the selection), while the picture's own pixels outside the
 * canvas stay dimmed and those inside do not.
 */
async function expectHandlesUndimmed(page, id, reference, label) {
  const handles = await handlePixels(page);
  const notes = [];
  let outsideCorners = 0;
  for (const [name, [fx, fy]] of Object.entries(CORNERS)) {
    const handle = handles[name];
    assert(handle, `${label}: no ${name} handle`);
    const outside = !(await inFrame(page, id, fx, fy));
    outsideCorners += outside ? 1 : 0;
    assert(reference.handles[name].mean >= 15, `the ${name} handle's fill is too dark to tell dimming apart`);
    const ratio = handle.mean / reference.handles[name].mean;
    notes.push(`${name}${outside ? '(outside)' : ''} ${ratio.toFixed(2)}`);
    assert(ratio >= 0.9, `${label}: the ${name} handle ${outside ? 'outside' : 'inside'} the canvas is drawn at ${ratio.toFixed(2)} of its brightness inside the canvas`);
  }
  assert(outsideCorners > 0, `${label}: no handle is outside the canvas`);
  let dimmedPatches = 0;
  for (const [fx, fy] of [[0.2, 0.2], [0.8, 0.8], [0.5, 0.5]]) {
    const side = await frameSide(page, id, fx, fy, 8);
    if (side === 'edge') continue;
    const patch = await picturePatch(page, id, fx, fy);
    const ratio = patch.mean / reference.patches[`${fx},${fy}`].mean;
    const outside = side === 'outside';
    notes.push(`picture ${fx},${fy}${outside ? '(outside)' : ''} ${ratio.toFixed(2)}`);
    dimmedPatches += outside ? 1 : 0;
    if (outside) assert(ratio <= 0.75, `${label}: the picture outside the canvas is not dimmed (${ratio.toFixed(2)})`);
    else assert(ratio >= 0.85, `${label}: the picture inside the canvas is dimmed (${ratio.toFixed(2)})`);
  }
  assert(dimmedPatches > 0, `${label}: no part of the picture outside the canvas was checked`);
  return notes.join(', ');
}

/**
 * A page point on the picture where the pointer reaches the stage: inside the window, no page element above it
 * and no other visible layer above it (by the stored geometry). `where` limits the points to inside or outside
 * the canvas frame.
 */
async function pointOn(page, id, where = 'any') {
  const state = await board(page);
  const node = state.nodes.find((n) => n.id === id);
  const frame = state.nodes.find((n) => n.blank);
  assert(node && state.view, `no layer ${id} or no view`);
  const area = await page.locator('[data-canvas-drawing-area] .konvajs-content').first().boundingBox();
  const size = page.viewportSize();
  const fractions = [0.5, 0.3, 0.7, 0.15, 0.85, 0.05, 0.95];
  for (const fx of fractions) for (const fy of fractions) {
    const wx = node.x + node.width * fx;
    const wy = node.y + node.height * fy;
    const inFrame = frame ? wx > frame.x && wx < frame.x + frame.width && wy > frame.y && wy < frame.y + frame.height : true;
    if ((where === 'inside' && !inFrame) || (where === 'outside' && inFrame)) continue;
    const covered = state.nodes.some((other) => other.id !== id && !other.blank && other.visible && other.zIndex > node.zIndex && !other.rotation
      && wx >= other.x && wx <= other.x + other.width && wy >= other.y && wy <= other.y + other.height);
    if (covered) continue;
    const x = area.x + state.view.panX + wx * state.view.zoom;
    const y = area.y + state.view.panY + wy * state.view.zoom;
    if (x < 1 || y < 1 || x > size.width - 1 || y > size.height - 1) continue;
    const onStage = await page.evaluate(({ px, py }) => Boolean(document.elementFromPoint(px, py)?.closest('.konvajs-content')), { px: x, py: y });
    if (onStage) return { x, y };
  }
  return null;
}

/** Click the picture; when the resize moved it out of view, 回到画布 brings the content back first. */
async function clickPicture(page, id, where = 'any') {
  let point = await pointOn(page, id, where);
  if (!point) {
    await page.locator(`[data-testid=canvas-zoom-controls] button[aria-label="${text('ui.canvas.toolbar.recenter')}"]`).click();
    await page.waitForTimeout(400);
    point = await pointOn(page, id, where);
  }
  assert(point, `no reachable point on ${id} (${where})`);
  // Stay out of Konva's double-click window, which opens the picture's detail view.
  await page.waitForTimeout(450);
  await page.mouse.click(point.x, point.y);
  await page.waitForTimeout(300);
}

/** Click empty stage (no layer under the pointer), which clears the selection with the select tool. */
async function clickEmpty(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  for (const [fx, fy] of [[0.97, 0.5], [0.5, 0.92], [0.2, 0.92], [0.97, 0.9]]) {
    const point = { x: area.x + area.width * fx, y: area.y + area.height * fy };
    const empty = await page.evaluate(({ x, y }) => {
      if (!document.elementFromPoint(x, y)?.closest('.konvajs-content')) return false;
      const stage = window.Konva?.stages?.find((item) => item.container().closest('[data-canvas-drawing-area]'));
      const rect = stage.container().getBoundingClientRect();
      return !stage.getIntersection({ x: x - rect.left, y: y - rect.top });
    }, point);
    if (!empty) continue;
    await page.waitForTimeout(450);
    await page.mouse.click(point.x, point.y);
    await page.waitForTimeout(250);
    return;
  }
  throw new Error('no empty stage point');
}

async function openParams(page) {
  if (!(await page.locator('[data-testid=composer-params-panel]').isVisible().catch(() => false))) {
    await page.click('[data-testid=composer-params]');
    await page.locator('[data-testid=composer-params-panel]').waitFor();
  }
}
async function closeParams(page) {
  await page.keyboard.press('Escape');
  await page.locator('[data-testid=composer-params-panel]').waitFor({ state: 'hidden' });
}

const sizeBoxes = (page) => page.locator('[data-testid=composer-params-panel] [role=combobox]');

/** The 参数 panel's pixel size control: the combobox whose options are W×H sizes (its index and the sizes). */
async function sizeOptions(page) {
  for (let index = 0; ; index += 1) {
    await openParams(page);
    if (index >= await sizeBoxes(page).count()) break;
    await sizeBoxes(page).nth(index).click();
    await page.locator('[role=option]:visible').first().waitFor();
    const options = await page.locator('[role=option]:visible').allInnerTexts();
    const sizes = options.map((option) => option.match(/(\d+)×(\d+)/)).filter(Boolean).map((m) => ({ label: m[0], width: Number(m[1]), height: Number(m[2]) }));
    await page.keyboard.press('Escape');
    await page.waitForTimeout(200);
    if (sizes.length > 1) {
      if (await page.locator('[data-testid=composer-params-panel]').isVisible()) await closeParams(page);
      return { index, sizes };
    }
  }
  throw new Error('the chosen 2D model offers no pixel sizes');
}

let sizeIndex = -1;
async function resizeTo(page, size, anchor = 'center') {
  await openParams(page);
  await page.locator(`[data-testid=canvas-resize-anchor] button[data-anchor="${anchor}"]`).click();
  await sizeBoxes(page).nth(sizeIndex).click();
  await page.locator('[role=option]:visible', { hasText: size.label }).first().click();
  await page.waitForTimeout(200);
  await closeParams(page);
  const now = (await board(page)).size;
  assert.deepEqual(now, { width: size.width, height: size.height }, `canvas is ${JSON.stringify(now)} after choosing ${size.label}`);
}

async function upload(page, count, file = SOLID) {
  const before = (await pictures(page)).length;
  await page.locator('input[type=file]').last().setInputFiles(file);
  await page.waitForFunction((expected) => {
    const id = new URLSearchParams(location.search).get('session');
    const state = JSON.parse(localStorage.getItem('canvas-store') || '{}').state;
    return (state?.nodesBySession?.[id] ?? []).filter((n) => !n.blank && n.imageUrl).length >= expected;
  }, before + count, { timeout: 20000 });
  await page.waitForTimeout(600);
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
  const outside = [];
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  const user = `resize-select-${engine}-${viewport}-${Date.now()}`;
  const login = await context.request.post(`${BASE}${MOCK_LOGIN}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(login.status(), 200, 'mock login');
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(`${BASE}/`);
  await page.waitForSelector('[data-canvas-drawing-area]', { timeout: 30000 });
  await upload(page, 1);
  await page.click('[data-testid=workspace-model-picker]');
  await page.locator('[data-testid=workspace-model-picker-image] [role=menu] [role=menuitemradio]').first().click();
  await page.keyboard.press('Escape');
  await page.click('[data-testid=canvas-tool-select]');
  const [picture] = await pictures(page);
  const { index, sizes } = await sizeOptions(page);
  sizeIndex = index;
  const start = (await board(page)).size;
  const byArea = [...sizes].sort((a, b) => a.width * a.height - b.width * b.height);
  const smaller = byArea.find((size) => size.width * size.height < start.width * start.height);
  const larger = byArea.at(-1);
  const initial = sizes.find((size) => size.width === start.width && size.height === start.height);
  assert(smaller && initial && larger.width * larger.height > start.width * start.height, `no smaller, equal and larger size than ${JSON.stringify(start)}`);
  const step = (name, fn) => record(engine, viewport, name, fn);

  const reference = { handles: null, patches: {} };
  await step('select before resizing', async () => {
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'before');
    // The picture is inside the canvas: how its handles and pixels look selected there.
    reference.handles = await handlePixels(page);
    for (const [fx, fy] of [[0.2, 0.2], [0.8, 0.8], [0.5, 0.5]]) reference.patches[`${fx},${fy}`] = await picturePatch(page, picture.id, fx, fy);
    assert(Object.keys(reference.handles).length === 4, 'no corner handles on the selected picture');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-1-inside.png`) });
  });
  await step(`shrink ${start.width}x${start.height} → ${smaller.label}, then select`, async () => {
    await resizeTo(page, smaller);
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'after shrink');
  });
  await step(`grow → ${larger.label}, then select`, async () => {
    await resizeTo(page, larger);
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'after grow');
  });
  await step('top-left anchored shrink leaves the picture partly outside; both parts select it', async () => {
    await resizeTo(page, initial);
    await resizeTo(page, smaller, 'top-left');
    const state = await board(page);
    const node = state.nodes.find((n) => n.id === picture.id);
    const frame = state.nodes.find((n) => n.blank);
    assert(node.x + node.width > frame.x + frame.width || node.y + node.height > frame.y + frame.height, 'the picture is not partly outside');
    for (const where of ['inside', 'outside']) {
      await clickEmpty(page);
      await clickPicture(page, picture.id, where);
      await expectSelected(page, [picture.id], `click ${where} the canvas`);
    }
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-2-partly-outside.png`) });
    const note = await expectHandlesUndimmed(page, picture.id, reference, 'partly outside');
    return note;
  });
  await step('undo the resize, then select; redo, then select', async () => {
    await page.click('[data-testid=canvas-tool-undo]');
    await page.waitForTimeout(300);
    assert.deepEqual((await board(page)).size, start, 'undo did not restore the size');
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'after undo');
    await page.click('[data-testid=canvas-tool-redo]');
    await page.waitForTimeout(300);
    assert.deepEqual((await board(page)).size, { width: smaller.width, height: smaller.height }, 'redo did not restore the size');
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'after redo');
  });
  await step('select after the autosave and after a reload', async () => {
    await page.waitForFunction(() => {
      const id = new URLSearchParams(location.search).get('session');
      return JSON.parse(localStorage.getItem('canvas-store') || '{}').state?._canvasSizeDirtyBySession?.[id] === false;
    }, null, { timeout: 15000 });
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'after autosave');
    await page.reload();
    await page.waitForSelector('[data-canvas-drawing-area]', { timeout: 30000 });
    await page.waitForFunction(() => document.querySelectorAll('[data-testid=layer-row]').length >= 2, null, { timeout: 15000 });
    await page.waitForTimeout(800);
    await page.click('[data-testid=canvas-tool-select]');
    assert.deepEqual((await board(page)).size, { width: smaller.width, height: smaller.height }, 'reload lost the size');
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'after reload');
  });
  await step('a locked picture stays unselectable after a resize; unlocked it selects again', async () => {
    await resizeTo(page, larger);
    await page.locator(`[data-testid=layer-row][data-node="${picture.id}"] button[aria-label="${text('ui.canvas.layers.lock')}"]`).click();
    await resizeTo(page, smaller);
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, [], 'locked');
    await page.locator(`[data-testid=layer-row][data-node="${picture.id}"] button[aria-label="${text('ui.canvas.layers.unlock')}"]`).click();
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'unlocked');
  });
  await step('fully outside the canvas after a top-left anchored shrink: selected, handles not dimmed, picture dimmed', async () => {
    await resizeTo(page, larger);
    await resizeTo(page, smaller, 'top-left');
    for (const [fx, fy] of [[0, 0], [1, 1], [0.5, 0.5]]) assert(!(await inFrame(page, picture.id, fx, fy)), 'the picture is not fully outside');
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, [picture.id], 'fully outside');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-3-fully-outside.png`) });
    const note = await expectHandlesUndimmed(page, picture.id, reference, 'fully outside');
    return note;
  });
  await step('two grouped pictures are selected together after a resize', async () => {
    await upload(page, 1, NOISE);
    const [, second] = await pictures(page);
    // Move the new picture off the first one so both can be clicked.
    const from = await pointOn(page, second.id);
    const zoom = (await board(page)).view.zoom;
    await page.waitForTimeout(450);
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move(from.x + 200 * zoom, from.y + 200 * zoom, { steps: 6 });
    await page.mouse.move(from.x + 400 * zoom, from.y + 400 * zoom, { steps: 6 });
    await page.mouse.up();
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    const other = await pointOn(page, second.id);
    await page.waitForTimeout(450);
    await page.keyboard.down('Shift');
    await page.mouse.click(other.x, other.y);
    await page.keyboard.up('Shift');
    await page.keyboard.press('Control+g');
    await page.waitForTimeout(300);
    const grouped = (await board(page)).nodes.filter((n) => n.groupId).map((n) => n.id);
    assert.deepEqual(grouped.sort(), [picture.id, second.id].sort(), 'the two pictures were not grouped');
    await resizeTo(page, larger);
    await clickEmpty(page);
    await clickPicture(page, picture.id);
    await expectSelected(page, grouped, 'group after grow');
    await resizeTo(page, smaller, 'bottom-right');
    await clickEmpty(page);
    await clickPicture(page, second.id);
    await expectSelected(page, grouped, 'group after anchored shrink');
  });

  await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}.png`) });
  results.push({ engine, viewport, name: 'no page errors and no requests outside BASE', passed: errors.length === 0 && outside.length === 0,
    ...(errors.length || outside.length ? { error: [...errors, ...outside].join(' | ').slice(0, 400) } : {}) });
  await context.close();
}

try {
  for (const engine of ENGINES) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      results.push({ engine, viewport: '-', name: `browser ${browser.version()}`, passed: true });
      for (const viewport of VIEWPORTS) await checks(engine, browser, viewport);
    } finally {
      await browser.close();
    }
  }
} finally {
  writeFileSync(path.join(OUT, 'report.json'), JSON.stringify({ base: 'loopback mock server', results }, null, 2), { mode: 0o600 });
}
const failed = results.filter((item) => !item.passed);
for (const item of results) console.log(`${item.passed ? 'PASS' : 'FAIL'} ${item.engine} ${item.viewport} ${item.name}${item.error ? ` — ${item.error}` : ''}`);
console.log(JSON.stringify({ passed: failed.length === 0, cases: results.length, failed: failed.length, out: OUT }));
process.exit(failed.length === 0 ? 0 : 1);
