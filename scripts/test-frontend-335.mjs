#!/usr/bin/env node
/**
 * Real-browser check for #335 (frontend issues found by the #313 review).
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid
 * calls), built from this checkout (`pnpm build` first), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18335 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18335 node scripts/test-frontend-335.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * LOCALES (default "zh-CN,en"), OUT (screenshots and report.json; default a temp folder),
 * PLAYWRIGHT_MODULE_PATH. Every request outside BASE is refused and counted. Expected words are read
 * from the server's public Schema in each locale.
 *
 * Per engine, viewport and locale:
 *  A. 加工 footer with Meshy and Hyper3D chosen together (mock Hyper3D Rodin results):
 *     1. 模型分割 on Hyper3D Bang + 骨骼绑定 on Meshy: the native estimate line for 模型分割 and the credit
 *        total of 骨骼绑定 are both shown (before #335 the credit total disappeared);
 *     2. 重贴图 on Hyper3D Texture + 骨骼绑定 on Meshy: the same, at Hyper3D Texture's estimate;
 *     3. on a ~80 MiB result ([mock:large_model]) 模型分割 on Bang's Rodin-task path + Meshy 重贴图 on its
 *        result: both shown; 重贴图 alone cannot run there, so nothing is priced and no estimate is listed.
 *  B. A choice card confirmed while a composer picture is still being read (the page's FileReader is
 *     slowed for this): the card says it waits (Schema `ui.chat.choice.waiting_attachments`) with its
 *     buttons locked and no turn sent; once the picture is attached the answer goes out by itself, the
 *     card shows the answer, and the picture stays in the composer. Before #335 nothing happened.
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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18335');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const LOCALES = (process.env.LOCALES || 'zh-CN,en').split(',').map((item) => item.trim()).filter(Boolean);
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'frontend-335-'));
mkdirSync(OUT, { recursive: true });
// How long the page's picture reads are held back in part B.
const SLOW_READ_MS = 4000;

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const answerField = application.chat.api.fields.choice_answer;
const localeCookie = application.localization.preference.cookie;
const SOURCE_OFFERING = 'hyper3d/rodin-gen-2.5/text-to-3d';
const BANG_RODIN = 'hyper3d/bang/rodin-task';
const fill = (template, values) => Object.entries(values).reduce((result, [key, value]) => result.replaceAll(`{${key}}`, String(value)), template);
const squash = (value) => value.replace(/\s+/g, ' ').trim();

function solidPng(width, height, [r, g, b]) {
  const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
  const crc = (buffer) => { let c = 0xffffffff; for (const byte of buffer) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
  const chunk = (type, data) => {
    const length = Buffer.alloc(4); length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const sum = Buffer.alloc(4); sum.writeUInt32BE(crc(body));
    return Buffer.concat([length, body, sum]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 2;
  const row = Buffer.concat([Buffer.from([0]), Buffer.from(Array.from({ length: width }, () => [r, g, b]).flat())]);
  const raw = Buffer.concat(Array.from({ length: height }, () => row));
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', header), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}
const PICTURE = solidPng(256, 256, [200, 120, 60]);

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 200)}`);
  return body;
}

/** A mock Rodin text-to-3d result in a fresh session, through the public prepare → grant → submit path. */
async function rodinSource(context, title, prompt) {
  const session = await api(context, 'POST', legacy.chat_sessions, { title });
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: SOURCE_OFFERING,
    input: { prompt, authorize_extra_credits: true } });
  const key = `frontend-335-${prepared.prepared_input_id}`;
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  const deadline = Date.now() + 90000;
  for (;;) {
    const task = await api(context, 'GET', routes.task_item.replace('{taskID}', accepted.task_id));
    if (task.state === 'SUCCEEDED') return { sessionID: session.id, taskID: accepted.task_id };
    assert(!['FAILED', 'CANCELED'].includes(task.state), `source task ${task.state}`);
    assert(Date.now() < deadline, 'source task did not finish');
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

async function waitFor(fn, message, timeout = 30000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await fn();
    if (value) return value;
    assert(Date.now() < deadline, message);
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

const results = [];
async function record(where, name, fn) {
  const started = Date.now();
  try {
    const note = await fn();
    results.push({ ...where, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
  } catch (error) {
    results.push({ ...where, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
  }
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
  // Part B: hold the page's picture reads back while `window.__slowReadMs` is set.
  await context.addInitScript(() => {
    const read = FileReader.prototype.readAsDataURL;
    FileReader.prototype.readAsDataURL = function readSlowly(blob) {
      const delay = window.__slowReadMs || 0;
      if (!delay) return read.call(this, blob);
      setTimeout(() => read.call(this, blob), delay);
    };
  });
  const user = `frontend-335-${engine}-${viewport}-${Date.now()}`;
  const login = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(login.status(), 200, 'mock login');
  const normal = await rodinSource(context, `#335 ${engine} ${viewport}`, 'A simple low-poly wooden stool');
  const large = await rodinSource(context, `#335 large ${engine} ${viewport}`, 'A tall carved totem [mock:large_model]');
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));

  for (const locale of LOCALES) {
    await context.addCookies([{ name: localeCookie, value: locale, url: BASE }]);
    const schema = (await api(context, 'GET', routes.schema ?? '/api/v1/schema')).schema;
    const text = (key) => {
      const value = schema.content.messages[key];
      assert(value, `missing Schema text ${key}`);
      return value;
    };
    const label = (id) => schema.capabilities.find((capability) => capability.id === id).label_cn;
    const where = { engine, viewport, locale };
    const step = (name, fn) => record(where, name, fn);
    const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${locale}-${name}.png`) });
    const panel = page.locator('[data-testid=model-processing-panel]');
    const estimate = (amount) => fill(text('ui.pricing.default_estimate'), { amount });

    const openProcessing = async (sessionID) => {
      await page.goto(`${BASE}/?session=${encodeURIComponent(sessionID)}`);
      // Leaving a page that holds an ~80 MiB model can take a while under load.
      await page.waitForSelector('[data-testid=studio-shell]', { timeout: 60000 });
      const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
      await tile.waitFor({ timeout: 20000 });
      await tile.click();
      await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
      await panel.getByRole('checkbox', { name: label('retexture') }).waitFor({ timeout: 20000 });
    };
    const tick = async (id, on = true) => {
      const box = panel.getByRole('checkbox', { name: label(id) });
      if (on) await box.check(); else await box.uncheck();
      await page.waitForTimeout(300);
    };
    const choose = async (id, pattern) => {
      const choice = panel.locator(`[data-testid=processing-offering-${id}]`);
      await choice.waitFor({ timeout: 5000 });
      await choice.locator('[data-slot=select-trigger], button').first().click();
      await page.getByRole('option').first().waitFor({ timeout: 5000 });
      await page.getByRole('option').filter({ hasText: pattern }).first().click();
      await page.getByRole('option').first().waitFor({ state: 'detached', timeout: 5000 });
      await page.waitForTimeout(300);
    };
    /** The footer's native estimate lines and credit line, as shown. */
    const footer = async () => {
      const native = panel.locator('[data-testid=processing-cost-native]');
      const credits = panel.locator('[data-testid=processing-cost]');
      return {
        native: await native.count() ? (await native.locator('p').allInnerTexts()).map(squash) : [],
        kind: await credits.count() ? await credits.getAttribute('data-kind') : null,
        credits: await credits.count() ? squash(await credits.innerText()) : null,
      };
    };
    const mixed = async (nativeStep, nativeAmount, creditStep, credits) => {
      const shown = await footer();
      assert.deepEqual(shown.native, [text('ui.pricing.totals_heading'), `${label(nativeStep)}: ${estimate(nativeAmount)}`], `native lines ${JSON.stringify(shown)}`);
      assert.equal(shown.kind, 'total', `credit line ${JSON.stringify(shown)}`);
      assert.equal(shown.credits, fill(text('ui.studio.processing.cost_total'), { credits }));
      // The step quotes say the same.
      assert.equal(squash(await panel.locator(`[data-testid=processing-quote-${nativeStep}]`).innerText()), estimate(nativeAmount));
      assert.equal(squash(await panel.locator(`[data-testid=processing-quote-${creditStep}]`).innerText()), `${text('ui.workspace.harness.credits')}: ${credits}`);
      return `${shown.native.slice(1).join(' / ')} + ${shown.credits}`;
    };

    await step('A1: Hyper3D Bang 模型分割 + Meshy 骨骼绑定 show the estimate and the credit total together', async () => {
      await openProcessing(normal.sessionID);
      await tick('mesh-segmentation');
      await choose('mesh-segmentation', /Bang/);
      await tick('rigging');
      const note = await mixed('mesh-segmentation', '0.5 Hyper3D credits', 'rigging', 5);
      await shot('A1-bang-rigging');
      await tick('rigging', false);
      await tick('mesh-segmentation', false);
      return note;
    });

    await step('A2: Hyper3D Texture 重贴图 + Meshy 骨骼绑定 show the estimate and the credit total together', async () => {
      await tick('retexture');
      await choose('retexture', /^Hyper3D/);
      await tick('rigging');
      const note = await mixed('retexture', '2 Hyper3D credits', 'rigging', 5);
      await shot('A2-texture-rigging');
      await tick('rigging', false);
      await tick('retexture', false);
      return note;
    });

    await step('A3: on an ~80 MiB model only the steps that can run are priced', async () => {
      await openProcessing(large.sessionID);
      // 重贴图 alone: no service takes the model, so nothing is priced and no estimate is listed.
      await tick('retexture');
      await panel.locator('[data-testid=processing-refused-retexture]').waitFor({ timeout: 10000 });
      let shown = await footer();
      assert.deepEqual(shown.native, [], `native lines ${JSON.stringify(shown)}`);
      assert.equal(shown.kind, 'none');
      assert.equal(shown.credits, text('ui.studio.processing.cost_none'));
      await shot('A3-none');
      // Bang on the Rodin task makes 重贴图 run on its result: both parts are priced.
      await tick('mesh-segmentation');
      await choose('mesh-segmentation', /Bang/);
      await waitFor(async () => (await footer()).kind === 'total', 'retexture after Bang not priced', 10000);
      const note = await mixed('mesh-segmentation', '0.5 Hyper3D credits', 'retexture', 15);
      await shot('A3-bang-retexture');
      await tick('mesh-segmentation', false);
      await tick('retexture', false);
      shown = await footer();
      assert.equal(shown.kind, 'empty');
      return note;
    });

    await step('B: a choice confirmed while a picture is read waits, says so, and is sent once the picture is attached', async () => {
      const session = await rodinSource(context, `#335 card ${engine} ${viewport} ${locale}`, 'A simple low-poly wooden stool');
      const turns = [];
      const onRequest = (request) => {
        if (request.method() === 'POST' && new URL(request.url()).pathname === legacy.chat_messages.replace('{sessionID}', session.sessionID)) {
          turns.push({ at: Date.now(), body: request.postDataJSON() });
        }
      };
      page.on('request', onRequest);
      try {
        await page.goto(`${BASE}/?session=${encodeURIComponent(session.sessionID)}`);
        await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
        const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
        await tile.waitFor({ timeout: 20000 });
        await tile.click();
        const box = page.locator('[data-testid=studio-composer] textarea');
        await box.fill(locale === 'en' ? 'Segment the current model' : '给当前模型做模型分割');
        await page.locator('[data-testid=composer-send]').click();
        const card = page.locator('[data-testid=chat-choice-card][data-state=pending]');
        await card.waitFor({ timeout: 30000 });
        await page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
        const cardID = await card.getAttribute('data-choice-id');
        await card.locator(`[data-option-id="${BANG_RODIN}"]`).click();
        const before = turns.length;

        await page.evaluate((delay) => { window.__slowReadMs = delay; }, SLOW_READ_MS);
        await page.locator('input[type=file][multiple]').first().setInputFiles({ name: 'reading.png', mimeType: 'image/png', buffer: PICTURE });
        const clicked = Date.now();
        await card.locator('[data-testid=chat-choice-confirm]').click();
        const waiting = card.locator('[data-testid=chat-choice-waiting]');
        await waiting.waitFor({ timeout: 2000 });
        assert.equal(squash(await waiting.innerText()), text('ui.chat.choice.waiting_attachments'));
        assert.equal(await waiting.getAttribute('role'), 'status');
        assert(await card.locator('[data-testid=chat-choice-confirm]').isDisabled(), 'confirm stays enabled while waiting');
        assert.equal(await page.locator('[data-testid=composer-attachment-chip]').count(), 0, 'the picture was attached before the read finished');
        assert.equal(turns.length, before, 'the answer went out before the picture was read');
        await shot('B1-waiting');
        await page.evaluate(() => { window.__slowReadMs = 0; });

        await page.locator('[data-testid=composer-attachment-chip]').first().waitFor({ timeout: SLOW_READ_MS + 10000 });
        const attached = Date.now();
        await waitFor(() => turns.length > before, 'the held answer was not sent', 15000);
        const sent = turns[before];
        assert(sent.at >= clicked + SLOW_READ_MS - 500, `sent ${sent.at - clicked} ms after the click, before the read could finish`);
        assert.deepEqual(sent.body[answerField], { choice_id: cardID, option_id: BANG_RODIN });
        await page.locator(`[data-testid=chat-choice-card][data-choice-id="${cardID}"][data-state=answered]`).waitFor({ timeout: 30000 });
        assert.equal(await page.locator('[data-testid=chat-choice-waiting]').count(), 0, 'the waiting note stayed');
        // A choice answer leaves the draft alone: the picture is still attached.
        assert.equal(await page.locator('[data-testid=composer-attachment-chip]').count(), 1, 'the picture left the composer');
        await shot('B2-answered');
        return `answer sent ${sent.at - clicked} ms after 确认 (picture attached after ${attached - clicked} ms)`;
      } finally {
        page.off('request', onRequest);
        await page.evaluate(() => { window.__slowReadMs = 0; }).catch(() => {});
      }
    });
  }

  await record({ engine, viewport, locale: '*' }, 'no page errors and no outside requests', async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

for (const engine of ENGINES) {
  const browser = await pw[engine].launch();
  try {
    for (const viewport of VIEWPORTS) await checks(engine, browser, viewport);
  } finally {
    await browser.close();
  }
}
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), locales: LOCALES, results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.locale} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
console.log(`report: ${path.join(OUT, 'report.json')}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
