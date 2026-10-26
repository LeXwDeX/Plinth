#!/usr/bin/env node
/**
 * Real-browser check for #323: every obtainable row of the 格式 tab has its own action and says what it costs.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18323 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18323 node scripts/test-formats-row-action.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * OUT (screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH. Every request outside
 * BASE is refused and counted.
 *
 * Per engine and viewport, on a mock Hyper3D Rodin result (GLB only; glTF comes from the free local
 * conversion, the other formats from Meshy Convert, #330):
 *  1. The glTF row reads 可转换 · 本地 and 免费 with its hint and a 转换 button; it never says 格式转换.
 *     A Meshy row keeps the provider name and the estimate, and its button names the formats the run brings along.
 *  2. Clicking the glTF button posts {"formats":["gltf"]} to the completion route; the row turns 转换中, then
 *     已有 with a download link whose bytes download. The Meshy rows stay obtainable.
 *  3. The task detail (the dialog the chat's result card also opens) shows the same row and action.
 *  4. 补齐格式 at the bottom is still there and still starts the rest (mock Meshy; no paid upstream).
 * Once per engine the same rows are read in English (Accept-Language en-US).
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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18323');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'formats-row-action-'));
mkdirSync(OUT, { recursive: true });

const { application, offerings } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const english = application.localization.bundles.en.messages;
const textIn = (locale, key, values = {}) => {
  const value = (locale === 'en' ? english : application.content.messages)[key];
  assert(value, `missing Schema text ${key} (${locale})`);
  return Object.entries(values).reduce((result, [name, item]) => result.replaceAll(`{${name}}`, String(item)), value);
};
// Offering copy (provider names and short labels) is translated by text, not by key.
const offeringText = (locale, value) => (locale === 'en' ? application.localization.bundles.en.offering_texts[value] : value) ?? value;
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const SOURCE_OFFERING = 'hyper3d/rodin-gen-2.5/text-to-3d';
// #330: the Meshy rows come from Meshy Convert, whose estimate is read from the Schema.
const MESHY_CREDITS = offerings.find((offering) => offering.id === 'meshy/processing/convert').estimated_credits;
const completeRoute = (taskID) => routes.model_formats_complete.replace('{taskID}', encodeURIComponent(taskID));

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 200)}`);
  return body;
}

async function login(context, user) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
}

/** A mock Rodin text-to-3d result in a fresh session, through the public prepare → grant → submit path. */
async function rodinSource(context, title) {
  const session = await api(context, 'POST', legacy.chat_sessions, { title });
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: SOURCE_OFFERING,
    input: { prompt: 'A simple low-poly wooden stool', authorize_extra_credits: true } });
  const key = `formats-row-${prepared.prepared_input_id}`;
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

/** The button sits inside its row: the narrow 格式 tab must not push it past the row's edge. */
async function assertInside(row, button) {
  const outer = await row.boundingBox();
  const inner = await button.boundingBox();
  assert(outer && inner, 'row or button not laid out');
  assert(inner.x >= outer.x - 0.5 && inner.x + inner.width <= outer.x + outer.width + 0.5, `button ${JSON.stringify(inner)} outside row ${JSON.stringify(outer)}`);
}

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

async function guardedContext(browser, viewport, outside, locale) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block', locale: locale === 'en' ? 'en-US' : 'zh-CN', acceptDownloads: true });
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  return context;
}

async function openFormatsTab(page, source) {
  await page.goto(`${BASE}/?session=${encodeURIComponent(source.sessionID)}`);
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
  const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
  if (await tile.count()) await tile.click();
  await page.locator('[data-testid=right-panel-tab-formats]').click({ timeout: 20000 });
  await page.locator('[data-testid=model-formats][data-layout=pinned]').waitFor({ timeout: 20000 });
  await page.locator('[data-testid=model-format-gltf][data-status=obtainable]').waitFor({ timeout: 20000 });
}

async function checks(engine, browser, [width, height], locale) {
  const viewport = `${width}x${height}${locale === 'en' ? '-en' : ''}`;
  const t = (key, values) => textIn(locale, key, values);
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside, locale);
  const user = `formats-row-${engine}-${viewport}-${Date.now()}`;
  await login(context, user);
  const source = await rodinSource(context, `#323 ${engine} ${viewport}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const completions = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === completeRoute(source.taskID)) completions.push(request.postData() ?? '');
  });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const row = (id) => page.locator(`[data-testid=model-format-${id}]`);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });

  await step('the 格式 tab opens on the Rodin result with glTF obtainable', () => openFormatsTab(page, source));

  await step('the glTF row reads 可转换 · 本地 and 免费, with its hint and a 转换 button', async () => {
    const gltf = row('gltf');
    const text = (await gltf.innerText()).replace(/\s+/g, ' ');
    assert(text.includes(`${t('ui.workspace.model_formats.status.obtainable')} · ${offeringText(locale, '本地')}`), `pill: ${text}`);
    assert(text.includes(t('ui.workspace.model_formats.free')), `free: ${text}`);
    assert(text.includes(t('ui.workspace.model_formats.obtainable_hint')), `hint: ${text}`);
    assert(!text.includes(offeringText(locale, '格式转换')), `still names the Offering: ${text}`);
    const action = gltf.locator('[data-testid=model-format-action-gltf]');
    assert.equal(await action.getAttribute('data-action'), 'start');
    assert.equal((await action.innerText()).trim(), t('ui.workspace.model_formats.convert'));
    assert(await action.isEnabled());
    await assertInside(gltf, action);
    return text;
  });

  await step('a Meshy row keeps the provider name and estimate, and names the formats the run converts', async () => {
    const fbx = row('fbx');
    const text = (await fbx.innerText()).replace(/\s+/g, ' ');
    assert(text.includes(`${t('ui.workspace.model_formats.status.obtainable')} · Meshy AI`), `pill: ${text}`);
    assert(text.includes(t('ui.workspace.model_formats.credits', { credits: MESHY_CREDITS })), `estimate: ${text}`);
    assert(!text.includes(t('ui.workspace.model_formats.free')), `paid row says free: ${text}`);
    const action = fbx.locator('[data-testid=model-format-action-fbx]');
    await assertInside(fbx, action);
    const together = t('ui.workspace.model_formats.convert_together', { formats: ['FBX', 'OBJ', 'STL', 'USDZ', '3MF'].join(t('ui.studio.list_separator')) });
    assert((await action.getAttribute('aria-label')).includes(together), await action.getAttribute('aria-label'));
    await action.hover();
    const tip = page.getByRole('tooltip').filter({ hasText: together });
    await tip.waitFor({ state: 'visible', timeout: 5000 });
    await shot('1-obtainable-hover-fbx');
    await page.mouse.move(5, 5);
    await tip.waitFor({ state: 'hidden', timeout: 5000 });
    const complete = page.locator('[data-testid=model-formats-actions] [data-testid=model-formats-complete]');
    assert(await complete.isVisible() && await complete.isEnabled(), '补齐格式 is missing');
    await shot('1-obtainable');
    return text;
  });

  await step('the task detail shows the same row, action and wording (flow layout)', async () => {
    await page.locator('[data-testid=version-tile] [data-testid=version-detail]').first().click();
    const detail = page.locator('[data-testid=result-detail] [data-testid=model-formats]');
    await detail.waitFor({ timeout: 20000 });
    const gltf = detail.locator('[data-testid=model-format-gltf][data-status=obtainable]');
    await gltf.waitFor({ timeout: 20000 });
    const text = (await gltf.innerText()).replace(/\s+/g, ' ');
    assert(text.includes(`${t('ui.workspace.model_formats.status.obtainable')} · ${offeringText(locale, '本地')}`), `detail pill: ${text}`);
    assert(text.includes(t('ui.workspace.model_formats.free')) && text.includes(t('ui.workspace.model_formats.obtainable_hint')), `detail note: ${text}`);
    const action = gltf.locator('[data-testid=model-format-action-gltf]');
    assert.equal((await action.innerText()).trim(), t('ui.workspace.model_formats.convert'));
    await assertInside(gltf, action);
    await gltf.scrollIntoViewIfNeeded();
    await shot('1-task-detail');
    await page.keyboard.press('Escape');
    await detail.waitFor({ state: 'detached', timeout: 10000 });
    return text;
  });

  await step('clicking 转换 converts glTF alone: 转换中, then 已有 with a download', async () => {
    const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === completeRoute(source.taskID) && candidate.request().method() === 'POST');
    await row('gltf').locator('[data-testid=model-format-action-gltf]').click();
    const answered = await response;
    assert.equal(answered.status(), 202);
    assert.deepEqual(JSON.parse(completions.at(-1) || 'null'), { formats: ['gltf'] });
    await page.locator('[data-testid=model-format-gltf][data-status=converting]').waitFor({ timeout: 5000 });
    const converting = (await row('gltf').innerText()).replace(/\s+/g, ' ');
    assert(converting.includes(t('ui.workspace.model_formats.status.converting')), converting);
    await shot('2-converting');
    // Only the glTF step started: the Meshy rows still wait for their own action.
    assert.equal(await row('fbx').getAttribute('data-status'), 'obtainable');
    await page.locator('[data-testid=model-format-gltf][data-status=available]').waitFor({ timeout: 60000 });
    const link = row('gltf').locator('a[download]');
    await link.waitFor({ timeout: 5000 });
    const href = await link.getAttribute('href');
    const bytes = await context.request.get(new URL(href, BASE).toString());
    assert.equal(bytes.status(), 200, `download ${href}`);
    const size = (await bytes.body()).length;
    assert(size > 0, 'empty download');
    assert.equal(await row('gltf').locator('[data-testid^=model-format-action-]').count(), 0, 'action left on an available row');
    await shot('3-available');
    return `${converting} → available, ${size} bytes`;
  });

  await step('补齐格式 still starts the rest (mock Meshy)', async () => {
    const complete = page.locator('[data-testid=model-formats-complete]');
    await complete.waitFor({ timeout: 5000 });
    const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === completeRoute(source.taskID) && candidate.request().method() === 'POST');
    await complete.click();
    const answered = await response;
    assert.equal(answered.status(), 202);
    assert.equal(completions.at(-1), '', '补齐格式 sends no format filter');
    await page.locator('[data-testid=model-format-fbx][data-status=converting]').waitFor({ timeout: 10000 });
    await shot('4-complete-rest');
  });

  await step('no runtime errors and no request left the loopback server', async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

try {
  for (const engine of ENGINES) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      for (const viewport of VIEWPORTS) await checks(engine, browser, viewport, 'zh');
      await checks(engine, browser, VIEWPORTS[0], 'en');
    } finally {
      await browser.close();
    }
  }
} finally {
  writeFileSync(path.join(OUT, 'report.json'), JSON.stringify({ scope: 'local mock server only; no deployed environment, no paid upstream', base: BASE, results }, null, 2));
}
const failed = results.filter((result) => !result.passed);
console.log(JSON.stringify({ passed: failed.length === 0, cases: results.length, failed, out: OUT }, null, 2));
process.exit(failed.length === 0 ? 0 : 1);
