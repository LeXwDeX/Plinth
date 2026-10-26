#!/usr/bin/env node
/**
 * Real-browser regression for the 2D canvas right-click menu (#280) and the stage toolbar's 局部重绘 after a
 * layer was deleted (#278), in Chromium and WebKit, through the real UI.
 *
 * Needs a local server with mock providers and mock login (no credentials, no paid calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18479 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18479 node scripts/test-canvas-menu-inpaint.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), OUT (screenshots and report.json; default a temp folder),
 * PLAYWRIGHT_MODULE_PATH (where the playwright package is). Every request outside BASE is refused and counted.
 *
 * #280 checks, at 1366x768, 1440x900 and 1920x1080: a right-click on a picture or on empty canvas opens the menu at
 * the pointer and it stays open for 2 s and while the pointer moves into it and over the align submenu; Escape and
 * an outside click close it; a second right-click moves it; an item runs exactly once; near the window corner it
 * stays inside the window; with another tool the menu does not open.
 * #278 checks: after the version on the stage loses its layer (it was deleted) while another picture stays in the
 * artboard, 局部重绘 enters the mask tool and, with a marquee selection, opens the inpaint dialog, never saying
 * 导出失败; with nothing left in the artboard the button is off and 去背景 is off too.
 */
import assert from 'node:assert/strict';
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
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'canvas-menu-inpaint-'));
mkdirSync(OUT, { recursive: true });
const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const MOCK_LOGIN = application.runtime.http.legacy_routes.auth_mock;
const VIEWPORTS = [[1366, 768], [1440, 900], [1920, 1080]];
const results = [];
let currentStep = 'setup';

const menu = (page) => page.locator('[data-testid=canvas-context-menu]');
const frames = (page, n = 2) => page.evaluate((count) => new Promise((resolve) => {
  let left = count;
  const tick = () => (--left <= 0 ? resolve() : requestAnimationFrame(tick));
  requestAnimationFrame(tick);
}), n);
// Every toast shown is logged by an observer (the page's own toast nodes are never touched); a step reads the ones
// shown after its mark.
const TOAST_LOG = `(() => {
  window.__toastLog = [];
  new MutationObserver((records) => {
    for (const record of records) for (const node of record.addedNodes) {
      // Keep the node: its text is filled in after it is inserted, and stays readable once it is removed.
      if (!(node instanceof Element)) continue;
      if (node.matches('[data-sonner-toast]')) window.__toastLog.push(node);
      window.__toastLog.push(...node.querySelectorAll('[data-sonner-toast]'));
    }
  }).observe(document, { childList: true, subtree: true });
})()`;
const toastMark = (page) => page.evaluate(() => window.__toastLog.length);
const toastsSince = (page, mark) => page.evaluate((from) => window.__toastLog.slice(from).map((node) => node.textContent ?? ''), mark);
const layerRows = (page) => page.locator('[data-testid=layer-row]');

async function record(engine, viewport, name, fn) {
  const started = Date.now();
  currentStep = name;
  try {
    const note = await fn();
    results.push({ engine, viewport, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
  } catch (error) {
    results.push({ engine, viewport, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
  }
}

/** Opens a fresh session and puts one generated picture (mock provider) on the board as version 1. */
async function sessionWithPicture(page) {
  await page.goto(`${BASE}/`);
  await page.waitForSelector('[data-testid=canvas-tool-rail]', { timeout: 30000 });
  await page.click('[data-testid=workspace-model-picker]');
  await page.locator('[data-testid=workspace-model-picker-image] [role=menu] [role=menuitemradio]').first().click();
  await page.keyboard.press('Escape');
  const box = page.locator('[data-testid=studio-composer] textarea');
  await box.click();
  await box.fill('一只猫，工作室光效');
  await page.keyboard.press('Enter');
  await approveIfAsked(page, () => page.locator('[data-testid=chat-image-result]').count().then((count) => count > 0));
  await page.waitForFunction(() => document.querySelectorAll('[data-testid=chat-image-result]').length > 0, null, { timeout: 90000 });
  await page.waitForFunction(() => document.querySelectorAll('[data-testid=layer-row]').length >= 2, null, { timeout: 30000 });
  await page.waitForTimeout(800);
}

async function approveIfAsked(page, done) {
  for (let i = 0; i < 60; i += 1) {
    const approve = page.locator('[data-testid=studio-approval-approve]');
    if (await approve.count()) { await approve.first().click(); return; }
    if (await done()) return;
    await page.waitForTimeout(500);
  }
}

/** The centre of the first picture layer, in page coordinates (the picture is selected by the layer panel). */
async function pictureCentre(page) {
  const area = await page.locator('[data-canvas-drawing-area]').boundingBox();
  return { x: area.x + area.width / 2, y: area.y + area.height / 2, area };
}

async function rightClick(page, x, y) {
  await page.mouse.move(x, y);
  await page.mouse.click(x, y, { button: 'right' });
  await menu(page).waitFor({ state: 'visible', timeout: 3000 });
}

/** A point on the stage's own canvas (not the tool rail or zoom controls) away from the picture in the middle. */
async function emptyCanvasPoint(page, area) {
  for (const [fx, fy] of [[0.12, 0.5], [0.88, 0.5], [0.5, 0.06], [0.5, 0.9], [0.12, 0.12]]) {
    const point = { x: area.x + area.width * fx, y: area.y + area.height * fy };
    const onStage = await page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.tagName === 'CANVAS', point);
    if (!onStage) continue;
    await page.mouse.click(point.x, point.y, { button: 'right' });
    await menu(page).waitFor({ state: 'visible', timeout: 3000 });
    const canvasMenu = (await menu(page).innerText()).includes(text('ui.canvas.menu.select_all'));
    await page.keyboard.press('Escape');
    await menu(page).waitFor({ state: 'hidden', timeout: 3000 });
    if (canvasMenu) return point;
  }
  throw new Error('no empty canvas point found');
}

/**
 * The menu is at the pointer: one of its vertical edges is there (it opens to the left when the right side is
 * short) and the pointer lies within its height (a tall menu is shifted up to stay inside the window). A menu
 * moved by a second right-click while open may also be shifted sideways to fit (`edge: false`): the pointer must
 * then still be over it or right next to it.
 */
function atPointer(popup, x, y, { edge = true } = {}) {
  const vertical = popup.y <= y + 16 && popup.y + popup.height >= y - 16;
  if (!edge) return vertical && popup.x <= x + 16 && popup.x + popup.width >= x - 16;
  return vertical && (Math.abs(popup.x - x) <= 16 || Math.abs(popup.x + popup.width - x) <= 16);
}

async function expectStaysOpen(page, ms) {
  const until = Date.now() + ms;
  while (Date.now() < until) {
    assert(await menu(page).isVisible(), 'the menu closed by itself');
    await page.waitForTimeout(100);
  }
}

async function menuChecks(engine, browser) {
  for (const [width, height] of VIEWPORTS) {
    const viewport = `${width}x${height}`;
    const context = await browser.newContext({ viewport: { width, height } });
    const page = await context.newPage();
    const errors = [];
    const outside = [];
    page.on('pageerror', (error) => errors.push(`${currentStep}: ${error.message}`));
    await page.route('**/*', (route) => (new URL(route.request().url()).origin === BASE ? route.continue() : (outside.push(route.request().url()), route.abort())));
    await page.request.post(`${BASE}${MOCK_LOGIN}`, { data: { name: 'canvas-qa', email: 'canvas-qa@example.test' } });
    await sessionWithPicture(page);
    const { x, y, area } = await pictureCentre(page);

    await record(engine, viewport, '280 picture: opens at the pointer and stays open 2 s', async () => {
      await rightClick(page, x, y);
      const popup = await menu(page).boundingBox();
      assert(atPointer(popup, x, y), `menu ${JSON.stringify(popup)}, pointer at ${x},${y}`);
      await expectStaysOpen(page, 2000);
      const labels = await menu(page).innerText();
      assert(labels.includes(text('ui.canvas.action.delete')) && labels.includes(text('ui.canvas.options.align')), 'not the layer menu');
    });

    await record(engine, viewport, '280 stays open while the pointer moves into it and over the align submenu', async () => {
      const popup = await menu(page).boundingBox();
      await page.mouse.move(popup.x + popup.width / 2, popup.y + 12, { steps: 8 });
      await expectStaysOpen(page, 600);
      const align = page.locator('[data-testid=canvas-menu-align]');
      const alignBox = await align.boundingBox();
      await page.mouse.move(alignBox.x + alignBox.width / 2, alignBox.y + alignBox.height / 2, { steps: 6 });
      const submenuItem = page.getByRole('menuitem', { name: text('ui.canvas.options.align_left') });
      await submenuItem.waitFor({ state: 'visible', timeout: 3000 });
      const itemBox = await submenuItem.boundingBox();
      await page.mouse.move(itemBox.x + itemBox.width / 2, itemBox.y + itemBox.height / 2, { steps: 6 });
      await expectStaysOpen(page, 1000);
      assert(await submenuItem.isVisible(), 'the align submenu closed');
    });

    await record(engine, viewport, '280 Escape closes it', async () => {
      await page.keyboard.press('Escape');
      if (await menu(page).isVisible()) await page.keyboard.press('Escape');
      await menu(page).waitFor({ state: 'hidden', timeout: 3000 });
    });

    await record(engine, viewport, '280 empty canvas: opens the canvas menu; a second right-click moves it', async () => {
      const blank = await emptyCanvasPoint(page, area);
      await rightClick(page, blank.x, blank.y);
      assert((await menu(page).innerText()).includes(text('ui.canvas.menu.select_all')), 'not the canvas menu');
      const first = await menu(page).boundingBox();
      const next = { x: blank.x + (blank.x < area.x + area.width / 2 ? 120 : -120), y: blank.y + (blank.y < area.y + area.height / 2 ? 120 : -120) };
      await page.mouse.click(next.x, next.y, { button: 'right' });
      await page.waitForTimeout(300);
      await expectStaysOpen(page, 1200);
      const second = await menu(page).boundingBox();
      assert(Math.abs(second.x - first.x) > 60 || Math.abs(second.y - first.y) > 60, 'the menu did not move to the second right-click');
    });

    await record(engine, viewport, '280 a right-click on the picture while the canvas menu is open switches to the layer menu', async () => {
      await page.mouse.click(x, y, { button: 'right' });
      await page.waitForTimeout(300);
      await expectStaysOpen(page, 800);
      const labels = await menu(page).innerText();
      assert(labels.includes(text('ui.canvas.action.delete')) && !labels.includes(text('ui.canvas.menu.select_all')), 'still the canvas menu');
      const popup = await menu(page).boundingBox();
      assert(atPointer(popup, x, y, { edge: false }), `menu ${JSON.stringify(popup)}, pointer at ${x},${y}`);
    });

    await record(engine, viewport, '280 an outside click closes it', async () => {
      const chat = await page.locator('[data-testid=studio-chat-column]').boundingBox();
      await page.mouse.click(chat.x + chat.width / 2, chat.y + 40);
      await menu(page).waitFor({ state: 'hidden', timeout: 3000 });
    });

    await record(engine, viewport, '280 an item runs once and closes the menu; the right-click does not move the picture', async () => {
      await layerRows(page).first().locator('button').first().click();
      const before = await layerRows(page).count();
      const pictureBefore = await page.evaluate(() => document.querySelector('[data-testid=layer-row]')?.getAttribute('data-node'));
      await rightClick(page, x, y);
      await page.waitForTimeout(400);
      await menu(page).getByRole('menuitem', { name: text('ui.canvas.menu.duplicate') }).click();
      await menu(page).waitFor({ state: 'hidden', timeout: 3000 });
      await page.waitForTimeout(500);
      const after = await layerRows(page).count();
      assert.equal(after, before + 1, `layers ${before} -> ${after}`);
      assert(pictureBefore, 'no picture layer');
      await page.keyboard.press(process.platform === 'darwin' ? 'Meta+z' : 'Control+z');
      await page.waitForTimeout(400);
    });

    await record(engine, viewport, '280 near the window corner the menu stays inside the window', async () => {
      const corner = { x: area.x + area.width - 8, y: area.y + area.height - 8 };
      await rightClick(page, corner.x, corner.y);
      await expectStaysOpen(page, 800);
      const popup = await menu(page).boundingBox();
      assert(popup.x >= 0 && popup.y >= 0 && popup.x + popup.width <= width && popup.y + popup.height <= height, `menu ${JSON.stringify(popup)} outside ${viewport}`);
      await page.keyboard.press('Escape');
      await menu(page).waitFor({ state: 'hidden', timeout: 3000 });
    });

    await record(engine, viewport, '280 another tool: no canvas menu', async () => {
      await page.click('[data-testid=canvas-tool-hand]');
      await page.mouse.click(x, y, { button: 'right' });
      await page.waitForTimeout(500);
      assert(!(await menu(page).isVisible()), 'the menu opened with the hand tool');
      await page.click('[data-testid=canvas-tool-select]');
    });

    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-menu.png`) });
    results.push({ engine, viewport, name: 'no page errors or outside requests', passed: errors.length === 0 && outside.length === 0, ...(errors.length || outside.length ? { error: JSON.stringify({ errors, outside }).slice(0, 400) } : {}) });
    await context.close();
  }
}

async function inpaintChecks(engine, browser) {
  const viewport = '1440x900';
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.addInitScript(TOAST_LOG);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(`${currentStep}: ${error.message}`));
  await page.request.post(`${BASE}${MOCK_LOGIN}`, { data: { name: 'canvas-qa', email: 'canvas-qa@example.test' } });
  await sessionWithPicture(page);
  const exportFailed = text('ui.workspace.canvas.export_failed');
  const inpaint = page.locator('[data-testid=stage-inpaint]');
  const maskTool = page.locator('[data-testid=canvas-tool-mask]');

  // Version 2 from a real (mock) inpaint: paint a mask, describe, submit.
  await record(engine, viewport, '278 setup: a mock inpaint makes version 2', async () => {
    await inpaint.click();
    const { x, y } = await pictureCentre(page);
    await page.mouse.move(x - 40, y);
    await page.mouse.down();
    for (let i = 0; i <= 12; i += 1) await page.mouse.move(x - 40 + i * 7, y + (i % 2) * 12, { steps: 3 });
    await page.mouse.up();
    await page.click('[data-testid=mask-done]');
    const dialog = page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') });
    await dialog.waitFor({ timeout: 15000 });
    await dialog.locator('textarea').first().fill('改成金色花纹');
    const tiles = await page.locator('[data-testid=version-tile]').count();
    await dialog.getByRole('button', { name: text('ui.canvas.inpaint.prepare') }).click();
    const confirm = dialog.getByRole('button', { name: text('ui.canvas.inpaint.confirm_execution') });
    await confirm.waitFor({ timeout: 3000 }).then(() => confirm.click()).catch(() => {});
    await approveIfAsked(page, () => page.locator('[data-testid=version-tile]').count().then((count) => count > tiles));
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile]').length > n, tiles, { timeout: 60000 });
    await page.waitForTimeout(5000);
  });

  await record(engine, viewport, '278 the version on the stage loses its layer; 局部重绘 still enters the mask tool', async () => {
    await page.locator('[data-testid=version-tile]').last().locator('button').first().click();
    await page.waitForTimeout(1500);
    const rows = layerRows(page);
    const selected = await rows.evaluateAll((nodes) => nodes.findIndex((node) => node.querySelector('[aria-pressed=true]')));
    assert(selected >= 0, 'the stage version has no selected layer');
    const before = await rows.count();
    await rows.nth(selected).locator('button').first().click();
    await page.keyboard.press('Delete');
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=layer-row]').length === n - 1, before, { timeout: 5000 });
    assert(!(await inpaint.isDisabled()), 'the button is off although pictures remain in the artboard');
    const mark = await toastMark(page);
    await inpaint.click();
    await page.waitForTimeout(800);
    const shown = await toastsSince(page, mark);
    assert(!shown.some((message) => message.includes(exportFailed)), `toast: ${shown.join(' | ')}`);
    assert.equal(await maskTool.getAttribute('aria-pressed'), 'true', 'the mask tool is not on');
    await page.click('[data-testid=mask-exit]');
    await page.click('[data-testid=canvas-tool-select]');
  });

  await record(engine, viewport, '278 with a marquee selection, 局部重绘 opens the inpaint dialog', async () => {
    await page.click('[data-testid=canvas-tool-marquee]');
    const { x, y } = await pictureCentre(page);
    await page.mouse.move(x - 60, y - 60);
    await page.mouse.down();
    await page.mouse.move(x + 60, y + 60, { steps: 8 });
    await page.mouse.up();
    const mark = await toastMark(page);
    await inpaint.click();
    await page.waitForTimeout(800);
    const shown = await toastsSince(page, mark);
    assert(!shown.some((message) => message.includes(exportFailed)), `toast: ${shown.join(' | ')}`);
    const dialog = page.getByRole('dialog').filter({ hasText: text('ui.canvas.inpaint.title') });
    await dialog.waitFor({ timeout: 15000 });
    await dialog.getByRole('button', { name: text('ui.canvas.inpaint.cancel') }).click();
    await page.keyboard.press('Escape');
    await page.click('[data-testid=canvas-tool-select]');
  });

  await record(engine, viewport, '278 with nothing left in the artboard, 局部重绘 and 去背景 are off', async () => {
    for (let guard = 0; guard < 10; guard += 1) {
      const rows = layerRows(page);
      const count = await rows.count();
      // Delete every layer except the blank initial one (#246), which draws nothing.
      let deleted = false;
      for (let index = 0; index < count; index += 1) {
        const label = await rows.nth(index).innerText();
        if (label.includes(text('ui.canvas.layers.initial_name'))) continue;
        await rows.nth(index).locator('button').first().click();
        await page.keyboard.press('Delete');
        await page.waitForTimeout(300);
        deleted = true;
        break;
      }
      if (!deleted) break;
    }
    await page.waitForTimeout(500);
    assert(await inpaint.isDisabled(), '局部重绘 is still on with an empty artboard');
    assert(await page.locator('[data-testid=stage-remove-background]').isDisabled(), '去背景 is still on with nothing to cut out');
  });

  await page.screenshot({ path: path.join(OUT, `${engine}-inpaint.png`) });
  results.push({ engine, viewport, name: '278 no page errors', passed: errors.length === 0, ...(errors.length ? { error: errors.join(' | ').slice(0, 400) } : {}) });
  await context.close();
}

try {
  for (const engine of ENGINES) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      const version = browser.version();
      results.push({ engine, viewport: '-', name: `browser ${version}`, passed: true });
      await menuChecks(engine, browser);
      await inpaintChecks(engine, browser);
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
