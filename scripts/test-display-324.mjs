#!/usr/bin/env node
/**
 * Real-browser checks for the #324 display fixes. No credentials and no paid upstream: a local server with
 * mock providers and the mock Agent, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18431 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18431 OUT=<scratch>/out node scripts/test-display-324.mjs
 * Per engine and viewport (a fresh person each time):
 *  1. Tripo segmentation is quoted 90 in the 加工 panel (service list, step line, total) and after saving it
 *     as the session's 功能 setting; no "40–90" range anywhere.
 *  3. The model picker trigger names both chosen models; the hover holds model and provider. Closing a
 *     picker panel with Esc opens no hint over the neighbouring pickers (#322 note 99059); the hint is passive.
 *  4. The 格式 tab's completion button says 免费 when only free conversions remain.
 *  5/12. An Agent approval card for Tripo Smart Segmentation names its source model (not a bare
 *     placeholder) and reads the Tripo balance (no "balance unavailable").
 *  6. A failed, uncharged task leaves the session credits unchanged.
 *  7. Both consent clicks of a paused Meshy Retexture step name its credits.
 *  8. The Retexture result has a picture on its version tile.
 * 11. A step no service can run on the model is not priced.
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
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'display-324-'));
mkdirSync(OUT, { recursive: true });

const { application, offerings: catalogOfferings } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const fill = (template, values) => template.replace(/\{(\w+)\}/g, (_, name) => String(values[name]));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const label = (id) => application.capabilities.find((capability) => capability.id === id).label_cn;
const offeringName = (id) => catalogOfferings.find((offering) => offering.id === id).model_name;
const TRIPO_SOURCE = 'tripo/tripo-v3.1-full/text-to-3d';
const MESHY_SOURCE = 'meshy/meshy-7.1/text-to-3d';
const RODIN_SOURCE = 'hyper3d/rodin-gen-2.5/text-to-3d';
const TRIPO_SEGMENT = 'tripo/geometry/segment';
const TRIPO_SMART = 'tripo/geometry/smartsegment-model';

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return body;
}

async function login(context, user) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
}

async function submit(context, sessionID, capability, offeringID, input) {
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: sessionID, capability, offering_id: offeringID, input });
  const key = `display324-${prepared.prepared_input_id}`;
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

/** A finished 3D source in a fresh session, through the public prepare → grant → submit path. */
async function source(context, title, offeringID, prompt) {
  const session = await api(context, 'POST', legacy.chat_sessions, { title });
  const consent = catalogOfferings.find((offering) => offering.id === offeringID).parameters.some((parameter) => parameter.name === 'authorize_extra_credits');
  const taskID = await submit(context, session.id, 'text-to-3d', offeringID, { prompt, ...(consent ? { authorize_extra_credits: true } : {}) });
  const task = await waitTask(context, taskID);
  return { sessionID: session.id, taskID, task };
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

async function panelTab(page, id) {
  await page.click(`[data-testid=right-panel-tab-${id}]`);
}

const RANGE = /\d\s*[–-]\s*\d/;

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  await login(context, `display324-${engine}-${viewport}-${Date.now()}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, name, fn);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });
  const panel = page.locator('[data-testid=model-processing-panel]');

  const tripo = await source(context, `#324 tripo ${engine} ${viewport}`, TRIPO_SOURCE, 'A small wooden stool');
  await openStudio(page, tripo.sessionID);
  await showTask(page, tripo.taskID);

  await step('1: 加工 quotes Tripo segmentation at 90, with no range', async () => {
    await panelTab(page, 'process');
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).check({ timeout: 20000 });
    const service = panel.locator('[data-testid=processing-offering-mesh-segmentation], [data-testid=processing-provider-mesh-segmentation]').first();
    await service.waitFor();
    const quote = await panel.locator('[data-testid=processing-quote-mesh-segmentation]').innerText();
    const total = await panel.locator('[data-testid=processing-cost]').innerText();
    const chosen = await service.innerText();
    assert(chosen.includes(offeringName(TRIPO_SEGMENT)), `default service ${chosen}`);
    assert(quote.includes('90') && !RANGE.test(quote), `step quote ${quote}`);
    assert.equal(total.trim(), fill(text('ui.studio.processing.cost_total'), { credits: 90 }));
    assert(!RANGE.test(chosen), `service line ${chosen}`);
    // The service list states each choice's quote; none is a range.
    const trigger = service.getByRole('combobox');
    if (await trigger.count()) {
      await trigger.click();
      await page.getByRole('option').first().waitFor({ timeout: 5000 });
      const options = await page.getByRole('option').allInnerTexts();
      await page.keyboard.press('Escape');
      await page.getByRole('listbox').waitFor({ state: 'hidden', timeout: 5000 }).catch(() => {});
      assert(options.length > 0 && options.every((option) => !RANGE.test(option)), `options ${options.join(' | ')}`);
      assert(options.some((option) => option.includes(offeringName(TRIPO_SEGMENT)) && option.includes('90')), `options ${options.join(' | ')}`);
      assert(options.some((option) => option.includes(offeringName(TRIPO_SMART)) && option.includes('105')), `options ${options.join(' | ')}`);
    }
    await shot('1-process');
    return `${chosen} | ${quote} | ${total}`;
  });

  await step('1: the 功能 setting for Tripo segmentation is quoted 90', async () => {
    await panelTab(page, 'operations');
    const harness = page.locator('[data-testid=harness-panel]');
    await harness.getByRole('radio', { name: new RegExp(application.chat.harness.operations.find((item) => item.id === 'mesh-segmentation').label) }).check({ timeout: 20000 });
    const service = harness.getByRole('combobox').first();
    await waitFor(async () => (await service.innerText()).includes(offeringName(TRIPO_SEGMENT)) || null, 'default harness service', 10000).catch(async () => {
      await service.click();
      await page.getByRole('option', { name: new RegExp(offeringName(TRIPO_SEGMENT)) }).click();
    });
    const stageSource = harness.getByRole('button', { name: text('ui.harness.use_current') });
    if (await stageSource.count()) await stageSource.click();
    await harness.getByRole('button', { name: text('ui.harness.save') }).click();
    const status = harness.locator('[data-testid=harness-status]');
    await waitFor(async () => (await status.innerText()).includes(fill(text('ui.harness.estimated_credits'), { credits: 90 })), 'harness status has no 90', 15000);
    const shown = await status.innerText();
    await shot('1-harness');
    await harness.getByRole('button', { name: text('ui.harness.clear') }).click();
    await waitFor(async () => (await status.innerText()).includes(text('ui.harness.status_free')), 'harness not cleared', 15000);
    return shown.replace(/\s+/g, ' ');
  });

  await step('3: the model picker names both chosen models; the hover names model and provider', async () => {
    await page.click('[data-testid=workspace-model-picker]');
    const popup = page.locator('[data-testid=workspace-model-picker-panel]');
    await popup.waitFor();
    await popup.getByText('Nano Banana Pro (高质量)', { exact: true }).first().click();
    await popup.getByText('Meshy 7.1', { exact: true }).first().click();
    await page.keyboard.press('Escape');
    const trigger = page.locator('[data-testid=workspace-model-picker]');
    const parts = await trigger.locator('[data-part]').allInnerTexts();
    assert.deepEqual(parts, ['Nano Banana Pro (高质量)', 'Meshy 7.1']);
    const name = await trigger.getAttribute('aria-label');
    assert(name.includes('Nano Banana Pro (高质量)（Meshy AI）') && name.includes('Meshy 7.1（Meshy AI）'), `aria-label ${name}`);
    const box = await trigger.boundingBox();
    const fits = await trigger.locator('[data-part]').evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().right));
    assert(fits.every((right) => right <= box.x + box.width), 'a name runs past the trigger');
    await trigger.hover();
    await page.locator('[data-testid=hint-tooltip]').first().waitFor({ timeout: 5000 });
    await shot('3-picker');
    return `${parts.join(' · ')} | ${name}`;
  });

  await step('3: closing a picker panel with Esc opens no hint over the pickers beside it', async () => {
    // Every composer picker shares the trigger and its hint (TablePopover); the chat model picker appears only
    // when an administrator offers two chat models, so the ones the mock stack shows stand for it.
    const pickers = ['workspace-model-picker', 'workspace-skill-picker', 'workspace-agent-scope-picker', 'workspace-reasoning-picker'];
    const checked = [];
    for (const id of pickers) {
      const trigger = page.locator(`[data-testid=${id}]`);
      if (!(await trigger.count()) || await trigger.isDisabled()) continue;
      await trigger.click();
      const popup = page.locator(`[data-testid=${id}-panel]`);
      await popup.waitFor();
      // As in #322 note 99059: the pointer is over the panel, not the trigger, when Esc closes it.
      const inside = await popup.boundingBox();
      await page.mouse.move(inside.x + inside.width / 2, inside.y + 12);
      await page.keyboard.press('Escape');
      await popup.waitFor({ state: 'hidden' });
      await page.waitForTimeout(600);
      assert.equal(await page.locator('[data-testid=hint-tooltip]').count(), 0, `${id}: a hint opened after Esc`);
      assert(await trigger.evaluate((node) => node === document.activeElement), `${id}: focus did not return to the trigger`);
      for (const neighbour of pickers.filter((other) => other !== id)) {
        const box = await page.locator(`[data-testid=${neighbour}]`).boundingBox().catch(() => null);
        if (!box) continue;
        const hit = await page.evaluate(([x, y, other]) => Boolean(document.elementFromPoint(x, y)?.closest(`[data-testid=${other}]`)), [box.x + 8, box.y + box.height / 2, neighbour]);
        assert(hit, `${id}: ${neighbour} is covered after Esc`);
      }
      checked.push(id);
    }
    assert(checked.includes('workspace-model-picker'), `checked ${checked.join(', ')}`);
    // Hovering the trigger still shows the hint at once; it never takes the pointer and leaves with it.
    const trigger = page.locator('[data-testid=workspace-model-picker]');
    await trigger.hover();
    const bubble = page.locator('[data-testid=hint-tooltip]').first();
    await bubble.waitFor({ timeout: 5000 });
    assert.equal(await bubble.evaluate((node) => getComputedStyle(node).pointerEvents), 'none', 'the hint takes the pointer');
    await page.mouse.move(width / 2, 120);
    await bubble.waitFor({ state: 'detached', timeout: 2000 });
    return `checked ${checked.join(', ')}`;
  });

  await step('5/12: the Smart Segmentation approval card names its source model and reads the Tripo balance', async () => {
    const box = page.locator('[data-testid=studio-composer] textarea');
    await box.fill('给当前模型做模型分割');
    await page.locator('[data-testid=composer-send]').click();
    const card = page.locator('[data-testid=chat-choice-card][data-state=pending]');
    await card.waitFor({ timeout: 30000 });
    await card.locator(`[data-option-id="${TRIPO_SMART}"]`).click();
    await card.locator('[data-testid=chat-choice-confirm]').click();
    const approval = page.locator('[data-testid=studio-approval-card][data-status=pending]');
    await approval.waitFor({ timeout: 60000 });
    await page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
    const credits = await approval.locator('[data-testid=studio-approval-credits]').innerText();
    assert(credits.includes('105'), `credits ${credits}`);
    const balance = await approval.locator('[data-testid=studio-approval-balance]').innerText();
    assert(!balance.includes(fill(text('ui.studio.approval.card.balance_unknown'), { provider: '' }).trim()) && /\d/.test(balance), `balance ${balance}`);
    const inputs = approval.locator('[data-testid=studio-approval-input]');
    const count = await inputs.count();
    assert(count >= 1, 'no inputs');
    const described = [];
    for (let index = 0; index < count; index++) {
      const input = inputs.nth(index);
      const words = (await input.innerText()).trim();
      assert(words.length > 0, `input ${index} has no words`);
      described.push(`${await input.getAttribute('data-kind')}:${words}:${await input.locator('[data-testid=studio-approval-thumb]').getAttribute('data-state')}`);
    }
    assert(described.some((entry) => entry.startsWith('task:') && entry.includes('@model')), `inputs ${described.join(' | ')}`);
    await approval.screenshot({ path: path.join(OUT, `${engine}-${viewport}-5-approval.png`) });
    await approval.locator('[data-testid=studio-approval-deny]').click();
    await approval.waitFor({ state: 'detached', timeout: 30000 });
    return `${credits} | ${balance} | ${described.join(' | ')}`;
  });

  await step('6: a failed task that was not charged leaves the session credits unchanged', async () => {
    const read = async () => (await api(context, 'GET', `${legacy.chat_sessions}/${tripo.sessionID}`)).estimatedCredits ?? null;
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]');
    const header = page.locator('[data-testid=studio-session-credits]');
    const before = await header.innerText();
    const failed = await submit(context, tripo.sessionID, 'text-to-3d', MESHY_SOURCE, { prompt: 'a stool [mock:insufficient_balance]' });
    const task = await waitTask(context, failed, ['FAILED']);
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]');
    const after = await page.locator('[data-testid=studio-session-credits]').innerText();
    assert.equal(after, before, `header ${before} -> ${after}`);
    await shot('6-session-credits');
    return `${task.state}; header ${before} -> ${after}; api ${await read()}`;
  });

  // A Rodin model over every retexture service's size limit.
  const large = await source(context, `#324 large ${engine} ${viewport}`, RODIN_SOURCE, 'A tall carved totem [mock:large_model]');
  await step('11: a step no service can run on the model is not priced', async () => {
    await openStudio(page, large.sessionID);
    await showTask(page, large.taskID);
    await panelTab(page, 'process');
    await panel.getByRole('checkbox', { name: label('retexture') }).check({ timeout: 20000 });
    await panel.locator('[data-testid=processing-refused-retexture]').waitFor({ timeout: 10000 });
    const none = panel.locator('[data-testid=processing-cost]');
    assert.equal(await none.getAttribute('data-kind'), 'none');
    assert.equal((await none.innerText()).trim(), text('ui.studio.processing.cost_none'));
    // Retexture after segmentation works on the segmented result, so it is priced again then; alone it is not.
    await panel.getByRole('checkbox', { name: label('retexture') }).uncheck();
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).check();
    await waitFor(async () => (await none.getAttribute('data-kind')) === 'total', 'segmentation not priced', 10000);
    const total = (await none.innerText()).trim();
    assert.equal(total, fill(text('ui.studio.processing.cost_total'), { credits: 105 }), `total ${total}`);
    await shot('11-unrunnable');
    return total;
  });

  const meshy = await source(context, `#324 meshy ${engine} ${viewport}`, MESHY_SOURCE, 'A small wooden stool');
  await step('4: the 格式 completion button says 免费 when only free conversions remain', async () => {
    await openStudio(page, meshy.sessionID);
    await showTask(page, meshy.taskID);
    await panelTab(page, 'formats');
    const complete = page.locator('[data-testid=model-formats-complete]');
    await complete.waitFor({ timeout: 20000 });
    const words = (await complete.innerText()).trim();
    assert.equal(words, text('ui.workspace.model_formats.complete_free'));
    await shot('4-formats');
    return words;
  });

  let retextureTask = '';
  await step('7: both consent clicks of a paused Meshy Retexture step name its credits', async () => {
    await panelTab(page, 'process');
    await panel.getByRole('checkbox', { name: label('retexture') }).check({ timeout: 20000 });
    const prompt = panel.getByRole('textbox').first();
    await prompt.fill('warm carved walnut');
    await panel.getByRole('button', { name: text('ui.workspace.processing.plan_confirm') }).click();
    const prepare = panel.getByRole('button', { name: new RegExp(`^${text('ui.workspace.processing.plan_prepare_step')}`) });
    await prepare.waitFor({ timeout: 30000 });
    const first = (await prepare.innerText()).trim();
    assert(/ · \d+$/.test(first), `prepare ${first}`);
    await prepare.click();
    const confirm = panel.getByRole('button', { name: new RegExp(`^${text('ui.workspace.processing.plan_confirm_step')}`) });
    await confirm.waitFor({ timeout: 30000 });
    const second = (await confirm.innerText()).trim();
    assert(/ · \d+$/.test(second), `confirm ${second}`);
    await shot('7-confirm');
    await confirm.click();
    const plans = await waitFor(async () => {
      const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(meshy.sessionID)}`);
      return (listed.plans ?? []).flatMap((plan) => plan.steps).find((item) => item.task_id)?.task_id;
    }, 'the step did not run', 30000);
    retextureTask = plans;
    return `${first} | ${second}`;
  });

  await step('8: the Retexture result has a picture on its version tile', async () => {
    assert(retextureTask, 'no retexture task');
    const task = await waitTask(context, retextureTask);
    assert(task.assets?.some((asset) => asset.role === 'thumbnail'), `assets ${JSON.stringify(task.assets?.map((asset) => asset.role))}`);
    const tile = page.locator(`[data-testid=version-tile][data-key="n:${retextureTask}"]`);
    await tile.waitFor({ timeout: 30000 });
    await waitFor(async () => (await tile.locator('[data-testid=version-thumb] img').count()) > 0, 'the tile has no picture', 20000);
    await tile.screenshot({ path: path.join(OUT, `${engine}-${viewport}-8-tile.png`) });
    return `thumbnail asset and tile picture for ${retextureTask}`;
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
