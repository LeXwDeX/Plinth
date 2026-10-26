#!/usr/bin/env node
/**
 * Real-browser checks for #326: the main model of a Meshy Rigging result is its required rigged character, not
 * the basic-animation attachments the result lists first; a prerequisite step that cannot run on the model is not
 * brought in and the panel says why; the Agent says the same without internal field names; and a plan that failed
 * before #325 is judged again before a paid retry is offered.
 * No credentials and no paid upstream: a local server with mock providers, the mock Agent and mock login.
 *
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18461 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18461 DATABASE_PATH=<scratch>/a.db OUT=<scratch>/out node scripts/test-rigged-source.mjs
 *
 * The mock Meshy Rigging result carries the six basic-animation files before the required character, like the real
 * one; [mock:large_model] grows the character to about 80 MiB, past Meshy Rigging's 70 MiB. The script ages Tasks
 * and writes two failed plans the way pre-#325 plans were stored, in that SQLite file (sqlite3 CLI). Per engine and
 * viewport:
 *  S. the large rigged result: the panel's source is the rigged character;
 *  R. 动画 on it: 骨骼绑定 is not brought in, the step says why (73.4 MB) and what works;
 *  F. 动画 on a rigged result Meshy Rigging takes: 骨骼绑定 is brought in, from the saved file;
 *  O. an old failed retexture plan offers its retry with the saved-file reason; an old failed animation plan on an
 *     expired rigging result offers no retry and says why;
 *  X. the mock Agent asked to animate the large rigged result refuses before any task, names the 73.4 MB limit and
 *     no internal field name;
 *  N1a. a recovered result (#295 refetch row) whose upstream job belongs to an old failed Task: the task reports the
 *     job's age from that Task and 重贴图 goes through the saved file;
 *  K. a ~80 MiB Rodin model, Bang on it, then with the Bang result selected "用 Bang 再分割一次": the mock Agent goes
 *     back to the first model on its own; the reply says why Bang cannot take the selected model (10 MB) and offers
 *     no Bang option on it; nothing runs;
 *  L. that question's tool result names both models by the version strip's labels (read from the turn's record);
 *  N2. "用 Meshy 换贴图 … [mock:source_expired]" on a fresh Meshy model: the provider answers as one that lost the
 *     source job; the chat says the source's original task expired (Schema text), not just 生成失败;
 *  N1b. after that 404 the model's job is gone: the task says so and 重贴图 goes through the saved file.
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18461');
  process.exit(2);
}
const DATABASE = process.env.DATABASE_PATH || '';
assert(DATABASE, 'DATABASE_PATH must name the server\'s SQLite file');
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'rigged-source-'));
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
const ANIMATION = 'meshy/processing/animation';
const offering = (id) => offerings.find((entry) => entry.id === id);
const maxAge = offering(RETEXTURE).parameters.find((parameter) => parameter.name === 'source_task').upstream_job_max_age_seconds;
assert(maxAge > 0, 'retexture source_task declares no upstream_job_max_age_seconds');
const PREREQUISITE_HEAD = text('ui.workspace.processing.prerequisite_refused').split('{')[0];
const INTERNAL = ['basic_animations', 'armature', '_glb_url', '_fbx_url'];
const RODIN = 'hyper3d/rodin-gen-2.5/text-to-3d';
const BANG_RODIN = 'hyper3d/bang/rodin-task';
const EXPIRED_TEXT = application.chat.presentation.messages.generation_failed_by_class.source_expired;
assert(EXPIRED_TEXT, 'generation_failed_by_class.source_expired missing');

async function api(context, method, route, data, headers = {}) {
  let response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  // Every engine and viewport is the same owner, so the per-owner rate limit can answer 429: wait and read again.
  for (let attempt = 0; response.status() === 429 && method === 'GET' && attempt < 10; attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 3000));
    response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  }
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return Object.assign(body, { httpStatus: response.status() });
}

async function login(context) {
  const user = `rigged-${Date.now()}`;
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
}

async function waitTask(context, taskID, timeout = 180000) {
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
  const key = `rigged-${prepared.prepared_input_id}`;
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  return waitTask(context, accepted.task_id);
}

const quote = (value) => `'${String(value).replace(/'/g, "''")}'`;
function sql(statement) {
  return execFileSync('sqlite3', [DATABASE, statement]).toString().trim();
}

/** Moves a Task's creation back past the declared age, as if it were generated days ago. */
function age(taskID) {
  const created = new Date(Date.now() - (maxAge + 3 * 24 * 3600) * 1000).toISOString();
  sql(`UPDATE generation_tasks SET created_at = ${quote(created)} WHERE id = ${quote(taskID)}`);
  assert.equal(sql(`SELECT created_at FROM generation_tasks WHERE id = ${quote(taskID)}`), created, `created_at of ${taskID} not rewritten`);
}

/**
 * Stores what a plan that failed before #325 looks like: its step Task failed as `rejected` (the upstream answered
 * 404 for the original task) and the plan kept plan_task_failed with a retryable step.
 */
function oldFailedPlan(id, source, step) {
  sql(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ${quote(JSON.stringify({ class: 'rejected', retryable: false, detail: 'Task not found' }))} WHERE id = ${quote(step.task.id)}`);
  const owner = sql(`SELECT owner_id FROM generation_tasks WHERE id = ${quote(source.task.id)}`);
  const session = sql(`SELECT session_id FROM generation_tasks WHERE id = ${quote(source.task.id)}`);
  const steps = [{ capability: step.capability, offering_id: step.offering, input: step.input, estimated_credits: offering(step.offering).estimated_credits,
    revision: offering(step.offering).revision, state: 'FAILED', task_id: step.task.id, retry: 0, failure: 'plan_task_failed', retryable: true }];
  const now = new Date(Date.now() - 3600 * 1000).toISOString();
  sql(`INSERT INTO processing_plans (id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits, request_hash, steps_json, failure, created_at, updated_at, version)
    VALUES (${quote(id)}, ${quote(owner)}, ${quote(session)}, ${quote(source.task.id)}, ${quote(source.asset)}, 'FAILED', ${steps[0].estimated_credits}, 'old-plan', ${quote(JSON.stringify(steps))}, 'plan_task_failed', ${quote(now)}, ${quote(now)}, 3)`);
}

const mainGLB = (task) => task.assets.find((asset) => asset.role === 'model' && asset.variant === 'rigged_character_glb_url');
const firstGLB = (task) => task.assets.filter((asset) => asset.role === 'model' && asset.media_type === 'model/gltf-binary').sort((a, b) => a.ordinal - b.ordinal)[0];

async function sessionTasks(context, sessionID) {
  const listed = await api(context, 'GET', `${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=50`);
  return (listed.tasks ?? listed.items ?? []).map((task) => ({ id: task.id, offering: task.offering_id ?? task.offering?.id, state: task.state }));
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

async function guardedContext(browser, viewport, outside, storageState) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block', storageState });
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
  await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
  const panel = page.locator('[data-testid=model-processing-panel]');
  await panel.waitFor({ timeout: 20000 });
  return panel;
}

// One set of sources for every engine and viewport: the large GLB is about 80 MiB.
const setup = await pw.chromium.launch();
const setupContext = await setup.newContext();
await login(setupContext);
const session = await api(setupContext, 'POST', legacy.chat_sessions, { title: '#326 rigged source' });
const big = await submitTask(setupContext, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A tall armored knight [mock:large_model]' });
const bigRig = await submitTask(setupContext, session.id, 'rigging', RIGGING, { source_task: big.id });
const small = await submitTask(setupContext, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A small wooden puppet' });
const smallRig = await submitTask(setupContext, session.id, 'rigging', RIGGING, { source_task: small.id });
const smallRig2 = await submitTask(setupContext, session.id, 'rigging', RIGGING, { source_task: small.id, height_meters: 1.2 });
const retexSource = await submitTask(setupContext, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A stone bench' });
const retexInput = { prompt: 'warm walnut' };
const retexTask = await submitTask(setupContext, session.id, 'retexture', RETEXTURE, { source_task: retexSource.id, ...retexInput });
const animTask = await submitTask(setupContext, session.id, 'animation', ANIMATION, { source_task: smallRig2.id, action_id: 14 });
// N1a: a recovered result whose upstream job is an old failed Task's (#295 refetch).
const holder = await submitTask(setupContext, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A wooden crate' });
const recovered = await submitTask(setupContext, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A wooden crate, recovered' });
// Every engine and viewport signs in as this owner (a mock login makes a new user each time).
const signedIn = await setupContext.storageState();
await setup.close();

const character = mainGLB(bigRig);
assert(character && character.size > 73400320, `large rigged character ${JSON.stringify(character)}`);
assert(firstGLB(bigRig).variant.startsWith('basic_animations.'), `the mock result does not list its attachments first: ${firstGLB(bigRig).variant}`);
oldFailedPlan(`old-retexture-${Date.now()}`, { task: retexSource, asset: retexSource.assets.find((asset) => asset.variant === 'glb').id },
  { capability: 'retexture', offering: RETEXTURE, input: retexInput, task: retexTask });
oldFailedPlan(`old-animation-${Date.now()}`, { task: smallRig2, asset: mainGLB(smallRig2).id },
  { capability: 'animation', offering: ANIMATION, input: { action_id: 14 }, task: animTask });
for (const task of [big, bigRig, small, smallRig, smallRig2, retexSource, holder]) age(task.id);
{
  const owner = sql(`SELECT owner_id FROM generation_tasks WHERE id = ${quote(holder.id)}`);
  sql(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ${quote(JSON.stringify({ class: 'integrity_failure', retryable: false, detail: 'storage failed' }))} WHERE id = ${quote(holder.id)}`);
  sql(`INSERT INTO generation_upstream_refetches (task_id, owner_id, session_id, source_task_id, source_attempt_id, ordinal, requested_by, created_at)
    VALUES (${quote(recovered.id)}, ${quote(owner)}, ${quote(session.id)}, ${quote(holder.id)}, ${quote(`attempt-${holder.id}`)}, 1, 'owner', ${quote(new Date().toISOString())})`);
}
const holderCreated = sql(`SELECT created_at FROM generation_tasks WHERE id = ${quote(holder.id)}`);
writeFileSync(path.join(OUT, 'fixtures.json'), `${JSON.stringify({ session: session.id, big: big.id, bigRig: bigRig.id, character: { id: character.id, size: character.size },
  small: small.id, smallRig: smallRig.id, smallRig2: smallRig2.id, retexSource: retexSource.id, retexTask: retexTask.id, animTask: animTask.id,
  holder: holder.id, recovered: recovered.id }, null, 2)}\n`);

/** The route the panel shows for 重贴图 on a task: it must be the saved file, not the same-provider task. */
async function retextureRoute(page, taskID) {
  const panel = await showTask(page, taskID);
  await panel.getByRole('checkbox', { name: label('retexture') }).check({ timeout: 20000 });
  const route = panel.locator('[data-testid=processing-route-retexture]');
  await route.waitFor({ timeout: 10000 });
  const shown = (await route.innerText()).trim();
  assert(shown.includes(text('ui.workspace.processing.asset_route')) && !shown.includes(text('ui.workspace.processing.task_route')), `retexture route "${shown}"`);
  await panel.getByRole('checkbox', { name: label('retexture') }).uncheck();
  return shown;
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside, signedIn);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });

  await step('S: the large rigged result\'s source is the rigged character, not a skeleton attachment', async () => {
    await openStudio(page, session.id);
    const panel = await showTask(page, bigRig.id);
    // #327: the row names the public format, never the result field; the file is the rigged character.
    const row = panel.locator('[data-testid=processing-source-label]');
    const source = (await row.innerText()).trim();
    assert.equal(source, 'meshy · GLB', `panel source "${source}"`);
    assert.equal(await row.getAttribute('data-asset-id'), mainGLB(bigRig).id, 'the panel source is not the rigged character');
    const glb = await page.evaluate(() => [...document.querySelectorAll('[data-testid=stage-3d] model-viewer, [data-testid=stage-3d] [src]')].map((node) => node.getAttribute('src') ?? '').join(' '));
    if (glb) assert(!glb.includes(firstGLB(bigRig).id), `the stage loads the skeleton attachment: ${glb}`);
    await shot('S-source');
    return source;
  });

  await step('R: 动画 on it brings in no rigging step and says why (73.4 MB) and what works', async () => {
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.getByRole('checkbox', { name: label('animation') }).check({ timeout: 20000 });
    await page.waitForTimeout(300);
    assert.equal(await panel.getByRole('checkbox', { name: label('rigging') }).isChecked(), false, 'rigging was brought in');
    const reason = (await panel.locator('[data-testid=processing-refused-animation]').innerText()).trim();
    assert(reason.includes(PREREQUISITE_HEAD.replace('{prerequisite}', '')) || reason.includes('也不行'), `reason "${reason}"`);
    assert(reason.includes('73.4 MB') && reason.includes(label('rigging')), `reason "${reason}"`);
    for (const name of INTERNAL) assert(!reason.includes(name), `reason names ${name}: ${reason}`);
    const confirm = panel.getByRole('button', { name: new RegExp(text('ui.workspace.processing.plan_confirm')) });
    assert(await confirm.isDisabled(), 'confirm is enabled');
    await shot('R-animation');
    return reason;
  });

  await step('F: 动画 on a rigged result Meshy Rigging takes brings in rigging from the saved file', async () => {
    await openStudio(page, session.id);
    const panel = await showTask(page, smallRig.id);
    await panel.getByRole('checkbox', { name: label('animation') }).check({ timeout: 20000 });
    await waitFor(() => panel.getByRole('checkbox', { name: label('rigging') }).isChecked(), 'rigging was not brought in', 10000);
    const route = (await panel.locator('[data-testid=processing-route-rigging]').innerText()).trim();
    assert(!route.includes(text('ui.workspace.processing.task_route')), `rigging route "${route}"`);
    await shot('F-rigging');
    return route;
  });

  await step('O1: an old failed retexture plan offers its retry with the saved-file reason', async () => {
    await openStudio(page, session.id);
    const panel = await showTask(page, retexSource.id);
    await panel.getByText(text('ui.workspace.processing.plan_retry_saved_file')).waitFor({ timeout: 15000 });
    assert(await panel.getByRole('button', { name: new RegExp(text('ui.workspace.processing.plan_retry')) }).count() > 0, 'no retry offered');
    await shot('O1-saved-file');
  });

  await step('O2: an old failed animation plan on an expired rigging result offers no retry and says why', async () => {
    await openStudio(page, session.id);
    const panel = await showTask(page, smallRig2.id);
    await panel.getByText(text('ui.workspace.processing.plan_retry_source_expired')).waitFor({ timeout: 15000 });
    assert.equal(await panel.getByRole('button', { name: new RegExp(text('ui.workspace.processing.plan_retry')) }).count(), 0, 'a paid retry is offered');
    await shot('O2-no-retry');
  });

  await step('X: the Agent asked to animate the large rigged result names the limit and no internal field', async () => {
    await openStudio(page, session.id);
    await showTask(page, bigRig.id);
    const replies = page.locator('[data-testid=chat-message][data-role=assistant]');
    const count = await replies.count();
    const before = (await sessionTasks(context, session.id)).length;
    await page.locator('[data-testid=studio-composer] textarea').fill('给当前模型做动画，跑步');
    await page.locator('[data-testid=composer-send]').click();
    await page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
    await waitFor(async () => (await replies.count()) > count, 'no reply');
    const reply = await replies.last().innerText();
    assert(/73\.4 MB/.test(reply), `reply does not name the limit: ${reply}`);
    for (const name of INTERNAL) assert(!reply.includes(name), `reply names ${name}: ${reply}`);
    await new Promise((resolve) => setTimeout(resolve, 1500));
    assert.equal((await sessionTasks(context, session.id)).length, before, 'a task was created');
    await shot('X-agent');
    return reply.slice(0, 300);
  });

  await step('N1a: a recovered result counts its job from the failed Task that holds it; 重贴图 uses the saved file', async () => {
    const task = await api(context, 'GET', routes.task_item.replace('{taskID}', recovered.id));
    assert.equal(task.upstream_job_created_at && new Date(task.upstream_job_created_at).toISOString(), new Date(holderCreated).toISOString(),
      `upstream_job_created_at ${task.upstream_job_created_at}`);
    await openStudio(page, session.id);
    const route = await retextureRoute(page, recovered.id);
    await shot('N1a-recovered');
    return route;
  });

  // K, L, N2, N1b run in a session of their own: the handles start at @model1.
  const say = async (words) => {
    await page.locator('[data-testid=studio-composer] textarea').fill(words);
    await page.locator('[data-testid=composer-send]').click();
  };
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 90000 });
  const replies = page.locator('[data-testid=chat-message][data-role=assistant]');
  const own = await api(context, 'POST', legacy.chat_sessions, { title: `#326 K ${engine} ${viewport}` });
  const rodin = await submitTask(context, own.id, 'text-to-3d', RODIN, { prompt: 'A tall carved totem [mock:large_model]', authorize_extra_credits: true });
  let bangTask = '';
  await step('K: setup, Bang on the ~80 MiB Rodin model through the chat', async () => {
    await openStudio(page, own.id);
    await showTask(page, rodin.id);
    await say('用 Bang 做模型分割');
    const card = page.locator('[data-testid=chat-choice-card][data-state=pending]');
    await Promise.race([card.waitFor({ timeout: 30000 }).catch(() => {}), idle()]);
    if (await card.count()) {
      await card.locator(`[data-option-id="${BANG_RODIN}"]`).click();
      await card.locator('[data-testid=chat-choice-confirm]').click();
    }
    await idle();
    const task = await waitFor(async () => (await sessionTasks(context, own.id)).find((item) => item.offering === BANG_RODIN), 'Bang did not run', 60000);
    await waitTask(context, task.id);
    bangTask = task.id;
    return task.id;
  });

  await step('K: with the Bang result selected, "用 Bang 再分割一次" says why Bang cannot take it and offers no Bang on it', async () => {
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await showTask(page, bangTask);
    const before = (await sessionTasks(context, own.id)).length;
    const count = await replies.count();
    await say('用 Bang 再分割一次');
    await idle();
    await waitFor(async () => (await replies.count()) > count, 'no reply');
    // Handles render as chips on lines of their own: read the reply as one line.
    const reply = (await replies.last().innerText()).replace(/\s+/g, ' ');
    const selected = /(@model\d+) selected/.exec(reply)?.[1] ?? /选中的是 (@model\d+)/.exec(reply)?.[1];
    assert(selected, `the reply does not name the selected model: ${reply}`);
    assert(/10\.48 MB/.test(reply), `the reply does not say why (10.48 MB): ${reply}`);
    assert(/Tripo/.test(reply), `the reply offers no service that takes the selected model: ${reply}`);
    for (const match of reply.matchAll(/([^;,，；:：]*?) on (@model\d+)/g)) {
      assert(!(match[1].includes('Bang') && match[2] === selected), `an option cannot run: "${match[0]}" in ${reply}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 1500));
    assert.equal((await sessionTasks(context, own.id)).length, before, 'a task was created');
    await shot('K-reply');
    return reply.slice(0, 400);
  });

  await step('L: the question names both models by the version strip\'s labels', async () => {
    const labelOf = async (taskID) => (await page.locator(`[data-testid=version-tile][data-key="n:${taskID}"]`).first().innerText()).trim();
    const shown = { [rodin.id]: await labelOf(rodin.id), [bangTask]: await labelOf(bangTask) };
    const record = sql(`SELECT group_concat(checkpoint_json, ' ') FROM go_chat_turns WHERE session_id = ${quote(own.id)}`)
      + sql(`SELECT group_concat(content || ' ' || tool_calls, ' ') FROM go_chat_messages WHERE session_id = ${quote(own.id)}`);
    const versions = [...record.matchAll(/\\*"version\\*":\s*\\*"([^"\\]+)\\*"/g)].map((match) => match[1]);
    assert(versions.length > 0, 'the turn record holds no version label');
    for (const version of versions) {
      assert(Object.values(shown).some((value) => value.includes(version)), `tool version "${version}" is not on the strip (${JSON.stringify(shown)})`);
    }
    return `tool ${[...new Set(versions)].join(', ')}; strip ${JSON.stringify(shown)}`;
  });

  const fresh = await submitTask(context, own.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A small wooden stool' });
  await step('N2: a step the provider refuses because it lost the source job says so in the chat', async () => {
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await showTask(page, fresh.id);
    await say('用 Meshy 换贴图：温暖的胡桃木纹理 [mock:source_expired]');
    await idle();
    // Retexture waits on its approval card in the manual mode; the automatic mode runs it at once.
    let cardModel = '';
    const card = page.locator('[data-testid=studio-approval-card]').last();
    for (let waited = 0; waited < 20000; waited += 500) {
      if (await page.locator('[data-testid=studio-approval-approve]').count()) {
        cardModel = (await card.locator('[data-testid=studio-approval-input]').allInnerTexts().catch(() => [])).join(' ').replace(/\s+/g, ' ').trim();
        await page.locator('[data-testid=studio-approval-approve]').first().click();
        break;
      }
      if ((await sessionTasks(context, own.id)).some((item) => item.offering === RETEXTURE)) break;
      await page.waitForTimeout(500);
    }
    if (cardModel) {
      // L on the card: it names the model as the version strip does.
      const strip = (await page.locator(`[data-testid=version-tile][data-key="n:${fresh.id}"]`).first().innerText()).replace(/\s+/g, ' ');
      const version = /3D v\d+/.exec(strip)?.[0];
      assert(version && cardModel.includes(version), `approval card "${cardModel}" vs strip "${strip}"`);
    }
    const task = await waitFor(async () => (await sessionTasks(context, own.id)).find((item) => item.offering === RETEXTURE && item.state === 'FAILED'),
      'the retexture did not fail', 60000);
    const head = EXPIRED_TEXT.slice(0, 16);
    await waitFor(async () => (await replies.allInnerTexts()).some((reply) => reply.includes(head)), `the chat does not say "${head}"`, 60000);
    const failed = await api(context, 'GET', routes.task_item.replace('{taskID}', task.id));
    assert.equal(failed.failure?.class, 'source_expired', `failure ${JSON.stringify(failed.failure)}`);
    await shot('N2-chat');
    return `${task.id}; card ${cardModel || 'none (automatic mode)'}`;
  });

  await step('N1b: after the 404 the model\'s job is gone and 重贴图 uses the saved file', async () => {
    const task = await api(context, 'GET', routes.task_item.replace('{taskID}', fresh.id));
    assert.equal(task.upstream_job_gone, true, `upstream_job_gone ${task.upstream_job_gone}`);
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    const route = await retextureRoute(page, fresh.id);
    await shot('N1b-gone');
    return route;
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
    for (const viewport of VIEWPORTS) {
      await checks(engine, browser, viewport);
      // Let the owner's request budget recover before the next run.
      await new Promise((resolve) => setTimeout(resolve, 20000));
    }
  } finally {
    await browser.close();
  }
}
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
process.exitCode = results.every((result) => result.passed) ? 0 : 1;
