#!/usr/bin/env node
/**
 * Real-browser checks for #325: a Meshy model whose original Meshy task is past the Schema's
 * upstream_job_max_age_seconds (Meshy keeps API tasks at most three days) is processed from its
 * saved file, and a service that only takes the original task says why before anything is submitted.
 * No credentials and no paid upstream: a local server with mock providers, the mock Agent and mock login.
 *
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18451 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18451 DATABASE_PATH=<scratch>/a.db OUT=<scratch>/out node scripts/test-meshy-expired-source.mjs
 *
 * The script ages the Meshy source Tasks by rewriting generation_tasks.created_at in that SQLite file
 * (sqlite3 CLI), the only way to get an old Task without waiting days. Per engine and viewport:
 *  P. 加工 panel, 重贴图 on the aged model: Meshy Retexture shows 输入方式 本地模型资产（… 原任务已超过保留期）,
 *     the plan runs, and the frozen input names source_asset, never source_task;
 *  N. the same on a fresh Meshy model keeps 同服务商任务 (source_task);
 *  A. Agent 重贴图 on the aged model (choice card → Meshy, approval card) succeeds with source_asset;
 *  X. Agent 动画 on an aged Meshy rigging result is refused before any task, naming the retention.
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * PLAYWRIGHT_MODULE_PATH. Every browser request outside BASE is refused and counted.
 */
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18451');
  process.exit(2);
}
const DATABASE = process.env.DATABASE_PATH || '';
assert(DATABASE, 'DATABASE_PATH must name the server\'s SQLite file');
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'meshy-expired-'));
mkdirSync(OUT, { recursive: true });

const { application, offerings } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const label = (id) => application.capabilities.find((capability) => capability.id === id).label_cn;
const SOURCE_OFFERING = 'meshy/meshy-7.1/text-to-3d';
const RIGGING = 'meshy/processing/rigging';
const RETEXTURE = 'meshy/processing/retexture';
const maxAge = offerings.find((offering) => offering.id === RETEXTURE).parameters.find((parameter) => parameter.name === 'source_task').upstream_job_max_age_seconds;
assert(maxAge > 0, 'retexture source_task declares no upstream_job_max_age_seconds');
const EXPIRED_HINT = text('ui.workspace.processing.asset_route_expired').split('{')[0];

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return Object.assign(body, { httpStatus: response.status() });
}

async function login(context, user) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
}

async function approveAndSubmit(context, prepared, key) {
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  return api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
}

async function waitTask(context, taskID, timeout = 120000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const task = await api(context, 'GET', routes.task_item.replace('{taskID}', taskID));
    if (task.state === 'SUCCEEDED') return task;
    assert(!['FAILED', 'CANCELED'].includes(task.state), `task ${taskID} ${task.state} ${JSON.stringify(task.failure ?? {})}`);
    assert(Date.now() < deadline, `task ${taskID} did not finish`);
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

async function submitTask(context, sessionID, capability, offeringID, input) {
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: sessionID, capability, offering_id: offeringID, input });
  const accepted = await approveAndSubmit(context, prepared, `expired-${prepared.prepared_input_id}`);
  return waitTask(context, accepted.task_id);
}

/** Moves a Task's creation back past the declared age, as if it were generated days ago. */
function age(taskID, seconds) {
  const created = new Date(Date.now() - seconds * 1000).toISOString();
  execFileSync('sqlite3', [DATABASE, `UPDATE generation_tasks SET created_at = '${created}' WHERE id = '${taskID.replace(/'/g, "''")}'`]);
  const stored = execFileSync('sqlite3', [DATABASE, `SELECT created_at FROM generation_tasks WHERE id = '${taskID.replace(/'/g, "''")}'`]).toString().trim();
  assert.equal(stored, created, `created_at of ${taskID} not rewritten`);
}

async function sessionTasks(context, sessionID) {
  const listed = await api(context, 'GET', `${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=50`);
  return (listed.tasks ?? listed.items ?? []).map((task) => ({ id: task.id, offering: task.offering_id ?? task.offering?.id, capability: task.capability, state: task.state }));
}

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

/** Ticks 重贴图 with Meshy Retexture, a text prompt, runs the plan through its confirmations, and returns the route text and the step's Task. */
async function panelRetexture(page, context, sessionID, taskID, screenshot) {
  await openStudio(page, sessionID);
  await showTask(page, taskID);
  await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
  const panel = page.locator('[data-testid=model-processing-panel]');
  await panel.getByRole('checkbox', { name: label('retexture') }).check({ timeout: 20000 });
  const service = panel.locator('[data-testid=processing-offering-retexture]');
  if (await service.count() && (await service.evaluate((node) => node.tagName)) === 'SELECT') await service.selectOption(RETEXTURE);
  else if (await service.count()) {
    await service.click();
    await page.getByRole('option', { name: /Meshy Retexture/ }).click();
  }
  const provider = await panel.locator('[data-testid=processing-provider-retexture], [data-testid=processing-offering-retexture]').first().innerText();
  assert(/Meshy/.test(provider), `retexture service ${provider}`);
  const route = (await panel.locator('[data-testid=processing-route-retexture]').innerText()).trim();
  const prompt = panel.locator('textarea, input[type=text]').first();
  await prompt.fill('warm carved walnut');
  await page.screenshot({ path: path.join(OUT, `${screenshot}-panel.png`) });
  const before = new Set((await sessionTasks(context, sessionID)).map((task) => task.id));
  const click = async (key) => {
    const button = panel.getByRole('button', { name: new RegExp(text(key)) });
    if (await button.count() && await button.first().isEnabled()) { await button.first().click(); return true; }
    return false;
  };
  assert(await click('ui.workspace.processing.plan_confirm'), 'confirm is not enabled');
  const step = await waitFor(async () => {
    await click('ui.workspace.processing.plan_prepare_step');
    await click('ui.workspace.processing.plan_confirm_step');
    return (await sessionTasks(context, sessionID)).find((task) => !before.has(task.id) && task.offering === RETEXTURE);
  }, 'the retexture step created no task', 60000);
  const done = await waitTask(context, step.id);
  await page.screenshot({ path: path.join(OUT, `${screenshot}-done.png`) });
  return { route, task: done };
}

/** The frozen input's source parameters. The public task view withholds them, so read the local SQLite row. */
function frozenSource(task) {
  const raw = execFileSync('sqlite3', [DATABASE, `SELECT input_json FROM generation_tasks WHERE id = '${task.id.replace(/'/g, "''")}'`]).toString().trim();
  const input = JSON.parse(raw);
  return { asset: typeof input.source_asset === 'string', task: typeof input.source_task === 'string',
    flat: JSON.stringify({ source_task: input.source_task, source_asset: input.source_asset }) };
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  await login(context, `expired-${engine}-${viewport}-${Date.now()}`);
  const session = await api(context, 'POST', legacy.chat_sessions, { title: `#325 ${engine} ${viewport}` });
  const old = await submitTask(context, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A small wooden stool' });
  const fresh = await submitTask(context, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A small stone bench' });
  const rig = await submitTask(context, session.id, 'rigging', RIGGING, { source_task: fresh.id });
  age(old.id, maxAge + 3 * 24 * 3600);
  age(rig.id, maxAge + 3 * 24 * 3600);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);

  await step('P: panel 重贴图 on an aged Meshy model goes through the saved file and succeeds', async () => {
    const { route, task } = await panelRetexture(page, context, session.id, old.id, `${engine}-${viewport}-P`);
    assert(route.includes(EXPIRED_HINT), `route "${route}"`);
    const frozen = frozenSource(task);
    assert(frozen.asset && !frozen.task, `frozen input ${frozen.flat}`);
    return `${route}; task ${task.id} ${task.state}`;
  });

  await step('N: panel 重贴图 on a fresh Meshy model keeps the original task', async () => {
    const { route, task } = await panelRetexture(page, context, session.id, fresh.id, `${engine}-${viewport}-N`);
    assert(route.includes(text('ui.workspace.processing.task_route')), `route "${route}"`);
    const frozen = frozenSource(task);
    assert(frozen.task && !frozen.asset, `frozen input ${frozen.flat}`);
    return `${route}; task ${task.id}`;
  });

  await step('R: panel 动画 on an aged Meshy rigging result never offers the original task', async () => {
    await openStudio(page, session.id);
    await showTask(page, rig.id);
    await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.getByRole('checkbox', { name: label('animation') }).check({ timeout: 20000 });
    const route = (await panel.locator('[data-testid=processing-route-animation]').innerText()).trim();
    const rigging = await panel.getByRole('checkbox', { name: label('rigging') }).isChecked();
    const refused = panel.locator('[data-testid=processing-refused-animation]');
    const reason = await refused.count() ? (await refused.innerText()).trim() : '';
    // Either the panel brings in a new rigging step that runs on the saved file (animation then takes
    // that new task), or animation stands alone and says the original task expired.
    if (rigging) {
      const riggingRoute = (await panel.locator('[data-testid=processing-route-rigging]').innerText()).trim();
      assert(!riggingRoute.includes(text('ui.workspace.processing.task_route')), `rigging refers to the expired task: ${riggingRoute}`);
      await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-R-animation.png`) });
      return `rigging brought in (${riggingRoute}); animation takes the new rigging task (${route})`;
    }
    assert(reason.includes('保留期') && !route.includes(text('ui.workspace.processing.task_route')), `route "${route}" reason "${reason}"`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-R-animation.png`) });
    return reason;
  });

  const box = page.locator('[data-testid=studio-composer] textarea');
  const say = async (words) => {
    await box.fill(words);
    await page.locator('[data-testid=composer-send]').click();
  };
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
  const replies = page.locator('[data-testid=chat-message][data-role=assistant]');

  await step('A: Agent 重贴图 on the aged model runs Meshy Retexture from the saved file', async () => {
    await openStudio(page, session.id);
    await showTask(page, old.id);
    const before = new Set((await sessionTasks(context, session.id)).map((task) => task.id));
    await say('给当前模型重贴图，暖色胡桃木');
    const card = page.locator('[data-testid=chat-choice-card][data-state=pending]');
    const approval = page.locator('[data-testid=studio-approval-approve]');
    const created = await waitFor(async () => {
      if (await card.count()) {
        const option = card.locator(`[data-option-id="${RETEXTURE}"]`);
        assert(await option.count(), 'the choice card does not offer Meshy Retexture');
        await option.click();
        await card.locator('[data-testid=chat-choice-confirm]').click();
      }
      if (await approval.count() && await approval.first().isEnabled()) await approval.first().click();
      return (await sessionTasks(context, session.id)).find((task) => !before.has(task.id) && task.offering === RETEXTURE);
    }, 'the Agent created no Meshy Retexture task', 90000);
    const task = await waitTask(context, created.id);
    const frozen = frozenSource(task);
    assert(frozen.asset && !frozen.task, `frozen input ${frozen.flat}`);
    await idle();
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-A-agent.png`) });
    return `task ${task.id} ${task.state}`;
  });

  await step('X: Agent 动画 on an aged Meshy rigging result is refused before any task, naming the retention', async () => {
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await showTask(page, rig.id);
    const before = (await sessionTasks(context, session.id)).length;
    const count = await replies.count();
    await say('给当前模型做动画，跑步');
    await idle();
    await waitFor(async () => (await replies.count()) > count, 'no reply');
    const reply = await replies.last().innerText();
    assert(/保留期|retention|no longer keeps/.test(reply), `reply does not name the retention: ${reply}`);
    await new Promise((resolve) => setTimeout(resolve, 1500));
    assert.equal((await sessionTasks(context, session.id)).length, before, 'a task was created');
    assert.equal(await page.locator('[data-testid=studio-approval-card]').count() > 0
      && await page.locator('[data-testid=studio-approval-approve]').isEnabled().catch(() => false), false, 'an approval card is waiting');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-X-refused.png`) });
    return reply.slice(0, 240);
  });

  await step('no page errors and no outside requests', async () => {
    assert.equal(errors.length, 0, `page errors: ${errors.join(' | ')}`);
    assert.equal(outside.length, 0, `outside requests: ${outside.join(' | ')}`);
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
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
process.exitCode = results.every((result) => result.passed) ? 0 : 1;
