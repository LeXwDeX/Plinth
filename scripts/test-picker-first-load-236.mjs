#!/usr/bin/env node
/**
 * Real-browser check for #236 (reworked for #405): the settings dialog behind the gear beside the send button opens on the
 * first press after a page load and stays open while the session context (levels, model list, context usage, messages) is
 * still arriving, and its chat model / thinking level tab then works. The dialog closes only on Esc, the close button or a
 * click outside; picking a level does not close it (#405: a pick applies at once and the dialog stays open). The gear node
 * is never replaced while the context loads.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid
 * calls), built from this checkout (`pnpm build` first), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18236 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18236 node scripts/test-picker-first-load-236.mjs
 * With one chat model the chat tab lists the thinking levels alone; with a second connection (LLM_CONNECTIONS, see
 * scripts/test-llm-settings.mjs) it has two columns and the model rows are exercised too.
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1440x900"), OUT (timeline of a failed case,
 * screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH. Every request outside BASE is refused.
 *
 * Per engine and viewport, every case loads the page in a fresh context and presses the gear on the first frame it
 * is on screen, while the server's answers are held back like this:
 *   no delay | the model list +900ms | the levels +900ms | the context usage +900ms | all four staggered.
 * Each case then checks that the dialog opened, stayed open (no frame without it) until the held answers had landed,
 * kept the same gear node and kept the keyboard focus inside; the chat model tab appears with the session's data and is
 * opened; then Esc closes the dialog and hands the focus back to the gear, a level pick (one PUT) applies and leaves the
 * dialog open, a click outside closes it, and three press/Esc rounds in a row open it every time. When a case fails, the
 * page's own timeline (presses, focus moves, keys, aria-expanded, dialog present) is printed, which names what closed it.
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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18236');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1440x900').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'picker-first-load-'));
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const escape = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const sessionRoute = (template) => new RegExp(`${template.split('{sessionID}').map(escape).join('[^/?]+')}(\\?.*)?$`);
const HELD = {
  model: sessionRoute(routes.chat_session_model),
  reasoning: sessionRoute(routes.chat_session_reasoning),
  context: sessionRoute(routes.chat_session_context),
  messages: sessionRoute(legacy.chat_messages),
};
const PROFILES = [
  { name: 'no delay', hold: {} },
  { name: 'model list +900ms', hold: { model: 900 } },
  { name: 'levels +900ms', hold: { reasoning: 900 } },
  { name: 'context usage +900ms', hold: { context: 900 } },
  { name: 'all staggered', hold: { reasoning: 300, messages: 500, model: 700, context: 1100 } },
];
// #405: the trigger is the gear, the panel is the dialog behind it; the chat model and levels are its chat tab.
const TRIGGER = '[data-testid=composer-settings-open]';
const DIALOG = '[data-testid=composer-settings-dialog]';
const CHAT_TAB = '[data-testid=composer-settings-tab-chat]';
const PANEL = '[data-testid=workspace-chat-model-picker-panel]';
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Runs in the page before its own scripts: a timeline of everything that can close or move the dialog. */
function installTimeline() {
  const events = [];
  window.__timeline = events;
  const stamp = () => Math.round(performance.now());
  const name = (node) => (node && node.tagName ? `${node.tagName.toLowerCase()}${node.getAttribute('data-testid') ? `#${node.getAttribute('data-testid')}` : ''}${node.getAttribute('role') ? `[${node.getAttribute('role')}]` : ''}` : String(node));
  for (const type of ['pointerdown', 'click', 'keydown']) document.addEventListener(type, (event) => events.push([stamp(), type, type === 'keydown' ? event.key : name(event.target)]), true);
  for (const type of ['focusin', 'focusout']) document.addEventListener(type, (event) => events.push([stamp(), type, name(event.target), '->', name(event.relatedTarget)]), true);
  const ids = new WeakMap();
  let count = 0;
  let last = '';
  const frame = () => {
    const trigger = document.querySelector('[data-testid=composer-settings-open]');
    if (trigger && !ids.has(trigger)) ids.set(trigger, ++count);
    const panel = document.querySelector('[data-testid=composer-settings-dialog]');
    const now = `trigger#${trigger ? ids.get(trigger) : '-'} expanded=${trigger ? trigger.getAttribute('aria-expanded') : '-'} panel=${panel ? 'yes' : 'no'}`;
    if (now !== last) { events.push([stamp(), 'state', now]); last = now; }
    requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}

const results = [];
async function record(engine, viewport, name, fn) {
  const started = Date.now();
  try {
    const note = await fn();
    results.push({ engine, viewport, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
    console.log(`PASS ${engine} ${viewport} ${name}${note ? ` | ${note}` : ''}`);
  } catch (error) {
    const message = String(error?.message ?? error).split('\n')[0];
    results.push({ engine, viewport, name, passed: false, ms: Date.now() - started, error: message });
    console.log(`FAIL ${engine} ${viewport} ${name} | ${message}`);
  }
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const seed = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
  const stamp = `picker-${engine}-${viewport}-${Date.now()}`;
  const login = await seed.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: stamp, email: `${stamp}@example.test` } });
  assert.equal(login.status(), 200, 'mock login');
  const storageState = await seed.storageState();
  await seed.close();

  for (const profile of PROFILES) {
    await record(engine, viewport, profile.name, async () => {
      const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block', storageState });
      const outside = [];
      const limited = [];
      context.on('response', (response) => { if (response.status() === 429 && response.url().startsWith(BASE)) limited.push(new URL(response.url()).pathname); });
      await context.addInitScript(installTimeline);
      await context.route('**/*', async (route) => {
        const url = route.request().url();
        if (!url.startsWith(BASE) && !url.startsWith('data:') && !url.startsWith('blob:')) { outside.push(url); return route.abort(); }
        const held = Object.entries(profile.hold).find(([key]) => route.request().method() === 'GET' && HELD[key].test(new URL(url).pathname + new URL(url).search));
        if (held) await sleep(held[1]);
        return route.continue().catch(() => undefined);
      });
      const page = await context.newPage();
      const timeline = async () => (await page.evaluate(() => window.__timeline).catch(() => [])).map((event) => event.join(' ')).join('\n');
      try {
        await page.goto(`${BASE}/`, { waitUntil: 'commit' });
        // The first frame the trigger is on screen is the earliest a person (or a script) can press it.
        await page.waitForFunction((selector) => {
          const element = document.querySelector(selector);
          const box = element ? element.getBoundingClientRect() : null;
          return Boolean(box && box.width > 0 && box.height > 0);
        }, TRIGGER, { polling: 'raf', timeout: 30000 });
        const press = async () => {
          const box = await page.locator(TRIGGER).boundingBox();
          await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
        };
        await press();
        const dialog = page.locator(DIALOG);
        await dialog.waitFor({ timeout: 5000 });
        await sleep(Math.max(0, ...Object.values(profile.hold)) + 800);

        const frames = await page.evaluate(() => window.__timeline.filter((event) => event[1] === 'state').map((event) => event[2]));
        assert(frames.some((state) => state.includes('panel=yes')), 'the dialog never showed');
        const firstOpen = frames.findIndex((state) => state.includes('panel=yes'));
        assert(frames.slice(firstOpen).every((state) => state.includes('panel=yes') && state.includes('expanded=true')), `the dialog closed by itself: ${frames.slice(firstOpen).join(' > ')}`);
        assert.equal(new Set(frames.map((state) => state.split(' ')[0]).filter((state) => state !== 'trigger#-')).size, 1, 'the gear node was replaced');
        assert(await page.evaluate((selector) => document.querySelector(selector).contains(document.activeElement), DIALOG), 'the keyboard focus left the dialog');
        // The chat model tab comes with the session's model list and levels, so it can appear after the dialog is open.
        const chatTab = page.locator(CHAT_TAB);
        await chatTab.waitFor({ timeout: 10000 });
        await chatTab.click();
        const panel = page.locator(PANEL);
        await panel.waitFor({ timeout: 5000 });
        assert.equal(await dialog.count(), 1, 'the dialog closed while the chat model tab opened');
        const columns = (await panel.getAttribute('data-layout')) === 'columns';

        await page.keyboard.press('Escape');
        await dialog.waitFor({ state: 'detached', timeout: 3000 });
        assert(await page.evaluate((selector) => document.activeElement === document.querySelector(selector), TRIGGER), 'the focus did not return to the gear');
        // The gear opens the dialog on the tab it was left on.
        await press();
        await panel.waitFor({ timeout: 5000 });
        if (columns) {
          const other = panel.locator('[data-testid=workspace-chat-model-section] [role=menuitemradio][aria-checked=false]').first();
          await other.click();
          await sleep(900);
          assert.equal(await dialog.count(), 1, 'picking a model closed the dialog');
          assert((await panel.locator('[data-testid=workspace-reasoning-section]').count()) === 1, 'the levels column went away');
        }
        const level = panel.locator('[data-testid=workspace-reasoning-section] [role=menuitemradio][aria-checked=false]').first();
        const levelName = (await level.innerText()).trim().split(/\s+/)[0].toLowerCase();
        const put = page.waitForRequest((request) => request.method() === 'PUT' && HELD.reasoning.test(new URL(request.url()).pathname), { timeout: 5000 });
        await level.click();
        await put;
        // #405: the pick applies at once and the dialog stays open, with the picked level checked.
        await page.waitForFunction(([selector, name]) => {
          const row = document.querySelector(selector);
          // innerText: the rows' names are capitalised by CSS, the level values are not.
          return Boolean(row && row.innerText.trim().split(/\s+/)[0].toLowerCase() === name);
        }, [`${DIALOG} [data-testid=workspace-reasoning-section] [role=menuitemradio][aria-checked=true]`, levelName], { timeout: 5000 });
        assert.equal(await dialog.count(), 1, 'picking a level closed the dialog');

        await page.mouse.click(6, 6);
        await dialog.waitFor({ state: 'detached', timeout: 3000 });
        for (let round = 0; round < 3; round += 1) {
          await press();
          await dialog.waitFor({ timeout: 5000 });
          await sleep(300);
          assert.equal(await dialog.count(), 1, `round ${round + 1}: the dialog closed by itself`);
          await page.keyboard.press('Escape');
          await dialog.waitFor({ state: 'detached', timeout: 3000 });
        }
        assert.equal(outside.length, 0, `requests outside BASE: ${outside.join(', ')}`);
        return columns ? 'two columns' : 'levels only';
      } catch (error) {
        await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${profile.name.replace(/\W+/g, '-')}.png`) }).catch(() => undefined);
        writeFileSync(path.join(OUT, `${engine}-${viewport}-${profile.name.replace(/\W+/g, '-')}.timeline.txt`), `${await timeline()}\n`);
        console.log(`--- timeline of "${profile.name}" (${engine} ${viewport})\n${await timeline()}\n---`);
        // A page load that was rate limited never gets its levels; that is the server's limiter, not the panel.
        if (limited.length > 0) throw new Error(`the server rate-limited this page load (HTTP 429 on ${[...new Set(limited)].join(', ')}): wait a minute and run again | ${String(error?.message ?? error).split('\n')[0]}`);
        throw error;
      } finally {
        await context.close();
        // Every page load is about a hundred API calls and the server answers at most 1200 a minute per address.
        await sleep(3000);
      }
    });
  }
}

for (const engine of ENGINES) {
  const browser = await pw[engine].launch();
  try {
    for (const size of VIEWPORTS) await checks(engine, browser, size);
  } finally {
    await browser.close();
  }
}
writeFileSync(path.join(OUT, 'report.json'), JSON.stringify(results, null, 2));
const failed = results.filter((result) => !result.passed);
console.log(`${results.length - failed.length}/${results.length} passed; evidence in ${OUT}`);
process.exit(failed.length > 0 ? 1 : 0);
