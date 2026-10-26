#!/usr/bin/env node
/**
 * Real-browser checks for the #321 follow-ups (dev-test acceptance note 98946, decisions 98949/98950).
 * No credentials and no paid upstream: a local server, mock login, and either mock providers or a
 * local stub of the Tripo wire.
 *
 * PHASE=mock (default) needs a local server with mock providers and the mock Agent, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18421 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18421 OUT=<scratch>/out node scripts/test-processing-followups.mjs
 * Per engine and viewport:
 *  C. the choice card's 用默认（…） button stays inside the card (both edges), truncates, and its
 *     tooltip holds the whole text;
 *  D. with the previous Bang result selected, 再分割一次 (the mock Agent goes back to the first model,
 *     as the real Agent did) runs nothing: the reply asks about the model; naming @model1 then
 *     proceeds (a card, as there are several services);
 *  E. on a ~80 MiB Rodin result ([mock:large_model]) 重贴图 is ticked alone (no unusable 重拓扑),
 *     says why each service refuses the model, and the panel is not stuck: 模型分割 can still be
 *     confirmed.
 * The large source is written to OUT/large-source.json for the next phase.
 *
 * PHASE=large restarts the same data in live provider mode against a local Tripo stub, with the
 * process temp dir unusable (the container's /tmp is a 64 MB tmpfs), e.g.
 *   TMPDIR=<a regular file> PROVIDER_MODE=live TRIPO_API_KEY=stub TRIPO_BASE_URL=http://127.0.0.1:18422/v3 \
 *   ...same settings and data as above... go run ./cmd/art-ai
 * then  PHASE=large STUB_PORT=18422 BASE=... OUT=<same> node scripts/test-processing-followups.mjs
 *  A. the 加工 panel lists Tripo Smart Segmentation for the ~80 MiB model, POST /tasks/prepare
 *     answers 201, and after grant and submit the stub receives exactly the stored GLB bytes.
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * PLAYWRIGHT_MODULE_PATH. Every browser request outside BASE is refused and counted.
 */
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18421');
  process.exit(2);
}
const PHASE = process.env.PHASE || 'mock';
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'processing-followups-'));
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const label = (id) => application.capabilities.find((capability) => capability.id === id).label_cn;
const SOURCE_OFFERING = 'hyper3d/rodin-gen-2.5/text-to-3d';
const TRIPO_SMART = 'tripo/geometry/smartsegment-model';
const BANG_RODIN = 'hyper3d/bang/rodin-task';

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

/** A mock Rodin text-to-3d result in a fresh session, through the public prepare → grant → submit path. */
async function rodinSource(context, title, prompt = 'A simple low-poly wooden stool') {
  const session = await api(context, 'POST', legacy.chat_sessions, { title });
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: SOURCE_OFFERING,
    input: { prompt, authorize_extra_credits: true } });
  const accepted = await approveAndSubmit(context, prepared, `followups-${prepared.prepared_input_id}`);
  const task = await waitTask(context, accepted.task_id);
  return { sessionID: session.id, taskID: accepted.task_id, task };
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

/** Puts a task's 3D result on the stage: its version tile (key n:<task>) becomes current. */
async function showTask(page, taskID) {
  const tile = page.locator(`[data-testid=version-tile][data-key="n:${taskID}"]`);
  await tile.waitFor({ timeout: 30000 });
  await tile.locator('button').first().click();
  await page.waitForFunction((key) => document.querySelector(`[data-testid=version-tile][data-key="${key}"]`)?.getAttribute('data-current') === 'true',
    `n:${taskID}`, { timeout: 10000 });
}

async function mockChecks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  const user = `followups-${engine}-${viewport}-${Date.now()}`;
  await login(context, user);
  const source = await rodinSource(context, `#321 follow-ups ${engine} ${viewport}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await openStudio(page, source.sessionID);
  const step = (name, fn) => record(engine, viewport, name, fn);
  const box = page.locator('[data-testid=studio-composer] textarea');
  const say = async (words) => {
    await box.fill(words);
    await page.locator('[data-testid=composer-send]').click();
  };
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
  const openCard = () => page.locator('[data-testid=chat-choice-card][data-state=pending]');

  await step('C: the 用默认 button stays inside the card and its hover hint has the whole text', async () => {
    await showTask(page, source.taskID);
    await say('给当前模型做模型分割');
    await openCard().waitFor({ timeout: 30000 });
    await idle();
    const card = openCard();
    const button = card.locator('[data-testid=chat-choice-default]');
    const [cardBox, buttonBox] = [await card.boundingBox(), await button.boundingBox()];
    assert(cardBox && buttonBox, 'card or button not laid out');
    const overflow = Math.round(buttonBox.x + buttonBox.width - (cardBox.x + cardBox.width));
    assert(overflow <= 0 && buttonBox.x >= cardBox.x, `button runs ${overflow}px past the card's right edge`);
    const shown = await button.innerText();
    const truncated = await button.locator('span.truncate').evaluate((node) => node.scrollWidth > node.clientWidth);
    await button.hover();
    const hint = page.locator('[data-testid=hint-tooltip] [data-testid=hint-name]');
    await hint.waitFor({ timeout: 5000 });
    const title = (await hint.innerText()).trim();
    assert(title.startsWith(text('ui.chat.choice.use_default').split('{')[0]) && title.length >= shown.trim().length, `hint ${title}`);
    await card.screenshot({ path: path.join(OUT, `${engine}-${viewport}-C-card.png`) });
    return `card right ${Math.round(cardBox.x + cardBox.width)}, button right ${Math.round(buttonBox.x + buttonBox.width)}; truncated ${truncated}; shown "${shown}"; title "${title}"`;
  });

  let bangTask = '';
  await step('D: choosing Bang (Rodin source) on the card runs it', async () => {
    const card = openCard();
    await card.locator(`[data-option-id="${BANG_RODIN}"]`).click();
    await card.locator('[data-testid=chat-choice-confirm]').click();
    await idle();
    const task = await waitFor(async () => (await sessionTasks(context, source.sessionID)).find((item) => item.offering === BANG_RODIN), 'Bang did not run');
    await waitTask(context, task.id);
    bangTask = task.id;
    return `Bang task ${task.id}`;
  });

  await step('D: 再分割一次 with the Bang result selected asks about the model and runs nothing', async () => {
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await showTask(page, bangTask);
    const before = (await sessionTasks(context, source.sessionID)).length;
    const replies = page.locator('[data-testid=chat-message][data-role=assistant]');
    const count = await replies.count();
    await say('再分割一次');
    await idle();
    await waitFor(async () => (await replies.count()) > count, 'no reply');
    const reply = await replies.last().innerText();
    assert(reply.includes('@model1') && reply.includes('@model2'), `reply does not ask about the model: ${reply}`);
    await new Promise((resolve) => setTimeout(resolve, 1500));
    const after = await sessionTasks(context, source.sessionID);
    assert.equal(after.length, before, `a task was created: ${JSON.stringify(after)}`);
    assert.equal(await openCard().count(), 0, 'a service card opened before the model was confirmed');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-D-asks.png`) });
    return reply.slice(0, 200);
  });

  await step('D: naming @model1 then proceeds to the service choice', async () => {
    await say('好的，用 @model1 再分割一次');
    await openCard().waitFor({ timeout: 30000 });
    await idle();
    const ids = await openCard().getByRole('radio').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-option-id')));
    assert(ids.includes(BANG_RODIN), `card options ${ids.join(', ')}`);
    return ids.join(', ');
  });

  // E: a model over every retexture service's limit.
  const large = await rodinSource(context, `#321 large ${engine} ${viewport}`, 'A tall carved totem [mock:large_model]');
  const model = large.task.assets?.find((asset) => asset.role === 'model' && (asset.variant === 'glb' || asset.media_type === 'model/gltf-binary'));
  if (engine === ENGINES[0] && viewport === `${VIEWPORTS[0][0]}x${VIEWPORTS[0][1]}`) {
    // The sign-in cookie too: a mock login makes a new person each time, and the next phase must be the owner.
    const { cookies } = await context.storageState();
    writeFileSync(path.join(OUT, 'large-source.json'), `${JSON.stringify({ user, cookies, sessionID: large.sessionID, taskID: large.taskID, asset: model }, null, 2)}\n`);
  }
  await step('E: 重贴图 on a ~80 MiB model is not stuck behind an unusable 重拓扑 and says why', async () => {
    assert(model && model.size > 64 * 1024 * 1024, `large model is ${model?.size} bytes`);
    await openStudio(page, large.sessionID);
    await showTask(page, large.taskID);
    await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.getByRole('checkbox', { name: label('retexture') }).check({ timeout: 20000 });
    assert.equal(await panel.getByRole('checkbox', { name: label('remesh') }).isChecked(), false, '重拓扑 was brought in');
    const why = panel.locator('[data-testid=processing-refused-retexture]');
    await why.waitFor({ timeout: 5000 });
    const reason = await why.innerText();
    assert(reason.includes('73.4 MB') && reason.includes('10.48 MB'), `reason ${reason}`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-E-retexture.png`) });
    await panel.getByRole('checkbox', { name: label('retexture') }).uncheck();
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).check();
    const consent = panel.getByRole('checkbox', { name: /同意附加格式费用/ });
    if (await consent.count()) await consent.check();
    await page.waitForFunction((name) => [...document.querySelectorAll('[data-testid=model-processing-panel] button')]
      .some((button) => button.textContent?.includes(name) && !button.disabled), text('ui.workspace.processing.plan_confirm'), { timeout: 10000 });
    return reason;
  });

  await step('no page errors and no outside requests', async () => {
    assert.equal(errors.length, 0, `page errors: ${errors.join(' | ')}`);
    assert.equal(outside.length, 0, `outside requests: ${outside.join(' | ')}`);
  });
  await context.close();
}

/** A local stand-in for the Tripo v3 wire: it accepts the upload and the create, and keeps the task running. */
function tripoStub(port) {
  const seen = { uploads: [], creates: 0 };
  const envelope = (response, data) => { response.writeHead(200, { 'Content-Type': 'application/json' }); response.end(JSON.stringify({ code: 0, data })); };
  const server = createServer((request, response) => {
    const chunks = [];
    request.on('data', (chunk) => chunks.push(chunk));
    request.on('end', () => {
      const body = Buffer.concat(chunks);
      if (request.method === 'POST' && request.url === '/v3/files') {
        const boundary = /boundary=([^;]+)/.exec(request.headers['content-type'] || '')?.[1];
        const start = body.indexOf('\r\n\r\n') + 4;
        const end = body.lastIndexOf(Buffer.from(`\r\n--${boundary}--`));
        const file = body.subarray(start, end);
        seen.uploads.push({ bytes: file.length, sha256: createHash('sha256').update(file).digest('hex') });
        return envelope(response, { file_token: 'file_stub_large' });
      }
      if (request.method === 'POST' && request.url === '/v3/mesh/smartsegment') {
        seen.creates += 1;
        return envelope(response, { task_id: 'stub-smartsegment' });
      }
      if (request.method === 'GET' && request.url?.startsWith('/v3/tasks/')) {
        return envelope(response, { task_id: 'stub-smartsegment', type: 'smartsegment_model', status: 'running', progress: 10 });
      }
      response.writeHead(404);
      response.end();
    });
  });
  return new Promise((resolve) => server.listen(port, '127.0.0.1', () => resolve({ server, seen })));
}

async function largeChecks(engine, browser, [width, height], source, stub) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  await context.addCookies(source.cookies);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);
  await step('A: the panel lists Tripo Smart Segmentation for the ~80 MiB model', async () => {
    await openStudio(page, source.sessionID);
    await showTask(page, source.taskID);
    await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).check({ timeout: 20000 });
    const provider = await panel.locator('[data-testid=processing-provider-mesh-segmentation], [data-testid=processing-offering-mesh-segmentation]').first().innerText();
    assert(/Tripo/.test(provider), `segmentation service ${provider}`);
    assert.equal(await panel.locator('[data-testid=processing-refused-mesh-segmentation]').count(), 0, 'the model was refused');
    await page.screenshot({ path: path.join(OUT, `large-${engine}-${viewport}-A-panel.png`) });
    return provider;
  });
  await step('A: POST /tasks/prepare accepts the ~80 MiB model with the process temp dir unusable', async () => {
    const prepared = await api(context, 'POST', routes.task_prepare, { session_id: source.sessionID, capability: 'mesh-segmentation', offering_id: TRIPO_SMART,
      input: { source_asset: source.asset.id, authorize_extra_credits: true } });
    assert.equal(prepared.httpStatus, 201);
    if (engine !== ENGINES[0] || viewport !== `${VIEWPORTS[0][0]}x${VIEWPORTS[0][1]}`) return `prepared ${prepared.prepared_input_id}`;
    // Once: grant and submit, and the stub receives the stored bytes (the upload copy is on the storage volume).
    const accepted = await approveAndSubmit(context, prepared, `followups-large-${prepared.prepared_input_id}`);
    await waitFor(() => stub.seen.uploads.length > 0, 'the stub received no upload', 60000);
    const upload = stub.seen.uploads[0];
    assert.equal(upload.bytes, source.asset.size, `uploaded ${upload.bytes} of ${source.asset.size} bytes`);
    if (source.asset.sha256) assert.equal(upload.sha256, source.asset.sha256);
    await waitFor(() => stub.seen.creates > 0, 'no create after the upload', 30000);
    return `prepared 201; task ${accepted.task_id}; upload ${upload.bytes} bytes, create ${stub.seen.creates}`;
  });
  await step('no page errors and no outside requests', async () => {
    assert.equal(errors.length, 0, `page errors: ${errors.join(' | ')}`);
    assert.equal(outside.length, 0, `outside requests: ${outside.join(' | ')}`);
  });
  await context.close();
}

let stub;
let source;
if (PHASE === 'large') {
  const file = path.join(OUT, 'large-source.json');
  assert(existsSync(file), 'run PHASE=mock first with the same OUT');
  source = JSON.parse(readFileSync(file, 'utf8'));
  stub = await tripoStub(Number(process.env.STUB_PORT || 18422));
}
for (const engine of ENGINES) {
  const browser = await pw[engine].launch();
  try {
    for (const viewport of VIEWPORTS) {
      if (PHASE === 'large') await largeChecks(engine, browser, viewport, source, stub);
      else await mockChecks(engine, browser, viewport);
    }
  } finally {
    await browser.close();
  }
}
stub?.server.close();
writeFileSync(path.join(OUT, `report-${PHASE}.json`), `${JSON.stringify({ base: BASE, phase: PHASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
console.log(`report: ${path.join(OUT, `report-${PHASE}.json`)}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
