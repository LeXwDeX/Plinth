#!/usr/bin/env node
/**
 * Real-browser checks for #326 P and the chat card that follows it: a paused or waiting processing plan, and a
 * chat step that failed because its source's original task is gone, are judged by the rule failed plans use
 * before a paid continue or regenerate is offered.
 * No credentials and no paid upstream: a local server with mock providers, the mock Agent and mock login.
 *
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18471 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18471 DATABASE_PATH=<scratch>/a.db OUT=<scratch>/out node scripts/test-paused-plan.mjs
 *
 * The script ages Tasks and writes one paused plan in that SQLite file (sqlite3 CLI). Per engine and viewport:
 *  P1. a retexture plan waiting for consent froze the source's original Meshy task, which is now past its age: the
 *      panel says the step will be prepared again on the saved file, offers 继续 (not 准备本步骤确认), and the
 *      model is free for another plan; 继续 freezes the saved file and asks for consent again; confirming runs
 *      the step on the saved file;
 *  P2. an animation plan waiting for consent on a rigging result past its age (animation takes no saved file)
 *      is shown failed with the reason, offers no continue, and the model is free for another plan;
 *  C.  "用 Meshy 换贴图 … [mock:source_expired]": after the provider lost the source job, the failed card says the
 *      regenerate uses the saved model file and keeps it.
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18471');
  process.exit(2);
}
const DATABASE = process.env.DATABASE_PATH || '';
assert(DATABASE, 'DATABASE_PATH must name the server\'s SQLite file');
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'paused-plan-'));
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
const credits = offering(RETEXTURE).estimated_credits;

async function api(context, method, route, data, headers = {}) {
  let response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  for (let attempt = 0; response.status() === 429 && method === 'GET' && attempt < 10; attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 3000));
    response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  }
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return body;
}

async function login(context) {
  const user = `paused-${Date.now()}`;
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
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

async function waitTask(context, taskID, timeout = 180000) {
  return waitFor(async () => {
    const task = await api(context, 'GET', routes.task_item.replace('{taskID}', taskID));
    assert(!['FAILED', 'CANCELED'].includes(task.state), `task ${taskID} ${task.state} ${JSON.stringify(task.failure ?? {})}`);
    return task.state === 'SUCCEEDED' ? task : null;
  }, `task ${taskID} did not finish`, timeout);
}

async function submitTask(context, sessionID, capability, offeringID, input) {
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: sessionID, capability, offering_id: offeringID, input });
  const key = `paused-${prepared.prepared_input_id}`;
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  return waitTask(context, accepted.task_id);
}

const quote = (value) => `'${String(value).replace(/'/g, "''")}'`;
const sql = (statement) => execFileSync('sqlite3', [DATABASE, statement]).toString().trim();
const frozenInput = (preparedID) => JSON.parse(sql(`SELECT json_extract(payload_json, '$.normalized_input') FROM generation_prepared_inputs WHERE id = ${quote(preparedID)}`) || '{}');

/** Moves a Task's creation back past the declared age, as if it were generated days ago. */
function age(taskID) {
  const created = new Date(Date.now() - (maxAge + 3 * 24 * 3600) * 1000).toISOString();
  sql(`UPDATE generation_tasks SET created_at = ${quote(created)} WHERE id = ${quote(taskID)}`);
}

const planOf = (context, planID) => api(context, 'GET', routes.processing_plan_item.replace('{planID}', planID));
const glbOf = (task) => task.assets.find((asset) => asset.role === 'model' && asset.media_type === 'model/gltf-binary' && asset.required !== false)
  ?? task.assets.find((asset) => asset.role === 'model');

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

/** A retexture plan made in the panel's way that waits for consent, its frozen input naming the original task. */
async function waitingRetexturePlan(context, sessionID) {
  const source = await submitTask(context, sessionID, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A stone bench' });
  const created = await api(context, 'POST', routes.processing_plans, { session_id: sessionID, source_task_id: source.id, source_asset_id: glbOf(source).id,
    steps: [{ offering_id: RETEXTURE, expected_offering_revision: offering(RETEXTURE).revision, input: { prompt: 'warm walnut' } }] },
  { 'Idempotency-Key': `paused-plan-${source.id}` });
  const plan = await waitFor(async () => {
    const current = await planOf(context, created.id);
    return current.state === 'PAUSED' && current.failure === 'plan_consent_required' ? current : null;
  }, 'the retexture plan did not wait for consent', 60000);
  const frozen = frozenInput(plan.steps[0].prepared_input_id);
  assert.equal(frozen.source_task, source.id, `the plan froze ${JSON.stringify(frozen)}`);
  age(source.id);
  return { source, plan };
}

// The shared setup: one owner, one session, an animation plan on an old rigging result.
const setup = await pw.chromium.launch();
const setupContext = await setup.newContext();
await login(setupContext);
const session = await api(setupContext, 'POST', legacy.chat_sessions, { title: '#326 paused plans' });
const puppet = await submitTask(setupContext, session.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A small wooden puppet' });
const rig = await submitTask(setupContext, session.id, 'rigging', RIGGING, { source_task: puppet.id });
const animationPrepared = await api(setupContext, 'POST', routes.task_prepare, { session_id: session.id, capability: 'animation', offering_id: ANIMATION,
  input: { source_task: rig.id, action_id: 14 } });
{
  // Stored as the worker stores a step waiting for consent.
  const owner = sql(`SELECT owner_id FROM generation_tasks WHERE id = ${quote(rig.id)}`);
  const id = `paused-animation-${Date.now()}`;
  const rigAsset = rig.assets.find((asset) => asset.role === 'model' && asset.required) ?? glbOf(rig);
  const steps = [{ capability: 'animation', offering_id: ANIMATION, input: { action_id: 14 }, estimated_credits: offering(ANIMATION).estimated_credits,
    revision: offering(ANIMATION).revision, state: 'PAUSED', retry: 0, failure: 'plan_consent_required', resumable: true,
    prepared_input_id: animationPrepared.prepared_input_id, prepared_input_hash: animationPrepared.input_hash ?? '', operation_id: id, plan_step_id: '0:0',
    idempotency_key: `processing:${id}:0:0` }];
  const now = new Date(Date.now() - 3600 * 1000).toISOString();
  sql(`INSERT INTO processing_plans (id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits, request_hash, steps_json, failure, created_at, updated_at, version)
    VALUES (${quote(id)}, ${quote(owner)}, ${quote(session.id)}, ${quote(rig.id)}, ${quote(rigAsset.id)}, 'PAUSED', ${steps[0].estimated_credits}, 'paused-plan', ${quote(JSON.stringify(steps))}, 'plan_consent_required', ${quote(now)}, ${quote(now)}, 2)`);
  age(rig.id);
}
const signedIn = await setupContext.storageState();
const setupRuns = [];
for (const engine of ENGINES) for (const [width, height] of VIEWPORTS) setupRuns.push(`${engine}-${width}x${height}`);
const waiting = {};
for (const run of setupRuns) waiting[run] = await waitingRetexturePlan(setupContext, session.id);
await setup.close();
writeFileSync(path.join(OUT, 'fixtures.json'), `${JSON.stringify({ session: session.id, puppet: puppet.id, rig: rig.id,
  waiting: Object.fromEntries(Object.entries(waiting).map(([run, value]) => [run, { source: value.source.id, plan: value.plan.id }])) }, null, 2)}\n`);

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside, signedIn);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  const { source, plan } = waiting[`${engine}-${viewport}`];

  await step('P1: a waiting plan whose frozen input names an expired task offers 继续 on the saved file and holds nothing', async () => {
    await openStudio(page, session.id);
    const panel = await showTask(page, source.id);
    await panel.getByText(text('ui.workspace.processing.plan_resume_saved_file')).waitFor({ timeout: 15000 });
    await panel.getByText(text('ui.workspace.processing.plan_waiting_alternative')).waitFor({ timeout: 5000 });
    assert.equal(await panel.getByRole('button', { name: new RegExp(text('ui.workspace.processing.plan_prepare_step')) }).count(), 0, 'consent is asked for the old input');
    const resume = panel.getByRole('button', { name: `${text('ui.workspace.processing.plan_resume')} · ${credits}` });
    assert(await resume.isEnabled(), 'no continue offered');
    assert(await panel.getByRole('checkbox', { name: label('retexture') }).isEnabled(), 'the panel is locked');
    await shot('P1-saved-file');
  });

  await step('P1: 继续 freezes the saved file and asks for consent again; confirming runs it on the saved file', async () => {
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.getByRole('button', { name: `${text('ui.workspace.processing.plan_resume')} · ${credits}` }).click();
    const prepare = panel.getByRole('button', { name: `${text('ui.workspace.processing.plan_prepare_step')} · ${credits}` });
    await prepare.waitFor({ timeout: 30000 });
    const refrozen = await planOf(context, plan.id);
    const stepNow = refrozen.steps[0];
    assert.equal(stepNow.failure, 'plan_consent_required', `step ${JSON.stringify(stepNow)}`);
    assert.notEqual(stepNow.prepared_input_id, plan.steps[0].prepared_input_id, 'the old input is still frozen');
    const frozen = frozenInput(stepNow.prepared_input_id);
    assert(frozen.source_asset && !frozen.source_task, `frozen again ${JSON.stringify(frozen)}`);
    await shot('P1-consent-again');
    await prepare.click();
    const confirm = panel.getByRole('button', { name: `${text('ui.workspace.processing.plan_confirm_step')} · ${credits}` });
    await confirm.waitFor({ timeout: 15000 });
    await confirm.click();
    const done = await waitFor(async () => {
      const current = await planOf(context, plan.id);
      assert.notEqual(current.state, 'FAILED', `plan failed ${JSON.stringify(current)}`);
      return current.state === 'SUCCEEDED' ? current : null;
    }, 'the plan did not finish', 120000);
    const ran = sql(`SELECT input_json FROM generation_tasks WHERE id = ${quote(done.steps[0].task_id)}`);
    assert(ran.includes('source_asset') && !ran.includes(source.id), `the step ran on ${ran}`);
    await shot('P1-done');
    return done.steps[0].task_id;
  });

  await step('P2: a waiting animation plan on an expired rigging result offers no continue, says why and holds nothing', async () => {
    await openStudio(page, session.id);
    const panel = await showTask(page, rig.id);
    await panel.getByText(text('ui.workspace.processing.plan_resume_source_expired')).waitFor({ timeout: 15000 });
    for (const key of ['ui.workspace.processing.plan_prepare_step', 'ui.workspace.processing.plan_resume', 'ui.workspace.processing.plan_retry']) {
      assert.equal(await panel.getByRole('button', { name: new RegExp(text(key)) }).count(), 0, `${key} is offered`);
    }
    assert(await panel.getByRole('checkbox', { name: label('animation') }).isEnabled(), 'the panel is locked');
    await shot('P2-no-continue');
  });

  await step('C: a failed card whose provider lost the source job keeps its regenerate and says it uses the saved file', async () => {
    const own = await api(context, 'POST', legacy.chat_sessions, { title: `#326 card ${engine} ${viewport}` });
    const fresh = await submitTask(context, own.id, 'text-to-3d', SOURCE_OFFERING, { prompt: 'A small wooden stool' });
    await openStudio(page, own.id);
    await showTask(page, fresh.id);
    await page.locator('[data-testid=studio-composer] textarea').fill('用 Meshy 换贴图：温暖的胡桃木纹理 [mock:source_expired]');
    await page.locator('[data-testid=composer-send]').click();
    await page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 90000 });
    for (let waited = 0; waited < 20000; waited += 500) {
      if (await page.locator('[data-testid=studio-approval-approve]').count()) {
        await page.locator('[data-testid=studio-approval-approve]').first().click();
        break;
      }
      await page.waitForTimeout(500);
    }
    const note = page.locator('[data-testid=paid-step-source-retry]');
    await note.first().waitFor({ timeout: 90000 });
    const said = (await note.first().innerText()).trim();
    assert.equal(said, text('ui.studio.result.source_retry.saved_file'), `note "${said}"`);
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await page.locator('[data-testid=paid-step-source-retry]').first().waitFor({ timeout: 30000 });
    assert(await page.locator('[data-testid=chat-regenerate]').count() > 0, 'the regenerate is gone');
    await shot('C-card');
    return said;
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
      await new Promise((resolve) => setTimeout(resolve, 15000));
    }
  } finally {
    await browser.close();
  }
}
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
process.exitCode = results.every((result) => result.passed) ? 0 : 1;
