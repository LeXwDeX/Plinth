#!/usr/bin/env node
/**
 * Real-browser check for #321: when several compatible services can run a processing step and the
 * person named none, the Agent asks on an in-chat choice card instead of choosing for them.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid
 * calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18321 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18321 node scripts/test-chat-choice-card.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * OUT (screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH. Every request
 * outside BASE is refused and counted.
 *
 * Per engine and viewport, on a mock Hyper3D Rodin result (three segmentation services fit it:
 * Tripo Smart Segmentation, both Bang paths; two retexture services: Meshy, Hyper3D):
 *  1. "做模型分割" → a pending card: labelled radio group, the default preselected and marked, Schema
 *     quotes; the keyboard moves the choice; 确认 sends a structured answer and exactly the chosen
 *     Bang path runs; the card then shows its answer read-only, also after a reload and on another
 *     device (a second browser context of the same person); another person cannot read it.
 *  2. "做模型分割" again → 用默认 → the first compatible service (Tripo Smart Segmentation); it bills extra
 *     export credits, so under the session's manual approval its approval card follows, and approving runs it.
 *  3. "重贴图" → a card → the person types "用 Meshy 的" instead → the card is answered by the Agent
 *     and, under the session's manual approval, the approval card follows for Meshy Retexture.
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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18321');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'chat-choice-card-'));
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const fill = (template, values) => Object.entries(values).reduce((result, [key, value]) => result.replaceAll(`{${key}}`, value), template);
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const answerField = application.chat.api.fields.choice_answer;
const label = (id) => application.capabilities.find((capability) => capability.id === id).label_cn;
const SOURCE_OFFERING = 'hyper3d/rodin-gen-2.5/text-to-3d';
const TRIPO_SMART = 'tripo/geometry/smartsegment-model';
const BANG_LOCAL = 'hyper3d/bang/local-model';
const BANG_RODIN = 'hyper3d/bang/rodin-task';
const MESHY = 'meshy/processing/retexture';
const HYPER3D_TEXTURE = 'hyper3d/texture/local-model';

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
  const key = `choice-card-${prepared.prepared_input_id}`;
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

const choicesOf = async (context, sessionID) =>
  (await api(context, 'GET', routes.chat_session_choices.replace('{sessionID}', encodeURIComponent(sessionID)))).choices;

/** Tasks of the session created after the source, by offering. */
async function processingTasks(context, sessionID, sourceTaskID) {
  const listed = await api(context, 'GET', `${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=50`);
  const items = listed.tasks ?? listed.items ?? [];
  return items.filter((task) => task.id !== sourceTaskID).map((task) => ({ id: task.id, offering: task.offering_id ?? task.offering?.id, capability: task.capability, state: task.state }));
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

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  const user = `choice-card-${engine}-${viewport}-${Date.now()}`;
  await login(context, user);
  const source = await rodinSource(context, `#321 card ${engine} ${viewport}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const turns = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === legacy.chat_messages.replace('{sessionID}', source.sessionID)) turns.push(request.postDataJSON());
  });
  await page.goto(`${BASE}/?session=${encodeURIComponent(source.sessionID)}`);
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const box = page.locator('[data-testid=studio-composer] textarea');
  const say = async (words) => {
    await box.fill(words);
    await page.locator('[data-testid=composer-send]').click();
  };
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
  const openCard = () => page.locator('[data-testid=chat-choice-card][data-state=pending]');
  const segmentation = label('mesh-segmentation');

  await step('the Rodin result is on the stage', async () => {
    const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
    await tile.waitFor({ timeout: 20000 });
    await tile.click();
    await box.waitFor({ timeout: 10000 });
  });

  let firstCard = '';
  await step('做模型分割 opens a card: labelled radio group, default preselected, Schema quotes', async () => {
    await say('给当前模型做模型分割');
    await openCard().waitFor({ timeout: 30000 });
    await idle();
    const card = openCard();
    firstCard = await card.getAttribute('data-choice-id');
    assert.equal(await card.getAttribute('aria-label'), text('ui.chat.choice.label'));
    const question = fill(text('ui.chat.choice.offering_question'), { step: segmentation });
    const group = page.getByRole('radiogroup', { name: question });
    assert.equal(await group.count(), 1, 'radio group named by the question');
    const radios = group.getByRole('radio');
    assert.equal(await radios.count(), 3);
    const ids = await radios.evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-option-id')));
    assert.deepEqual(ids, [TRIPO_SMART, BANG_LOCAL, BANG_RODIN]);
    assert.equal(await radios.nth(0).getAttribute('aria-checked'), 'true');
    assert.equal(await radios.nth(0).getByText(text('ui.chat.choice.default_badge'), { exact: true }).count(), 1);
    const quotes = await card.locator('[data-testid=chat-choice-quote]').allInnerTexts();
    assert(quotes[0].startsWith(text('ui.workspace.harness.credits')), `Tripo quote ${quotes[0]}`);
    assert(quotes.slice(1).every((quote) => quote.startsWith(text('ui.pricing.default_estimate').split('{')[0])), `Bang quotes ${quotes.join(' | ')}`);
    assert.equal(await card.getByRole('button', { name: text('ui.chat.choice.confirm'), exact: true }).count(), 1);
    assert.equal(await card.locator('[data-testid=chat-choice-default]').count(), 1);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-1-card.png`) });
    return `options ${ids.join(', ')}; quotes ${quotes.join(' | ')}`;
  });

  await step('the keyboard moves the choice; 确认 sends the structured answer and the chosen Bang path runs', async () => {
    const card = openCard();
    const radios = card.getByRole('radio');
    await radios.nth(0).focus();
    await page.keyboard.press('End');
    assert.equal(await radios.nth(2).getAttribute('aria-checked'), 'true', 'End selects the last');
    await page.keyboard.press('ArrowDown');
    assert.equal(await radios.nth(0).getAttribute('aria-checked'), 'true', 'ArrowDown wraps');
    await page.keyboard.press('ArrowUp');
    assert.equal(await radios.nth(2).getAttribute('aria-checked'), 'true', 'ArrowUp wraps');
    assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-option-id')), BANG_RODIN, 'focus follows');
    const before = turns.length;
    await card.locator('[data-testid=chat-choice-confirm]').click();
    await waitFor(() => turns.length > before, 'no answer turn');
    const body = turns.at(-1);
    assert.deepEqual(body[answerField], { choice_id: firstCard, option_id: BANG_RODIN });
    const title = await radios.nth(2).locator('span.font-medium').first().innerText();
    assert.equal(body.content, fill(text('ui.chat.choice.answer_text'), { title: title.replace(text('ui.chat.choice.default_badge'), '').trim() }));
    await idle();
    const done = page.locator(`[data-testid=chat-choice-card][data-choice-id="${firstCard}"]`);
    await done.and(page.locator('[data-state=answered]')).waitFor({ timeout: 15000 });
    assert.equal(await done.locator('[data-testid=chat-choice-confirm]').count(), 0, 'answered card is read-only');
    const status = await done.locator('[data-testid=chat-choice-status]').innerText();
    assert(status.startsWith(text('ui.chat.choice.answered').split('{')[0]) && status.includes('Rodin'), `status ${status}`);
    const tasks = await waitFor(async () => {
      const listed = await processingTasks(context, source.sessionID, source.taskID);
      return listed.some((task) => task.offering === BANG_RODIN) ? listed : null;
    }, 'the chosen Bang path did not run');
    assert(!tasks.some((task) => task.offering === TRIPO_SMART || task.offering === BANG_LOCAL), `other services ran: ${JSON.stringify(tasks)}`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-2-answered.png`) });
    return `answer ${JSON.stringify(body[answerField])}; tasks ${tasks.map((task) => task.offering).join(', ')}; ${status}`;
  });

  await step('the answer stays after a reload and on another device; another person cannot read the cards', async () => {
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    const done = page.locator(`[data-testid=chat-choice-card][data-choice-id="${firstCard}"][data-state=answered]`);
    await done.waitFor({ timeout: 20000 });
    assert.equal(await done.getByRole('radio', { checked: true }).getAttribute('data-option-id'), BANG_RODIN);
    // Another device: the same person's sign-in, none of this browser's page state.
    const { cookies } = await context.storageState();
    const other = await guardedContext(browser, { width, height }, outside);
    await other.addCookies(cookies);
    const second = await other.newPage();
    await second.goto(`${BASE}/?session=${encodeURIComponent(source.sessionID)}`);
    await second.locator(`[data-testid=chat-choice-card][data-choice-id="${firstCard}"][data-state=answered]`).waitFor({ timeout: 30000 });
    await other.close();
    const stranger = await guardedContext(browser, { width, height }, outside);
    await login(stranger, `${user}-other`);
    const response = await stranger.request.get(`${BASE}${routes.chat_session_choices.replace('{sessionID}', encodeURIComponent(source.sessionID))}`);
    assert.equal(response.status(), 404, 'another person reads the cards');
    await stranger.close();
  });

  await step('用默认 picks the first compatible service; its approval card follows and approving runs it', async () => {
    await say('再做一次模型分割');
    const card = openCard();
    await card.waitFor({ timeout: 30000 });
    await idle();
    const id = await card.getAttribute('data-choice-id');
    assert.notEqual(id, firstCard);
    const button = card.locator('[data-testid=chat-choice-default]');
    assert((await button.innerText()).startsWith(text('ui.chat.choice.use_default').split('{')[0]));
    const before = turns.length;
    await button.click();
    await waitFor(() => turns.length > before, 'no default turn');
    assert.deepEqual(turns.at(-1)[answerField], { choice_id: id, use_default: true });
    await idle();
    const done = page.locator(`[data-testid=chat-choice-card][data-choice-id="${id}"][data-state=answered]`);
    await done.waitFor({ timeout: 15000 });
    const status = await done.locator('[data-testid=chat-choice-status]').innerText();
    assert(status.startsWith(text('ui.chat.choice.answered_default').split('{')[0]), `status ${status}`);
    // Tripo Smart Segmentation bills extra export credits: under manual approval the person approves it after choosing.
    const approval = page.locator('[data-testid=studio-approval-card]');
    await approval.waitFor({ timeout: 15000 });
    const model = await approval.locator('[data-testid=studio-approval-model]').innerText();
    assert(/Tripo/.test(model), `approval names ${model}`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-3-default-then-approval.png`) });
    await approval.locator('[data-testid=studio-approval-approve]').click();
    await waitFor(async () => (await processingTasks(context, source.sessionID, source.taskID)).some((task) => task.offering === TRIPO_SMART), 'the default service did not run');
    await approval.waitFor({ state: 'detached', timeout: 15000 });
    return `${status}; approval ${model}`;
  });

  await step('a typed answer works, and the approval card follows for the chosen retexture service', async () => {
    await say('给当前模型重贴图，青苔石头质感');
    const card = openCard();
    await card.waitFor({ timeout: 30000 });
    await idle();
    const id = await card.getAttribute('data-choice-id');
    const ids = await card.getByRole('radio').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-option-id')));
    assert.deepEqual(ids, [MESHY, HYPER3D_TEXTURE]);
    assert.equal(await page.locator('[data-testid=studio-approval-card]').count(), 0, 'no approval before the choice');
    // Hyper3D Texture needs a reference picture; a text-only retexture names Meshy in words.
    await say('用 Meshy 的');
    const approval = page.locator('[data-testid=studio-approval-card]');
    await approval.waitFor({ timeout: 30000 });
    await idle();
    assert(/Meshy/.test(await approval.locator('[data-testid=studio-approval-model]').innerText()), 'approval names Meshy');
    const choices = await choicesOf(context, source.sessionID);
    const answered = choices.find((choice) => choice.id === id);
    assert.equal(answered.state, 'answered');
    assert.equal(answered.answer.option_id, MESHY);
    assert.equal(answered.answer.use_default, false);
    assert.equal(answered.answer.source, 'agent');
    assert.equal(turns.at(-1)[answerField], undefined, 'a typed reply carries no card answer');
    const done = page.locator(`[data-testid=chat-choice-card][data-choice-id="${id}"][data-state=answered]`);
    await done.waitFor({ timeout: 15000 });
    assert.equal(await page.locator('[data-testid=chat-choice-card] [data-testid=studio-approval-approve]').count(), 0, 'the two cards stay apart');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-4-typed-then-approval.png`) });
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
console.log(`report: ${path.join(OUT, 'report.json')}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
