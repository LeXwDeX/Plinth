#!/usr/bin/env node
/**
 * Real-browser checks for the second round of #321 follow-ups (dev-test acceptance note 99042: G, H and
 * the offering_id leak; user rule 98950). No credentials and no paid upstream: a local server with mock
 * providers, the mock Agent and mock login, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18431 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18431 OUT=<scratch>/out node scripts/test-processing-followups-2.mjs
 *
 * Per engine and viewport, in a session whose first model is a ~80 MiB Rodin result ([mock:large_model])
 * and whose second is a Bang result made from it (as large, so over the Bang local-model limit):
 *  G. with the Bang result selected, 「再把这个模型分割成部件一次，用 Bang」 runs nothing: no task, no
 *     approval, no choice card; the reply says why Bang cannot take the model (size and limit) and
 *     proposes the original model @model1. The mock Agent calls without a service, the way the real
 *     Agent did after the refusal on dev-test, so the tool-layer guard is what holds.
 *  H. 「用原模型，用 Bang」 (no handle) runs Bang on @model1 at once: one Bang task on the Rodin source,
 *     no second question.
 *  ID. no assistant reply of the session contains an internal Offering ID.
 *
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18431');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'processing-followups-2-'));
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const SOURCE_OFFERING = 'hyper3d/rodin-gen-2.5/text-to-3d';
const BANG_RODIN = 'hyper3d/bang/rodin-task';
const OFFERING_ID = /\b(?:hyper3d|tripo|meshy|local|matting)\/[a-z0-9.-]+/;

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
    assert(!['FAILED', 'CANCELED'].includes(task.state), `task ${taskID} ${task.state}`);
    assert(Date.now() < deadline, `task ${taskID} did not finish`);
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

async function sessionTasks(context, sessionID) {
  const listed = await api(context, 'GET', `${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=50`);
  return (listed.tasks ?? listed.items ?? []).map((task) => ({ id: task.id, offering: task.offering_id ?? task.offering?.id, state: task.state }));
}

async function pendingApprovals(context, sessionID) {
  const body = await api(context, 'GET', routes.chat_session_pending_approvals.replace('{sessionID}', sessionID));
  const list = Object.values(body).find(Array.isArray) ?? [];
  return list.filter((item) => !item.status || item.status === 'pending');
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

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  await login(context, `followups2-${engine}-${viewport}-${Date.now()}`);
  const session = await api(context, 'POST', legacy.chat_sessions, { title: `#321 round 2 ${engine} ${viewport}` });
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: SOURCE_OFFERING,
    input: { prompt: 'A tall carved wooden totem [mock:large_model]', authorize_extra_credits: true } });
  const rodin = await waitTask(context, (await approveAndSubmit(context, prepared, `followups2-${prepared.prepared_input_id}`)).task_id);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await openStudio(page, session.id);
  const step = (name, fn) => record(engine, viewport, name, fn);
  const box = page.locator('[data-testid=studio-composer] textarea');
  const say = async (words) => {
    await box.fill(words);
    await page.locator('[data-testid=composer-send]').click();
  };
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
  const openCard = () => page.locator('[data-testid=chat-choice-card][data-state=pending]');
  const replies = page.locator('[data-testid=chat-message][data-role=assistant]');
  // The reply is read once its text has settled (the handle chip and the body render after the turn ends).
  const lastReplyAfter = async (count) => {
    await waitFor(async () => (await replies.count()) > count, 'no reply');
    await idle();
    let previous = '';
    return waitFor(async () => {
      const current = await replies.last().innerText();
      const settled = current.trim().length > 12 && current === previous;
      previous = current;
      return settled ? current : null;
    }, 'the reply did not settle', 20000);
  };

  let bangTask = '';
  await step('setup: Bang (Rodin source) on the ~80 MiB Rodin model, chosen on the card', async () => {
    await showTask(page, rodin.id);
    await say('给当前模型做模型分割');
    await openCard().waitFor({ timeout: 30000 });
    await idle();
    const card = openCard();
    await card.locator(`[data-option-id="${BANG_RODIN}"]`).click();
    await card.locator('[data-testid=chat-choice-confirm]').click();
    await idle();
    const task = await waitFor(async () => (await sessionTasks(context, session.id)).find((item) => item.offering === BANG_RODIN), 'Bang did not run');
    const done = await waitTask(context, task.id);
    const glb = done.assets?.find((asset) => asset.role === 'model' && asset.variant === 'glb');
    assert(glb && glb.size > 10 * 1024 * 1024, `the Bang result is ${glb?.size} bytes, not over the 10 MiB Bang local-model limit`);
    bangTask = task.id;
    return `Bang result ${task.id}, ${(glb.size / 1048576).toFixed(1)} MiB`;
  });

  await step('G: 用 Bang on a model Bang cannot take runs nothing and asks with the reason', async () => {
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await showTask(page, bangTask);
    const before = (await sessionTasks(context, session.id)).length;
    const count = await replies.count();
    await say('再把这个模型分割成部件一次，用 Bang');
    const reply = await lastReplyAfter(count);
    for (const want of ['Bang', '10.48 MB', '@model1']) assert(reply.includes(want), `the reply does not say ${want}: ${reply}`);
    await new Promise((resolve) => setTimeout(resolve, 1500));
    const after = await sessionTasks(context, session.id);
    assert.equal(after.length, before, `a task was created: ${JSON.stringify(after.map((task) => task.offering))}`);
    assert.equal((await pendingApprovals(context, session.id)).length, 0, 'an approval is pending');
    assert.equal(await openCard().count(), 0, 'a choice card opened');
    assert.equal(await page.locator('[data-testid=studio-approval-approve]').count(), 0, 'an approval card is shown');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-G-asks.png`) });
    return reply.slice(0, 240);
  });

  await step('H: one answer (用原模型，用 Bang) runs Bang on @model1 without asking again', async () => {
    const before = await sessionTasks(context, session.id);
    const count = await replies.count();
    await say('用原模型，用 Bang');
    const reply = await lastReplyAfter(count);
    const created = await waitFor(async () => (await sessionTasks(context, session.id)).filter((task) => !before.some((old) => old.id === task.id)),
      `nothing ran after the answer; reply: ${reply}`, 30000).then((tasks) => (tasks.length ? tasks : null));
    assert.equal(created.length, 1, `tasks created: ${JSON.stringify(created)}`);
    // The Rodin-source Bang path takes only a Rodin text/image-to-3D task: the only one here is @model1
    // (the public task view withholds source parameters).
    assert.equal(created[0].offering, BANG_RODIN, `ran ${created[0].offering}`);
    assert(!/source_confirmation_required|Nothing was submitted/.test(reply), `asked again: ${reply}`);
    await waitTask(context, created[0].id);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-H-ran.png`) });
    return `task ${created[0].id} (${created[0].offering}); reply: ${reply.slice(0, 120)}`;
  });

  await step('ID: no assistant reply names an internal Offering ID', async () => {
    const texts = await replies.allInnerTexts();
    const leaks = texts.filter((text) => OFFERING_ID.test(text));
    assert.equal(leaks.length, 0, `replies with IDs: ${leaks.map((text) => text.match(OFFERING_ID)[0]).join(', ')}`);
    return `${texts.length} replies checked`;
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
