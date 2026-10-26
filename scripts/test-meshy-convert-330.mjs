#!/usr/bin/env node
/**
 * Real-browser checks for #330 (补齐格式 and 格式转换 through Meshy Convert). No credentials and no paid
 * upstream: a local server with mock providers and the mock Agent, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18330 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18330 OUT=<scratch>/out node scripts/test-meshy-convert-330.mjs
 * Per engine and viewport (a fresh person each time):
 *  A. 加工 → 格式转换 on a mock Rodin result lists Meshy Convert last among its services; the default
 *     service is not Meshy Convert; choosing it shows 1 credit and the saved-file route. Its six target
 *     formats are fixed by the Schema, so the step shows no field for them (the OBJ note lives in the
 *     Schema description the Agent reads and in the download archive's README).
 *  B. 加工 → 格式转换 on a mock Meshy 7.1 result: choosing Meshy Convert takes the original task; 确认
 *     posts a plan whose step is exactly Meshy Convert, and its Task succeeds with the six model files.
 *  C. 格式 on the Rodin result: the FBX row reads 可转换 · Meshy AI at 1 credit; 补齐格式 starts the
 *     Meshy Convert plan; FBX/OBJ/USDZ/STL/3MF become 已有 and the FBX file downloads.
 *     The Rodin source carries [mock:texture_padding]: its GLB texture has one alignment space after IEND, as
 *     the dev-test keychain had, and Convert copies it into its USDZ (#330 B); glTF and every Convert format
 *     still become 已有.
 *  D. In chat, 重拓扑 on a mock Tripo result opens the choice card for the remesh services only: Meshy
 *     Convert (a format conversion) is not among them.
 *  E. (#330 C) With the English locale the FBX row reads "About 1 credit" and 补齐格式 "(about 1 credit)".
 * Then, for one shared person whose Rodin source carries [mock:convert_rejected] (Meshy Convert finishes
 * upstream, i.e. charged, and its files then fail the delivery check; set up once, ~4 min):
 *  F. Every engine and viewport: the failed rows say 1 credit was charged and offer 免费取回 (free) first;
 *     the failure block names the charge and leads with the free refetch.
 *  G. Once (first engine/viewport): clicking 免费取回 posts only the task refetch of the failed Convert task
 *     (no plan retry, no new plan); the refetch fails the same way (~4 min).
 *  H. Every engine and viewport: the rows now offer 重试（再次计费） with the charge note, and the failure
 *     block's retry says it charges again. Nothing is retried.
 * #330 second follow-ups (dev-test note 99645):
 *  I. Every engine and viewport, Chinese and English: the local glTF packaging and the Meshy Convert both failed
 *     (a route fixture of the formats response: on the mock stack the local converter is the real one and
 *     cannot be made to fail). Each failure is its own group naming its formats and provider; only the
 *     charged one names the charge; each button names its formats.
 *  J. For one shared person whose Rodin source carries [mock:convert_rejected_once] (the Convert fails delivery,
 *     its free refetch gets clean files): before the refetch the README names the failed formats as
 *     conversion_failed, not not_converted. Once, the free refetch is clicked and succeeds. Then every engine and
 *     viewport: every format is 已有, the failure block is gone, the refetch reads eligible:false reason
 *     delivered, the version strip shows the source as the only 3D version (no failed tiles, no refetch
 *     version), the stage still previews the source's own GLB, and each format downloads with its media type
 *     (IANA model/* types, FBX application/octet-stream), nosniff and as an attachment.
 * #330 third follow-ups:
 *  K. Every engine and viewport, Chinese and English: a ZIP request answered 413 shows the Schema's translated
 *     too-large reason (download each format), not the generic download-failed text.
 * Direct conversion (#330 third follow-ups):
 *  D. A chat request converts a model through convert_model_format without a Processing Plan. The direct Task
 *     delivers its own files; every engine/viewport, Chinese and English: the format row is 已有, offers no retry/
 *     refetch/paid completion for it, the version strip keeps one source version, and no page error or outside
 *     request appears. J also checks the failed source Task is now a real SUCCEEDED Task after the refetch.
 * The local /api/ limiter counts all contexts by client address. Combinations are separated by the Schema's
 * full rate window, so the complete D/J/K matrix stays under it. A 429 fails a step immediately with its method
 * and path; it is never retried.
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"), PARTS=J or PARTS=D
 * (only that part; K belongs to J),
 * PLAYWRIGHT_MODULE_PATH. Every browser request outside BASE is refused and counted.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { execFileSync } from 'node:child_process';
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18330');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'meshy-330-'));
mkdirSync(OUT, { recursive: true });

const { application, offerings: catalogOfferings } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const fill = (template, values) => template.replace(/\{(\w+)\}/g, (_, name) => String(values[name]));
const routes = application.runtime.http.routes;
// The Product Schema's /api/ limiter is keyed by client address, so all browser
// contexts in this local run share one 1200-request window. One page combination
// already issues several hundred requests; leaving a full window between
// combinations keeps the complete matrix under the same limit without changing
// the product rule or retrying a 429.
const API_RATE_WINDOW_MS = application.runtime.http.api_rate_limit?.requests_per_minute > 0 ? 60000 : 0;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const legacy = application.runtime.http.legacy_routes;
const label = (id) => application.capabilities.find((capability) => capability.id === id).label_cn;
const offering = (id) => {
  const found = catalogOfferings.find((item) => item.id === id);
  assert(found, `offering ${id} is not in the Catalog`);
  return found;
};
const CONVERT = offering('meshy/processing/convert');
const CONVERT_NAME = `${CONVERT.provider_name} · ${CONVERT.model_name}`;
const RODIN = 'hyper3d/rodin-gen-2.5/text-to-3d';
const MESHY = 'meshy/meshy-7.1/text-to-3d';
const TRIPO = 'tripo/tripo-v3.1-full/text-to-3d';
const REMESH_SERVICES = ['meshy/processing/remesh', 'tripo/geometry/decimate-triangle'];
const completeRoute = (taskID) => routes.model_formats_complete.replace('{taskID}', encodeURIComponent(taskID));
const refetchRoute = (taskID) => routes.task_refetch.replace('{taskID}', encodeURIComponent(taskID));
const english = application.localization.bundles.en.messages;
const formatsRoute = (taskID) => routes.model_formats.replace('{taskID}', encodeURIComponent(taskID));
const archiveRoute = (taskID) => routes.model_archive.replace('{taskID}', encodeURIComponent(taskID));
const pendingApprovalsRoute = (sessionID) => routes.chat_session_pending_approvals.replace('{sessionID}', encodeURIComponent(sessionID));
const formatLabel = (id) => application.model_delivery.formats.find((format) => format.id === id).label;
const formatList = (ids, messages = application.content.messages) => ids.map(formatLabel).join(messages['ui.studio.list_separator'] ?? text('ui.studio.list_separator'));
const localeCookie = application.localization.preference.cookie;

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()}${response.status() === 429 ? ' rate limited' : ''} ${JSON.stringify(body).slice(0, 300)}`);
  return body;
}

async function login(context, user) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
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

/** A finished mock 3D result in a fresh session, through the public prepare → grant → submit path. */
async function source(context, title, offeringID, marker = '') {
  const session = await api(context, 'POST', legacy.chat_sessions, { title });
  const prompt = `A simple low-poly wooden stool${marker ? ` ${marker}` : ''}`;
  const input = offeringID === MESHY ? { prompt } : { prompt, authorize_extra_credits: true };
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: offeringID, input });
  const key = `meshy330-${prepared.prepared_input_id}`;
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  await waitTask(context, accepted.task_id);
  return { sessionID: session.id, taskID: accepted.task_id };
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

async function pauseBetweenCombinations(label) {
  if (API_RATE_WINDOW_MS <= 0) return;
  console.log(`[throttle] ${label}: waiting ${API_RATE_WINDOW_MS}ms for the ${application.runtime.http.api_rate_limit.requests_per_minute}/min /api window`);
  await sleep(API_RATE_WINDOW_MS);
}

/** The option labels of a step's service selector, read from its open list. */
async function openOptions(page, step) {
  await step.locator('[data-slot=select-trigger], button').first().click();
  const list = page.getByRole('option');
  await list.first().waitFor({ timeout: 5000 });
  return (await list.allInnerTexts()).map((item) => item.replace(/\s+/g, ' ').trim());
}

const rateWatches = new WeakMap();
function watchRateLimit(page) {
  let watch = rateWatches.get(page);
  if (watch) return watch;
  const seen = [];
  let reject;
  const failure = new Promise((_, fail) => { reject = fail; });
  failure.catch(() => {});
  page.on('response', (response) => {
    if (response.status() !== 429) return;
    const target = new URL(response.url());
    const item = `${response.request().method()} ${target.pathname}`;
    seen.push(item);
    reject(new Error(`HTTP 429 rate limited: ${item}`));
  });
  watch = { seen, failure };
  rateWatches.set(page, watch);
  return watch;
}
function assertNoRateLimit(page) {
  const watch = watchRateLimit(page);
  assert.equal(watch.seen.length, 0, `HTTP 429 rate limited: ${watch.seen.slice(-3).join(', ')}`);
}
function withRateLimit(page, promise) {
  return Promise.race([promise, watchRateLimit(page).failure]);
}

async function openStage(page, sessionID, tab) {
  watchRateLimit(page);
  assertNoRateLimit(page);
  await withRateLimit(page, page.goto(`${BASE}/?session=${encodeURIComponent(sessionID)}`));
  await withRateLimit(page, page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 }));
  const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
  await withRateLimit(page, tile.waitFor({ timeout: 20000 }));
  await withRateLimit(page, tile.click());
  if (tab) await withRateLimit(page, page.locator(`[data-testid=right-panel-tab-${tab}]`).click({ timeout: 20000 }));
  assertNoRateLimit(page);
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block', acceptDownloads: true });
  const outside = [];
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  await login(context, `meshy330-${engine}-${viewport}-${Date.now()}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const planPosts = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === routes.processing_plans) planPosts.push(request.postDataJSON());
  });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  const panel = page.locator('[data-testid=model-processing-panel]');
  const conversion = label('format-conversion');
  const rodin = await source(context, `#330 rodin ${engine} ${viewport}`, RODIN, '[mock:texture_padding]');

  await step('A: 格式转换 lists Meshy Convert last and keeps the default; choosing it quotes 1 credit', async () => {
    await openStage(page, rodin.sessionID, 'process');
    await panel.getByRole('checkbox', { name: conversion }).check({ timeout: 20000 });
    const choice = panel.locator('[data-testid=processing-offering-format-conversion]');
    await choice.waitFor({ timeout: 5000 });
    const before = (await choice.innerText()).replace(/\s+/g, ' ');
    const options = await openOptions(page, choice);
    assert(options.length >= 2 && options.at(-1).startsWith(CONVERT_NAME), `options: ${options.join(' | ')}`);
    assert(!before.includes(CONVERT.model_name), `the default became Meshy Convert: ${before}`);
    await page.getByRole('option').filter({ hasText: CONVERT.model_name }).click();
    await page.getByRole('option').first().waitFor({ state: 'detached', timeout: 5000 });
    const quote = (await panel.locator('[data-testid=processing-quote-format-conversion]').innerText()).trim();
    assert(quote.startsWith(text('ui.workspace.harness.credits')) && /\b1\b/.test(quote), `quote ${quote}`);
    const route = (await panel.locator('[data-testid=processing-route-format-conversion]').innerText()).trim();
    assert(route.includes(text('ui.workspace.processing.asset_route')), `route ${route}`);
    // The fixed target formats are not a field the person edits.
    const section = panel.locator('[data-testid=processing-offering-format-conversion]').locator('xpath=..');
    assert.equal(await section.locator('[name=target_formats], [id$=target_formats]').count(), 0, 'target_formats is editable');
    await shot('A-panel-convert');
    await panel.getByRole('checkbox', { name: conversion }).uncheck();
    return `${options.join(' | ')}; default ${before}; ${quote}`;
  });

  await step('B: on a Meshy result, 确认 runs exactly Meshy Convert by the original task', async () => {
    const meshy = await source(context, `#330 meshy ${engine} ${viewport}`, MESHY);
    planPosts.length = 0;
    await openStage(page, meshy.sessionID, 'process');
    await panel.getByRole('checkbox', { name: conversion }).check({ timeout: 20000 });
    const choice = panel.locator('[data-testid=processing-offering-format-conversion]');
    await choice.waitFor({ timeout: 5000 });
    await openOptions(page, choice);
    await page.getByRole('option').filter({ hasText: CONVERT.model_name }).click();
    await page.getByRole('option').first().waitFor({ state: 'detached', timeout: 5000 });
    const route = (await panel.locator('[data-testid=processing-route-format-conversion]').innerText()).trim();
    assert(route.includes(text('ui.workspace.processing.task_route')), `route ${route}`);
    await panel.getByRole('button', { name: text('ui.workspace.processing.plan_confirm') }).click();
    const posted = await (async () => {
      const deadline = Date.now() + 15000;
      while (planPosts.length === 0 && Date.now() < deadline) await page.waitForTimeout(200);
      return planPosts[0];
    })();
    assert(posted, 'no plan POST');
    assert.equal(posted.steps.length, 1);
    assert.equal(posted.steps[0].offering_id, CONVERT.id);
    assert.equal(posted.steps[0].expected_offering_revision, CONVERT.revision);
    let plan;
    let task;
    const until = Date.now() + 90000;
    while (Date.now() < until) {
      const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(meshy.sessionID)}`);
      plan = listed.plans?.find((candidate) => candidate.source_task_id === meshy.taskID);
      assert(plan?.state !== 'FAILED', `plan failed: ${plan?.failure}`);
      if (plan?.steps?.[0]?.task_id) {
        task = await api(context, 'GET', routes.task_item.replace('{taskID}', plan.steps[0].task_id));
        if (task.state === 'SUCCEEDED') break;
        assert(!['FAILED', 'CANCELED'].includes(task.state), `convert task ${task.state}`);
      } else if (plan?.state === 'PAUSED') {
        const action = panel.getByRole('button', { name: new RegExp(`^(${[text('ui.workspace.processing.plan_prepare_step'), text('ui.workspace.processing.plan_confirm_step'), text('ui.workspace.processing.plan_resume')].join('|')})`) });
        if (await action.count() && await action.first().isEnabled()) await action.first().click();
      }
      await page.waitForTimeout(800);
    }
    assert.equal(task?.state, 'SUCCEEDED', 'the Convert task did not finish');
    assert.equal(task.offering_id ?? task.offering?.id, CONVERT.id);
    assert.equal(plan.steps[0].estimated_credits, 1, `plan credits ${plan.steps[0].estimated_credits}`);
    const variants = (task.assets ?? []).filter((asset) => asset.role === 'model').map((asset) => asset.variant).sort();
    assert.deepEqual(variants, ['3mf', 'fbx', 'glb', 'obj', 'stl', 'usdz']);
    await shot('B-plan-done');
    await panel.getByRole('checkbox', { name: conversion }).uncheck().catch(() => {});
    return `plan ${plan.id} → task ${task.id} ${variants.join(',')}`;
  });

  await step('E: the English format list says About 1 credit (#330 C)', async () => {
    await context.addCookies([{ name: localeCookie, value: 'en', url: BASE }]);
    try {
      await openStage(page, rodin.sessionID, 'formats');
      const fbx = page.locator('[data-testid=model-format-fbx][data-status=obtainable]');
      await fbx.waitFor({ timeout: 20000 });
      const row = (await fbx.innerText()).replace(/\s+/g, ' ');
      const singular = fill(english['ui.workspace.model_formats.credits_one'], { credits: 1 });
      assert(row.includes(singular) && !row.includes('1 credits'), `fbx row ${row}`);
      const complete = (await page.locator('[data-testid=model-formats-complete]').innerText()).trim();
      assert.equal(complete, fill(english['ui.workspace.model_formats.complete_one'], { credits: 1 }));
      await shot('E-english-singular');
      return `${row} | ${complete}`;
    } finally {
      await context.addCookies([{ name: localeCookie, value: application.localization.default_locale, url: BASE }]);
    }
  });

  await step('C: 格式 offers Meshy Convert at 1 credit and 补齐格式 fills the formats', async () => {
    await openStage(page, rodin.sessionID, 'formats');
    await page.locator('[data-testid=model-formats][data-layout=pinned]').waitFor({ timeout: 20000 });
    const fbx = page.locator('[data-testid=model-format-fbx][data-status=obtainable]');
    await fbx.waitFor({ timeout: 20000 });
    const row = (await fbx.innerText()).replace(/\s+/g, ' ');
    assert(row.includes(`${text('ui.workspace.model_formats.status.obtainable')} · ${CONVERT.provider_name}`), `fbx row ${row}`);
    assert(row.includes(fill(text('ui.workspace.model_formats.credits'), { credits: 1 })), `fbx estimate ${row}`);
    await shot('C-formats-obtainable');
    const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === completeRoute(rodin.taskID) && candidate.request().method() === 'POST');
    await page.locator('[data-testid=model-formats-complete]').click();
    assert.equal((await response).status(), 202);
    // The padded texture (#330 B) no longer fails the local glTF package or the Convert USDZ.
    for (const format of ['gltf', 'fbx', 'obj', 'usdz', 'stl', '3mf']) {
      await page.locator(`[data-testid=model-format-${format}][data-status=available]`).waitFor({ timeout: 90000 });
    }
    const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(rodin.sessionID)}`);
    const completion = listed.plans?.filter((plan) => plan.source_task_id === rodin.taskID && plan.steps?.[0]?.offering_id === CONVERT.id) ?? [];
    assert.equal(completion.length, 1, `completion plans ${JSON.stringify(listed.plans?.map((plan) => plan.steps?.[0]?.offering_id))}`);
    assert(!listed.plans?.some((plan) => plan.steps?.some((item) => item.offering_id === 'meshy/processing/remesh')), 'Remesh still runs format completion');
    const link = page.locator('[data-testid=model-format-fbx] a[download]');
    await link.waitFor({ timeout: 5000 });
    const bytes = await context.request.get(new URL(await link.getAttribute('href'), BASE).toString());
    assert.equal(bytes.status(), 200, 'fbx download');
    assert((await bytes.body()).length > 0, 'empty fbx');
    await shot('C-formats-complete');
    return `plan ${completion[0].id}`;
  });

  await step('D: the chat remesh choice card lists remesh services only', async () => {
    const tripo = await source(context, `#330 tripo ${engine} ${viewport}`, TRIPO);
    await openStage(page, tripo.sessionID);
    const box = page.locator('[data-testid=studio-composer] textarea');
    await box.fill('给当前模型重拓扑');
    await page.locator('[data-testid=composer-send]').click();
    const card = page.locator('[data-testid=chat-choice-card][data-state=pending]');
    await card.waitFor({ timeout: 30000 });
    const ids = await card.getByRole('radio').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-option-id')));
    assert(!ids.includes(CONVERT.id), `Meshy Convert offered for remesh: ${ids.join(', ')}`);
    assert.deepEqual([...ids].sort(), [...REMESH_SERVICES].sort(), `options ${ids.join(', ')}`);
    await shot('D-remesh-card');
    return ids.join(', ');
  });

  await step('no page errors and no outside requests', async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

/**
 * One person, one Rodin source whose Meshy Convert finishes upstream and then fails delivery. Started through
 * the completion route, as 补齐格式 does; waits until the Convert plan has failed.
 */
async function failedDelivery(browser) {
  const context = await browser.newContext();
  const user = `meshy330-failed-${Date.now()}`;
  await login(context, user);
  const rodin = await source(context, '#330 charged but undelivered', RODIN, '[mock:convert_rejected]');
  await api(context, 'POST', completeRoute(rodin.taskID));
  const deadline = Date.now() + 10 * 60000;
  for (;;) {
    const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(rodin.sessionID)}`);
    const plan = listed.plans?.find((candidate) => candidate.source_task_id === rodin.taskID && candidate.steps?.[0]?.offering_id === CONVERT.id);
    if (plan?.state === 'FAILED') {
      const failedTask = plan.steps[0].task_id;
      const eligibility = await api(context, 'GET', refetchRoute(failedTask));
      assert(eligibility.eligible && eligibility.charged_credits === 1, `eligibility ${JSON.stringify(eligibility)}`);
      // Mock sign-in mints a new person on every login, so later contexts reuse this sign-in instead.
      const state = await context.storageState();
      await context.close();
      return { user, state, ...rodin, planID: plan.id, failedTask };
    }
    assert(Date.now() < deadline, `the Convert plan did not fail: ${plan?.state}`);
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }
}

/** F/G/H on the shared failed delivery. `phase` is 'free' (before the refetch) or 'paid' (after it failed). */
async function failedDeliveryChecks(engine, browser, [width, height], shared, phase, trigger) {
  const viewport = `${width}x${height}`;
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block', storageState: shared.state });
  const outside = [];
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const posts = [];
  page.on('request', (request) => { if (request.method() === 'POST') posts.push(new URL(request.url()).pathname); });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  const charged = (key) => fill(text(`${key}_one`), { credits: 1 });
  const rowOf = async () => {
    await openStage(page, shared.sessionID, 'formats');
    const fbx = page.locator('[data-testid=model-format-fbx][data-status=obtainable]');
    await fbx.waitFor({ timeout: 20000 });
    return fbx;
  };
  if (phase === 'free') {
    await step('F: a charged but undelivered Convert offers the free refetch first', async () => {
      const fbx = await rowOf();
      const action = fbx.locator('[data-testid=model-format-action-fbx]');
      assert.equal(await action.getAttribute('data-action'), 'refetch');
      assert.equal((await action.innerText()).trim(), text('ui.workspace.model_formats.refetch'));
      const row = (await fbx.innerText()).replace(/\s+/g, ' ');
      assert(row.includes(text('ui.workspace.model_formats.free')), `price ${row}`);
      const hint = (await fbx.locator('[data-testid=model-format-hint-fbx]').innerText()).trim();
      assert.equal(hint, charged('ui.workspace.model_formats.refetch_hint'));
      const block = page.locator('[data-testid=model-formats-failed]');
      assert((await block.locator('[data-testid=model-formats-charged]').first().innerText()).trim() === charged('ui.workspace.model_formats.charged_note'));
      const group = block.locator('[data-testid=model-formats-failed-step]');
      assert.equal(await group.count(), 1, 'one failed conversion');
      const formats = (await group.getAttribute('data-formats')).split(' ');
      const lead = group.locator('button[data-action=refetch]');
      assert.equal((await lead.innerText()).trim(), fill(text('ui.workspace.model_formats.refetch_named'), { format: formatList(formats) }));
      assert.equal((await group.locator('[data-testid=model-formats-failed-title]').innerText()).trim(),
        fill(text('ui.workspace.model_formats.failed_step'), { formats: formatList(formats), provider: CONVERT.provider_name }));
      await shot('F-free-refetch');
      return hint;
    });
    if (trigger) {
      await step('G: 免费取回 posts only the task refetch and nothing is charged again', async () => {
        const fbx = await rowOf();
        // Only what the click sends counts; opening the page posts its own canvas project.
        posts.length = 0;
        const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === refetchRoute(shared.failedTask) && candidate.request().method() === 'POST');
        await fbx.locator('[data-testid=model-format-action-fbx]').click();
        assert.equal((await response).status(), 201);
        assert.deepEqual(posts.filter((item) => item !== refetchRoute(shared.failedTask)), [], `other POSTs ${posts.join(', ')}`);
        await page.locator('[data-testid=model-format-fbx][data-status=converting]').waitFor({ timeout: 20000 });
        await shot('G-refetching');
        const deadline = Date.now() + 10 * 60000;
        let eligibility;
        for (;;) {
          eligibility = await api(context, 'GET', refetchRoute(shared.failedTask));
          if ((eligibility.failed_refetches ?? 0) >= 1 && !eligibility.refetch_task_id) break;
          assert(Date.now() < deadline, `refetch did not end: ${JSON.stringify(eligibility)}`);
          await new Promise((resolve) => setTimeout(resolve, 3000));
        }
        const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(shared.sessionID)}`);
        const plan = listed.plans.find((candidate) => candidate.id === shared.planID);
        assert.equal(plan.steps[0].task_id, shared.failedTask, 'the failed plan was retried');
        assert.equal(listed.plans.filter((candidate) => candidate.steps?.[0]?.offering_id === CONVERT.id).length, 1, 'a second Convert plan was created');
        return `refetch failed ${eligibility.failed_refetches}x; plan untouched`;
      });
    }
  } else {
    await step('H: after the free refetch failed, the retry says it charges again', async () => {
      const fbx = await rowOf();
      const action = fbx.locator('[data-testid=model-format-action-fbx]');
      assert.equal(await action.getAttribute('data-action'), 'retry');
      assert.equal((await action.innerText()).trim(), text('ui.workspace.model_formats.retry_charged'));
      const hint = (await fbx.locator('[data-testid=model-format-hint-fbx]').innerText()).trim();
      assert.equal(hint, charged('ui.workspace.model_formats.retry_charged_hint'));
      const price = (await fbx.locator('[data-testid=model-format-price-fbx]').innerText()).trim();
      assert.equal(price, charged('ui.workspace.model_formats.credits'));
      const block = page.locator('[data-testid=model-formats-failed]');
      const formats = (await block.locator('[data-testid=model-formats-failed-step]').getAttribute('data-formats')).split(' ');
      assert.equal((await block.locator('button[data-action=retry]').innerText()).trim(), fill(text('ui.workspace.model_formats.retry_charged_named'), { format: formatList(formats) }));
      assert.equal(await block.locator('button[data-action=refetch]').count(), 0);
      await shot('H-paid-retry');
      return hint;
    });
  }
  await step(`no page errors and no outside requests (${phase})`, async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

/** The README of a task's download archive, read with the system unzip. */
async function readme(context, taskID, name) {
  const response = await context.request.get(`${BASE}${archiveRoute(taskID)}`, { headers: { Origin: BASE } });
  assert.equal(response.status(), 200, 'archive');
  const file = path.join(OUT, `${name}.zip`);
  writeFileSync(file, await response.body());
  return execFileSync('unzip', ['-p', file, application.model_delivery.archive.readme.filename], { encoding: 'utf8' });
}

/** Opens a context of the shared person, refusing every request outside BASE. */
async function sharedContext(browser, [width, height], shared) {
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block', storageState: shared.state });
  const outside = [];
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  return { context, page, outside, errors };
}

const LOCAL_GLTF = offering('local/generated-glb/to-gltf');

/**
 * I: the formats response of the shared failed delivery with the local glTF packaging failed as well. The local
 * converter is the real one on the mock stack, so this one state is a route fixture over the live response.
 */
async function mixedFailureChecks(engine, browser, [width, height], shared) {
  const viewport = `${width}x${height}`;
  const { context, page, outside, errors } = await sharedContext(browser, [width, height], shared);
  await context.route(`${BASE}${formatsRoute(shared.taskID)}`, async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const live = await route.fetch();
    const body = await live.json();
    const local = { provider_id: LOCAL_GLTF.provider_id, provider_name: LOCAL_GLTF.provider_name, provider_short_label: LOCAL_GLTF.provider_short_label, estimated_credits: 0 };
    body.formats = body.formats.map((row) => row.id === 'gltf'
      ? { id: row.id, label: row.label, status: 'obtainable', conversion: local, reason: 'conversion_failed' }
      : row);
    body.steps = [{
      offering_id: LOCAL_GLTF.id, offering_revision: LOCAL_GLTF.revision, ...local,
      formats: ['gltf'], state: 'FAILED', plan_id: 'fixture-gltf', plan_version: 2, failure: 'plan_task_failed', result_task_id: 'fixture-gltf-task',
    }, ...body.steps];
    return route.fulfill({ response: live, json: body });
  });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  for (const [locale, messages] of [[application.localization.default_locale, application.content.messages], ['en', { ...application.content.messages, ...english }]]) {
    await step(`I: mixed failure, each conversion its own group (${locale})`, async () => {
      await context.addCookies([{ name: localeCookie, value: locale, url: BASE }]);
      await openStage(page, shared.sessionID, 'formats');
      const groups = page.locator('[data-testid=model-formats-failed] [data-testid=model-formats-failed-step]');
      await groups.first().waitFor({ timeout: 20000 });
      assert.equal(await groups.count(), 2, 'two failed conversions');
      const gltf = groups.nth(0);
      const convert = groups.nth(1);
      assert.equal(await gltf.getAttribute('data-formats'), 'gltf');
      const meshyFormats = (await convert.getAttribute('data-formats')).split(' ');
      const title = (group) => group.locator('[data-testid=model-formats-failed-title]').innerText();
      assert.equal((await title(gltf)).trim(), fill(messages['ui.workspace.model_formats.failed_step'], { formats: 'glTF', provider: LOCAL_GLTF.provider_short_label }));
      assert.equal((await title(convert)).trim(), fill(messages['ui.workspace.model_formats.failed_step'], { formats: formatList(meshyFormats, messages), provider: CONVERT.provider_name }));
      assert.equal(await gltf.locator('[data-testid=model-formats-charged]').count(), 0, 'the free glTF group names a charge');
      assert.equal(await convert.locator('[data-testid=model-formats-charged]').count(), 1);
      assert.equal((await gltf.locator('button[data-action=retry]').innerText()).trim(), fill(messages['ui.workspace.model_formats.retry_named'], { format: 'glTF' }));
      const lead = convert.locator('button');
      const action = await lead.getAttribute('data-action');
      const key = action === 'refetch' ? 'refetch_named' : 'retry_charged_named';
      assert.equal((await lead.innerText()).trim(), fill(messages[`ui.workspace.model_formats.${key}`], { format: formatList(meshyFormats, messages) }));
      // Each row hint quotes its button as it reads.
      const fbxAction = (await page.locator('[data-testid=model-format-action-fbx]').innerText()).trim();
      const fbxHint = (await page.locator('[data-testid=model-format-hint-fbx]').innerText()).trim();
      assert(fbxHint.includes(fbxAction), `hint "${fbxHint}" does not name "${fbxAction}"`);
      await shot(`I-mixed-${locale}`);
      return `${await title(gltf)} | ${await title(convert)}`;
    });
  }
  await context.addCookies([{ name: localeCookie, value: application.localization.default_locale, url: BASE }]);
  await step('no page errors and no outside requests (I)', async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

/** J: one person whose Rodin source's Convert fails delivery once; its free refetch gets clean files. */
async function refetchedDelivery(browser) {
  const context = await browser.newContext();
  const user = `meshy330-refetched-${Date.now()}`;
  await login(context, user);
  const rodin = await source(context, '#330 refetched', RODIN, '[mock:convert_rejected_once]');
  await api(context, 'POST', completeRoute(rodin.taskID));
  const deadline = Date.now() + 10 * 60000;
  for (;;) {
    const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(rodin.sessionID)}`);
    const plan = listed.plans?.find((candidate) => candidate.source_task_id === rodin.taskID && candidate.steps?.[0]?.offering_id === CONVERT.id);
    if (plan?.state === 'FAILED') {
      const text330 = await readme(context, rodin.taskID, 'J-before-refetch');
      const reasons = application.model_delivery.reasons;
      assert(text330.includes(`- FBX：${reasons.conversion_failed}`), `README before the refetch:\n${text330}`);
      assert(!text330.includes(reasons.not_converted), `README calls a failed conversion not converted:\n${text330}`);
      const state = await context.storageState();
      await context.close();
      return { user, state, ...rodin, planID: plan.id, failedTask: plan.steps[0].task_id };
    }
    assert(Date.now() < deadline, `the Convert plan did not fail: ${plan?.state}`);
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }
}

async function refetchedChecks(engine, browser, [width, height], shared, trigger) {
  const viewport = `${width}x${height}`;
  const { context, page, outside, errors } = await sharedContext(browser, [width, height], shared);
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  if (trigger) {
    await step('J: the free refetch succeeds and delivers every format', async () => {
      await openStage(page, shared.sessionID, 'formats');
      const lead = page.locator('[data-testid=model-formats-failed] button[data-action=refetch]');
      await lead.waitFor({ timeout: 20000 });
      const response = page.waitForResponse((candidate) => new URL(candidate.url()).pathname === refetchRoute(shared.failedTask) && candidate.request().method() === 'POST');
      await lead.click();
      assert.equal((await response).status(), 201);
      const deadline = Date.now() + 10 * 60000;
      let eligibility;
      for (;;) {
        eligibility = await api(context, 'GET', refetchRoute(shared.failedTask));
        if (eligibility.reason === 'delivered') break;
        assert(Date.now() < deadline, `refetch did not deliver: ${JSON.stringify(eligibility)}`);
        await new Promise((resolve) => setTimeout(resolve, 3000));
      }
      return `refetch ${eligibility.refetch_task_id}`;
    });
  }
  await step('J: after the refetch: all formats, no failure block, delivered, one 3D version on the source GLB', async () => {
    const eligibility = await api(context, 'GET', refetchRoute(shared.failedTask));
    assert.equal(eligibility.eligible, false, `eligibility ${JSON.stringify(eligibility)}`);
    assert.equal(eligibility.reason, 'delivered');
    // The original failed conversion is a real Task with its own assets now.
    const recovered = await api(context, 'GET', routes.task_item.replace('{taskID}', shared.failedTask));
    assert.equal(recovered.state, 'SUCCEEDED', `the failed source Task is ${recovered.state}`);
    assert(recovered.assets.length > 0, 'the completed source Task has no assets');
    let note = '';
    for (const [locale, messages] of [[application.localization.default_locale, application.content.messages], ['en', { ...application.content.messages, ...english }]]) {
      await context.addCookies([{ name: localeCookie, value: locale, url: BASE }]);
      await openStage(page, shared.sessionID, 'formats');
      for (const format of ['glb', 'gltf', 'fbx', 'obj', 'usdz', 'stl', '3mf']) {
        await page.locator(`[data-testid=model-format-${format}][data-status=available]`).waitFor({ timeout: 30000 });
      }
      assert.equal(await page.locator('[data-testid=model-formats-failed]').count(), 0, `the failure block is still shown (${locale})`);
      assert((await page.locator('[data-testid=model-format-fbx]').innerText()).includes(messages['ui.workspace.model_formats.status.available']), `FBX is not 已有 (${locale})`);
      const tiles = page.locator('[data-testid=version-tile]');
      const keys = await tiles.evaluateAll((nodes) => nodes.map((node) => `${node.getAttribute('data-key')}:${node.getAttribute('data-status')}:${node.getAttribute('data-current')}`));
      assert.deepEqual(keys, [`n:${shared.taskID}:ready:true`], `version strip ${keys.join(', ')}`);
      note = keys.join(', ');
      await shot(`J-refetched-${locale}`);
    }
    return note;
  });
  await step('J: each format downloads with its media type, nosniff, as an attachment', async () => {
    const notes = [];
    for (const format of application.model_delivery.formats) {
      const link = page.locator(`[data-testid=model-format-${format.id}] a[download]`);
      if (await link.count() === 0) continue;
      const response = await context.request.get(new URL(await link.first().getAttribute('href'), BASE).toString());
      assert.equal(response.status(), 200, `${format.id} download`);
      const headers = response.headers();
      const disposition = headers['content-disposition'] ?? '';
      assert(disposition.startsWith('attachment'), `${format.id} disposition ${disposition}`);
      assert.equal(headers['x-content-type-options'], 'nosniff', `${format.id} nosniff`);
      const packaged = /\.zip"?$/i.test(disposition) || /\.zip/i.test(decodeURIComponent(disposition));
      const expected = packaged ? 'application/zip' : format.download_media_type;
      assert.equal(headers['content-type'].split(';')[0].trim(), expected, `${format.id} content-type (${disposition})`);
      notes.push(`${format.id}=${headers['content-type']}`);
    }
    assert(notes.length >= 7, `downloads ${notes.join(', ')}`);
    return notes.join(', ');
  });
  await step('no page errors and no outside requests (J)', async () => {
    assertNoRateLimit(page);
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

/** D: a direct convert_model_format Task is visible in the Formats tab and keeps one source version. */
async function directConversionChecks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
  const outside = [];
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  const user = `meshy330-direct-${engine}-${viewport}-${Date.now()}`;
  await login(context, user);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  const model = await source(context, `#330 direct ${engine} ${viewport}`, MESHY);
  await openStage(page, model.sessionID);
  await step('D: a direct conversion is started from chat', async () => {
    await page.locator('[data-testid=studio-composer] textarea').fill('把 @model1 转换成 FBX');
    await page.locator('[data-testid=composer-send]').click();
    // Mock sessions may auto-approve the Agent operation; when a card waits,
    // approve it here. Either way the conversion itself is the assertion below.
    const deadline = Date.now() + 10000;
    for (;;) {
      const listed = await api(context, 'GET', pendingApprovalsRoute(model.sessionID));
      const request = listed.items?.[0];
      if (request?.request_id) {
        await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', encodeURIComponent(request.request_id)), { decision: 'approve' });
        return request.request_id;
      }
      if (Date.now() >= deadline) return 'auto-approved';
      await new Promise((resolve) => setTimeout(resolve, 500));
    }
  });
  await step('D: the direct conversion delivers FBX without a duplicate paid action', async () => {
    const deadline = Date.now() + 120000;
    for (;;) {
      const body = await api(context, 'GET', formatsRoute(model.taskID));
      const fbx = body.formats?.find((row) => row.id === 'fbx');
      if (fbx?.status === 'available') break;
      assert(Date.now() < deadline, `direct conversion FBX did not arrive: ${JSON.stringify(fbx)}`);
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    for (const [locale, messages] of [[application.localization.default_locale, application.content.messages], ['en', { ...application.content.messages, ...english }]]) {
      await context.addCookies([{ name: localeCookie, value: locale, url: BASE }]);
      await openStage(page, model.sessionID, 'formats');
      await page.locator('[data-testid=model-format-fbx][data-status=available]').waitFor({ timeout: 30000 });
      assert.equal(await page.locator('[data-testid=model-format-action-fbx]').count(), 0, `FBX offers another action (${locale})`);
      assert.equal(await page.locator('[data-testid=model-formats-failed]').count(), 0, `a direct conversion produced a failure block (${locale})`);
      assert.equal(await page.locator('[data-testid=model-format-fbx] a[download]').count(), 1, `FBX has no download (${locale})`);
      const tiles = page.locator('[data-testid=version-tile]');
      const keys = await tiles.evaluateAll((nodes) => nodes.map((node) => `${node.getAttribute('data-key')}:${node.getAttribute('data-status')}:${node.getAttribute('data-current')}`));
      assert.equal(keys.length, 1, `version strip ${keys.join(', ')}`);
      assert(keys[0].startsWith(`n:${model.taskID}:ready`), `version strip ${keys.join(', ')}`);
      assert((await page.locator('[data-testid=model-format-fbx]').innerText()).includes(messages['ui.workspace.model_formats.status.available']), 'FBX is not labelled 已有');
      await shot(`D-direct-${locale}`);
    }
  });
  await step('D: no page errors and no outside requests', async () => {
    assertNoRateLimit(page);
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

/** K: a failed archive request shows the localized too-large reason in its own fresh context. */
async function archiveUnavailableChecks(engine, browser, [width, height], shared) {
  const viewport = `${width}x${height}`;
  const { context, page, outside, errors } = await sharedContext(browser, [width, height], shared);
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  await context.route(`${BASE}${archiveRoute(shared.taskID)}`, (route) => route.fulfill({
    status: 413,
    contentType: 'application/json',
    body: JSON.stringify({ code: 'too_large', detail: 'server detail must not replace the product reason' }),
  }));
  await step('K: a too-large archive shows the localized Schema reason', async () => {
    for (const [locale, messages] of [[application.localization.default_locale, application.content.messages], ['en', { ...application.content.messages, ...english }]]) {
      await context.addCookies([{ name: localeCookie, value: locale, url: BASE }]);
      await openStage(page, shared.sessionID, 'formats');
      const link = page.locator('[data-testid=model-archive-link]');
      await link.waitFor({ timeout: 60000 });
      await link.click();
      const alert = page.getByRole('alert').filter({ hasText: messages['error.model_archive_too_large'] });
      await withRateLimit(page, alert.waitFor({ timeout: 20000 }));
      assert.equal((await alert.innerText()).trim(), messages['error.model_archive_too_large']);
      assert(!(await alert.innerText()).includes(messages['ui.workspace.model_formats.download_failed']));
    }
    await context.addCookies([{ name: localeCookie, value: application.localization.default_locale, url: BASE }]);
    await shot('K-archive-too-large');
  });
  await step('no page errors and no outside requests (K)', async () => {
    assertNoRateLimit(page);
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
  });
  await context.close();
}

/** PARTS=J runs only the refetch-success part (set up once, ~2 min); the default runs everything. */
const PARTS = new Set((process.env.PARTS || 'all').split(',').map((item) => item.trim()));
async function partJ() {
  const refetched = await refetchedDelivery(browsers[ENGINES[0]]);
  for (const [engineIndex, engine] of ENGINES.entries()) {
    for (const [viewportIndex, viewport] of VIEWPORTS.entries()) {
      await refetchedChecks(engine, browsers[engine], viewport, refetched, engineIndex === 0 && viewportIndex === 0);
      await archiveUnavailableChecks(engine, browsers[engine], viewport, refetched);
      if (engineIndex !== ENGINES.length - 1 || viewportIndex !== VIEWPORTS.length - 1) {
        await pauseBetweenCombinations('J/K');
      }
    }
  }
}

async function partD() {
  for (const [engineIndex, engine] of ENGINES.entries()) {
    for (const [viewportIndex, viewport] of VIEWPORTS.entries()) {
      await directConversionChecks(engine, browsers[engine], viewport);
      if (engineIndex !== ENGINES.length - 1 || viewportIndex !== VIEWPORTS.length - 1) {
        await pauseBetweenCombinations('D');
      }
    }
  }
}

const browsers = {};
try {
  for (const engine of ENGINES) browsers[engine] = await pw[engine].launch({ headless: true });
  // K is not a separate part: it belongs to the J refetch checks. Selecting
  // only J or only D must never pull in the full A-I suite through a third
  // token (for example PARTS=J,K); D and J may be selected together.
  const runJ = PARTS.has('J') || PARTS.has('K');
  const runAll = !runJ && !PARTS.has('D');
  if (runAll) {
    for (const engine of ENGINES) {
      for (const viewport of VIEWPORTS) await checks(engine, browsers[engine], viewport);
    }
    const shared = await failedDelivery(browsers[ENGINES[0]]);
    for (const [engineIndex, engine] of ENGINES.entries()) {
      for (const [viewportIndex, viewport] of VIEWPORTS.entries()) {
        await failedDeliveryChecks(engine, browsers[engine], viewport, shared, 'free', false);
      }
      if (engineIndex === ENGINES.length - 1) await failedDeliveryChecks(ENGINES[0], browsers[ENGINES[0]], VIEWPORTS[0], shared, 'free', true);
    }
    for (const engine of ENGINES) {
      for (const viewport of VIEWPORTS) await failedDeliveryChecks(engine, browsers[engine], viewport, shared, 'paid', false);
    }
    for (const engine of ENGINES) {
      for (const viewport of VIEWPORTS) await mixedFailureChecks(engine, browsers[engine], viewport, shared);
    }
  }
  if (runJ || runAll) await partJ();
  if (PARTS.has('D') || runAll) await partD();
} finally {
  for (const browser of Object.values(browsers)) await browser.close();
  writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ scope: 'local mock server only; no deployed environment, no paid upstream', base: BASE, results }, null, 2)}\n`);
}
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
console.log(`report: ${path.join(OUT, 'report.json')}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
