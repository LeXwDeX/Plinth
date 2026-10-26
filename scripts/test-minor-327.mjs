#!/usr/bin/env node
/**
 * Real-browser checks for #327. No credentials and no paid upstream: a local server with mock providers, the mock
 * Agent and mock login, with one allow-listed administrator e-mail, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18527 WEB_ASSETS_ROOT=build/go-web \
 *   ADMIN_ALLOWLIST_EMAILS=admin327@example.test DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage \
 *   LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18527 OUT=<scratch>/out node scripts/test-minor-327.mjs
 * Per engine and viewport (a fresh person each time):
 *  1. The usage statistics count what the session total counts: the person's row equals their session's credits; a
 *     failed, uncharged task is a task and a failure but adds no credits.
 *  2. The administration home 预估消耗 card adds every user up per unit (Tripo credits beside platform credits)
 *     instead of "≈报价未知", and equals the server's total.
 *  3. 功能 设置: 清空选择 keeps the saved enabled operations (mesh-completion stays); saving unchanged content keeps
 *     the revision.
 *  4. The 加工 panel writes sizes in decimal MB (a ~80 MiB Rodin model reads as its bytes / 1,000,000) and names the
 *     source by its public format (hyper3d · GLB; a Meshy Rigging result meshy · GLB, never rigged_character_glb_url).
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * PLAYWRIGHT_MODULE_PATH. Every browser request outside BASE is refused and counted.
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18527');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'minor-327-'));
const ADMIN_EMAIL = process.env.ADMIN_EMAIL || 'admin327@example.test';
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const usageFields = application.administration.usage.api.fields;
const label = (id) => application.capabilities.find((capability) => capability.id === id).label_cn;
const TRIPO = 'tripo/tripo-v3.1-full/text-to-3d';
const MESHY = 'meshy/meshy-7.1/text-to-3d';
const RODIN = 'hyper3d/rodin-gen-2.5/text-to-3d';
const RIGGING = 'meshy/processing/rigging';

/** The decimal-MB text of src/lib/size-text.ts, written out independently for the check. */
function decimalSize(bytes, roundUp) {
  const [unit, scale] = bytes < 1_000_000 ? [' KB', 1_000] : [' MB', 1_000_000];
  const hundredths = roundUp ? Math.ceil((bytes * 100) / scale) : Math.floor((bytes * 100) / scale);
  return `${(hundredths / 100).toString()}${unit}`;
}

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return body;
}

async function login(context, user, email = `${user}@example.test`) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email } });
  assert.equal(response.status(), 200, 'mock login');
  return response.json().catch(() => ({}));
}

async function submit(context, sessionID, capability, offeringID, input) {
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: sessionID, capability, offering_id: offeringID, input });
  const key = `minor327-${prepared.prepared_input_id}`;
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  return accepted.task_id;
}

async function waitTask(context, taskID, done = ['SUCCEEDED'], timeout = 120000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const task = await api(context, 'GET', routes.task_item.replace('{taskID}', taskID));
    if (done.includes(task.state)) return task;
    assert(!['FAILED', 'CANCELED', 'SUCCEEDED'].includes(task.state), `task ${taskID} ${task.state}`);
    assert(Date.now() < deadline, `task ${taskID} did not finish`);
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

const consent = (offeringID) => {
  const offering = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8')).offerings.find((item) => item.id === offeringID);
  return offering.parameters.some((parameter) => parameter.name === 'authorize_extra_credits') ? { authorize_extra_credits: true } : {};
};

async function waitFor(fn, message, timeout = 30000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await fn();
    if (value) return value;
    assert(Date.now() < deadline, message);
    await new Promise((resolve) => setTimeout(resolve, 400));
  }
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

async function guardedContext(browser, viewport, outside) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block' });
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  return context;
}

async function openStudio(page, sessionID) {
  await page.goto(`${BASE}/?session=${encodeURIComponent(sessionID)}`);
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
}

async function showTask(page, taskID) {
  const tile = page.locator(`[data-testid=version-tile][data-key="n:${taskID}"]`);
  await tile.waitFor({ timeout: 30000 });
  await tile.locator('button').first().click();
  await page.waitForFunction((key) => document.querySelector(`[data-testid=version-tile][data-key="${key}"]`)?.getAttribute('data-current') === 'true',
    `n:${taskID}`, { timeout: 10000 });
}

const sameTotals = (a, b) => JSON.stringify([...(a ?? [])].sort((x, y) => x.unit.localeCompare(y.unit))) === JSON.stringify([...(b ?? [])].sort((x, y) => x.unit.localeCompare(y.unit)));

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  const user = `minor327-${engine}-${viewport}-${Date.now()}`;
  const userID = (await login(context, user))?.user?.id ?? '';
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (target, name) => target.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });

  // One session: a Tripo model (native Tripo credits), a Meshy model, and a Meshy one that fails uncharged.
  const session = await api(context, 'POST', legacy.chat_sessions, { title: `#327 ${engine} ${viewport}` });
  const tripo = await waitTask(context, await submit(context, session.id, 'text-to-3d', TRIPO, { prompt: 'A small wooden stool', ...consent(TRIPO) }));
  const meshy = await waitTask(context, await submit(context, session.id, 'text-to-3d', MESHY, { prompt: 'A small wooden puppet', ...consent(MESHY) }));
  const failed = await waitTask(context, await submit(context, session.id, 'text-to-3d', MESHY, { prompt: 'a stool [mock:insufficient_balance]', ...consent(MESHY) }), ['FAILED']);

  const admin = await guardedContext(browser, { width, height }, outside);
  await login(admin, `admin327-${engine}-${viewport}`, ADMIN_EMAIL);

  await step('1: the usage row counts what the session total counts; the uncharged failure is a task, not credits', async () => {
    const own = await api(context, 'GET', `${legacy.chat_sessions}/${session.id}`);
    const usage = await api(admin, 'GET', `${legacy.admin_usage}?days=7`);
    const row = usage[usageFields.data].find((item) => (userID ? item[usageFields.user_id] === userID : String(item[usageFields.user_name]).startsWith(user)));
    assert(row, `no usage row for ${user}`);
    assert.equal(row[usageFields.total_tasks], 3, `tasks ${row[usageFields.total_tasks]}`);
    assert.equal(row[usageFields.failed_tasks], 1, `failures ${row[usageFields.failed_tasks]}`);
    if (own.price_totals) {
      assert(sameTotals(row.price_totals, own.price_totals), `row ${JSON.stringify(row.price_totals)} vs session ${JSON.stringify(own.price_totals)}`);
    } else {
      assert.equal(row[usageFields.estimated_credits], own.estimatedCredits ?? own.estimated_credits, `row ${JSON.stringify(row)} vs session ${JSON.stringify(own)}`);
    }
    return `failed ${failed.id}; row ${JSON.stringify(row.price_totals ?? row[usageFields.estimated_credits])}; session ${JSON.stringify(own.price_totals ?? own.estimatedCredits)}`;
  });

  await step('2: the 预估消耗 card adds every user up per unit instead of "报价未知"', async () => {
    const usage = await api(admin, 'GET', `${legacy.admin_usage}?days=7`);
    const total = usage[usageFields.total];
    assert(total && Array.isArray(total.price_totals) && total.price_totals.some((item) => item.unit === 'tripo.credit'), `total ${JSON.stringify(total)}`);
    const adminPage = await admin.newPage();
    adminPage.on('pageerror', (error) => errors.push(error.message));
    await adminPage.goto(`${BASE}/admin`);
    const card = adminPage.locator('[data-testid=admin-usage-total]');
    await card.waitFor({ timeout: 30000 });
    await waitFor(async () => /\d/.test(await card.innerText()), 'card has no number', 15000);
    const shown = (await card.innerText()).trim();
    assert(!shown.includes(text('ui.pricing.unknown')), `card ${shown}`);
    for (const item of total.price_totals) {
      const unit = item.unit === 'legacy.credits' ? text('ui.pricing.legacy_estimate') : item.unit_label;
      const amount = item.amount.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
      assert(shown.includes(`${amount} ${unit}`), `card "${shown}" lacks ${amount} ${unit}`);
    }
    await shot(adminPage, '2-admin-card');
    await adminPage.close();
    return shown;
  });

  await step('3: 清空选择 keeps the saved enabled operations; unchanged content keeps the revision', async () => {
    const route = routes.chat_session_harness.replace('{sessionID}', session.id);
    const initial = await api(context, 'GET', route);
    const enabled = [...application.chat.harness.default_enabled_operations, 'mesh-completion'];
    const seeded = await api(context, 'PUT', route, { expected_revision: initial.selection.revision,
      selection: { operation_id: '', enabled_operations: enabled, sources: [], fixed_parameters: {}, fillable_parameters: [] } });
    await openStudio(page, session.id);
    await showTask(page, tripo.id);
    await page.click('[data-testid=right-panel-tab-operations]');
    const harness = page.locator('[data-testid=harness-panel]');
    const operation = application.chat.harness.operations.find((item) => item.id === 'mesh-segmentation');
    await harness.getByRole('radio', { name: new RegExp(operation.label) }).check({ timeout: 20000 });
    const service = harness.getByRole('combobox').first();
    await waitFor(async () => !(await service.innerText()).includes(text('ui.harness.choose_offering')) || null, 'no service', 8000).catch(async () => {
      await service.click();
      await page.getByRole('option').first().click();
    });
    const stageSource = harness.getByRole('button', { name: text('ui.harness.use_current') });
    if (await stageSource.count()) await stageSource.click();
    await harness.getByRole('button', { name: text('ui.harness.save') }).click();
    const saved = await waitFor(async () => {
      const view = await api(context, 'GET', route);
      return view.selection.operation_id === 'mesh-segmentation' ? view : null;
    }, 'not saved', 15000);
    assert(saved.selection.enabled_operations.includes('mesh-completion'), `saved enabled ${saved.selection.enabled_operations}`);
    await shot(page, '3-saved');
    await harness.getByRole('button', { name: text('ui.harness.clear') }).click();
    const cleared = await waitFor(async () => {
      const view = await api(context, 'GET', route);
      return !view.selection.operation_id ? view : null;
    }, 'not cleared', 15000);
    assert.deepEqual([...cleared.selection.enabled_operations].sort(), [...saved.selection.enabled_operations].sort(), 'clear changed the enabled list');
    assert.equal(cleared.selection.revision, saved.selection.revision + 1, 'clear made more than one version');
    await waitFor(async () => (await harness.locator('[data-testid=harness-status]').innerText()).includes(text('ui.harness.status_free')), 'panel not free', 10000);
    const again = await api(context, 'PUT', route, { expected_revision: cleared.selection.revision, selection: cleared.selection });
    assert.equal(again.selection.revision, cleared.selection.revision, 'an unchanged save made a new revision');
    await shot(page, '3-cleared');
    return `seeded r${seeded.selection.revision}; saved r${saved.selection.revision}; cleared r${cleared.selection.revision} (${cleared.selection.enabled_operations.join(',')}); unchanged r${again.selection.revision}`;
  });

  await step('4: the 加工 panel writes decimal MB and names the source by its format', async () => {
    const large = await waitTask(context, await submit(context, session.id, 'text-to-3d', RODIN, { prompt: 'A tall carved totem [mock:large_model]', ...consent(RODIN) }));
    const glb = large.assets.find((asset) => asset.role === 'model' && asset.media_type === 'model/gltf-binary');
    assert.equal(glb.format, 'GLB', `format ${glb.format}`);
    await openStudio(page, session.id);
    await showTask(page, large.id);
    await page.click('[data-testid=right-panel-tab-process]');
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.waitFor({ timeout: 20000 });
    const source = (await panel.locator('[data-testid=processing-source-label]').innerText()).trim();
    assert.equal(source, 'hyper3d · GLB');
    await panel.getByRole('checkbox', { name: label('retexture') }).check({ timeout: 20000 });
    const refusal = (await panel.locator('[data-testid=processing-refused-retexture]').innerText()).trim();
    const size = decimalSize(glb.size, true);
    assert(refusal.includes(size), `refusal "${refusal}" lacks ${size}`);
    assert(!/MiB/.test(refusal), `refusal "${refusal}"`);
    await shot(page, '4-large');
    const rigged = await waitTask(context, await submit(context, session.id, 'rigging', RIGGING, { source_task: meshy.id }));
    await openStudio(page, session.id);
    await showTask(page, rigged.id);
    await page.click('[data-testid=right-panel-tab-process]');
    await panel.waitFor({ timeout: 20000 });
    const riggedSource = (await panel.locator('[data-testid=processing-source-label]').innerText()).trim();
    assert.equal(riggedSource, 'meshy · GLB');
    await shot(page, '4-rigged');
    return `${source} | ${size} in "${refusal.slice(0, 120)}" | ${riggedSource}`;
  });

  await step('no page errors and no outside requests', async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await admin.close();
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

writeFileSync(path.join(OUT, 'report.json'), JSON.stringify(results, null, 2));
for (const result of results) {
  console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
}
const failed = results.filter((result) => !result.passed);
console.log(`${results.length - failed.length}/${results.length} passed; report ${path.join(OUT, 'report.json')}`);
process.exit(failed.length ? 1 : 0);
