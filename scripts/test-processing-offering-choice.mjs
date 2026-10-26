#!/usr/bin/env node
/**
 * Real-browser check for #321: the 3D「加工」panel lets the person choose among several compatible
 * Offerings of one processing capability, and the choice drives the quote and the submitted plan.
 *
 * Needs a local server with mock providers and mock login (no credentials, no paid calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18321 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18321 node scripts/test-processing-offering-choice.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * OUT (screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH. Every request
 * outside BASE is refused and counted.
 *
 * Per engine and viewport: a mock Hyper3D Rodin text-to-3d result is created through the public
 * prepare → grant → submit API in a fresh session, then opened in the studio. In 加工:
 *  - 模型分割 lists Tripo Smart Segmentation (any model) and both Bang Offerings (the local-model path
 *    and the Rodin-source path), in Schema order;
 *  - 重贴图 lists Meshy Retexture and Hyper3D Texture but not Tripo Texture (its source contract
 *    needs a Tripo task); the default is the first compatible one (Meshy, credits) and choosing
 *    Hyper3D changes the step quote and the footer to the native estimate;
 *  - with Hyper3D Texture chosen, 确认 stays disabled and the panel names the missing reference image
 *    until it is attached; then 确认 posts a plan whose step is exactly that Offering and revision,
 *    and the server's plan and created Task use it.
 * Then, on a mock Tripo text-to-3d result (#185 Quick Cap enabled): 模型分割 → Tripo Semantic
 * Segmentation makes 部件补全 offer both Tripo completion services (AI Part Completion, Quick Part Cap).
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { deflateSync } from 'node:zlib';
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
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'processing-offering-choice-'));
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
const MESHY = 'meshy/processing/retexture';
const HYPER3D_TEXTURE = 'hyper3d/texture/local-model';
const TRIPO_TEXTURE = 'tripo/processing/texture';
const BANG_LOCAL = 'hyper3d/bang/local-model';
const BANG_RODIN = 'hyper3d/bang/rodin-task';
const TRIPO_SMART = 'tripo/geometry/smartsegment-model';
const TRIPO_SOURCE = 'tripo/tripo-v3.1-full/text-to-3d';

function solidPng(width, height, [r, g, b]) {
  const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
  const crc = (buffer) => { let c = 0xffffffff; for (const byte of buffer) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
  const chunk = (type, data) => {
    const length = Buffer.alloc(4); length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const sum = Buffer.alloc(4); sum.writeUInt32BE(crc(body));
    return Buffer.concat([length, body, sum]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 2;
  const row = Buffer.concat([Buffer.from([0]), Buffer.from(Array.from({ length: width }, () => [r, g, b]).flat())]);
  const raw = Buffer.concat(Array.from({ length: height }, () => row));
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', header), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}
const REFERENCE = path.join(OUT, 'reference-256.png');
writeFileSync(REFERENCE, solidPng(256, 256, [90, 140, 200]));

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 200)}`);
  return body;
}

/** A mock Rodin text-to-3d result in a fresh session, through the public prepare → grant → submit path. */
async function rodinSource(context, title, offeringID = SOURCE_OFFERING) {
  const session = await api(context, 'POST', legacy.chat_sessions, { title });
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: offeringID,
    input: { prompt: 'A simple low-poly wooden stool', authorize_extra_credits: true } });
  const key = `offering-choice-${prepared.prepared_input_id}`;
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

/** The option labels of a step's service selector, read from its open list. */
async function openOptions(page, step) {
  await step.locator('[data-slot=select-trigger], button').first().click();
  const list = page.getByRole('option');
  await list.first().waitFor({ timeout: 5000 });
  return (await list.allInnerTexts()).map((item) => item.replace(/\s+/g, ' ').trim());
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
  const outside = [];
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  const user = `offering-choice-${engine}-${viewport}-${Date.now()}`;
  const login = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(login.status(), 200, 'mock login');
  const source = await rodinSource(context, `#321 ${engine} ${viewport}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const failed = [];
  page.on('requestfailed', (request) => failed.push(`${request.method()} ${request.url().slice(0, 80)} ${request.failure()?.errorText}`));
  const planPosts = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === routes.processing_plans) planPosts.push(request.postDataJSON());
  });
  await page.goto(`${BASE}/?session=${encodeURIComponent(source.sessionID)}`);
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const panel = page.locator('[data-testid=model-processing-panel]');

  await step('the Rodin result opens with the 加工 tab', async () => {
    const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
    if (await tile.count()) await tile.click();
    await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
    await panel.waitFor({ timeout: 20000 });
    await panel.getByRole('checkbox', { name: label('retexture') }).waitFor({ timeout: 20000 });
  });

  await step('模型分割 offers Tripo Smart Segmentation and both Bang paths for a Rodin result', async () => {
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).check();
    const choice = panel.locator('[data-testid=processing-offering-mesh-segmentation]');
    await choice.waitFor({ timeout: 5000 });
    assert.equal(await choice.getByText(text('ui.workspace.processing.offering'), { exact: true }).count(), 1, 'selector label');
    const options = await openOptions(page, choice);
    await page.keyboard.press('Escape');
    assert.equal(options.length, 3, `options: ${options.join(' | ')}`);
    assert(/^Tripo3D · Tripo Smart Segmentation/.test(options[0]) && options[0].includes(text('ui.workspace.harness.credits')), options[0]);
    assert(options.slice(1).every((option) => /^Hyper3D · /.test(option) && option.includes(text('ui.pricing.default_estimate').split('{')[0])), options.join(' | '));
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-1-segmentation.png`) });
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).uncheck();
    return options.join(' | ');
  });

  let before = '';
  let after = '';
  await step('重贴图 lists only compatible services; choosing Hyper3D Texture changes the quote', async () => {
    await panel.getByRole('checkbox', { name: label('retexture') }).check();
    const choice = panel.locator('[data-testid=processing-offering-retexture]');
    await choice.waitFor({ timeout: 5000 });
    const quote = panel.locator('[data-testid=processing-quote-retexture]');
    before = (await quote.innerText()).trim();
    assert(before.startsWith(text('ui.workspace.harness.credits')), `default quote ${before}`);
    const options = await openOptions(page, choice);
    assert.equal(options.length, 2, `options: ${options.join(' | ')}`);
    assert(/^Meshy/.test(options[0]) && /^Hyper3D/.test(options[1]), `order: ${options.join(' | ')}`);
    assert(!options.some((option) => /Tripo/.test(option)), 'Tripo Texture needs a Tripo task source');
    await page.getByRole('option').filter({ hasText: /^Hyper3D/ }).click();
    await page.waitForTimeout(300);
    after = (await quote.innerText()).trim();
    assert.notEqual(after, before, 'quote did not change');
    assert(after.startsWith(text('ui.pricing.default_estimate').split('{')[0]) && /Hyper3D credits/.test(after), `chosen quote ${after}`);
    const footer = (await page.locator('[data-testid=processing-cost-native]').innerText()).replace(/\s+/g, ' ');
    assert(footer.includes(after), `footer ${footer}`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-2-retexture-hyper3d.png`) });
    return `${before} → ${after}`;
  });

  await step('submit uses the chosen Offering (POST body, saved plan and created Task)', async () => {
    const retexture = panel.locator('[data-testid=processing-offering-retexture]').locator('xpath=..');
    const confirm = panel.getByRole('button', { name: text('ui.workspace.processing.plan_confirm') });
    // Required input missing: the button stays disabled and the panel says what is missing.
    const missingPrefix = text('ui.workspace.processing.required_missing').split('{')[0];
    assert(await confirm.isDisabled(), 'confirm is enabled before the reference image is attached');
    const stepMissing = (await panel.locator('[data-testid=processing-missing-retexture]').innerText()).trim();
    const footerMissing = (await panel.locator('[data-testid=processing-missing]').innerText()).trim();
    assert(stepMissing.startsWith(missingPrefix) && footerMissing.startsWith(missingPrefix) && footerMissing.includes(label('retexture')), `missing: ${stepMissing} / ${footerMissing}`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-2b-missing-reference.png`) });
    await retexture.locator('input[type=file]').first().setInputFiles(REFERENCE);
    await panel.locator('[data-testid=processing-missing]').waitFor({ state: 'detached', timeout: 5000 });
    await page.waitForFunction((name) => [...document.querySelectorAll('[data-testid=model-processing-panel] button')]
      .some((button) => button.textContent?.includes(name) && !button.disabled), text('ui.workspace.processing.plan_confirm'), { timeout: 10000 });
    await confirm.click();
    const deadline = Date.now() + 15000;
    while (planPosts.length === 0 && Date.now() < deadline) await page.waitForTimeout(200);
    if (planPosts.length === 0) {
      await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-3-not-submitted.png`) });
      const alert = (await panel.locator('[role=alert]').allInnerTexts()).join(' / ');
      assert.fail(`no plan POST; panel alerts: ${alert || 'none'}; failed requests: ${failed.join(' | ') || 'none'}`);
    }
    assert.equal(planPosts.length, 1, 'one plan POST');
    const [body] = planPosts;
    const offerings = await api(context, 'GET', `${routes.offerings}?capability=retexture`);
    const chosen = offerings.offerings.find((offering) => offering.id === HYPER3D_TEXTURE);
    assert.equal(body.steps.length, 1);
    assert.equal(body.steps[0].offering_id, HYPER3D_TEXTURE);
    assert.equal(body.steps[0].expected_offering_revision, chosen.revision);
    assert(typeof body.steps[0].input.reference_image === 'string' && !body.steps[0].input.reference_image.startsWith('data:'), 'reference image uploaded as an input asset');
    assert.equal(body.steps[0].input.target_formats, undefined, 'no Meshy-only fixed values leak into the Hyper3D step');
    // Hyper3D Texture is a retexture: under the session's manual approval it pauses for the person, who
    // approves it in this panel (prepare → confirm). Every grant request must name the chosen step's prepared input.
    let plan;
    let task;
    const approvals = [];
    const until = Date.now() + 90000;
    while (Date.now() < until) {
      const listed = await api(context, 'GET', `${routes.processing_plans}?session_id=${encodeURIComponent(source.sessionID)}`);
      plan = listed.plans?.find((candidate) => candidate.source_task_id === source.taskID);
      assert(plan?.state !== 'FAILED', `plan failed: ${plan?.failure}`);
      if (plan?.steps?.[0]?.task_id) {
        task = await api(context, 'GET', routes.task_item.replace('{taskID}', plan.steps[0].task_id));
        if (task.state === 'SUCCEEDED' || task.state === 'FAILED') break;
      } else if (plan?.state === 'PAUSED') {
        const action = panel.getByRole('button', { name: new RegExp(`^(${[text('ui.workspace.processing.plan_prepare_step'), text('ui.workspace.processing.plan_confirm_step'), text('ui.workspace.processing.plan_resume')].join('|')})`) });
        if (await action.count() && await action.first().isEnabled()) {
          approvals.push((await action.first().innerText()).trim());
          await action.first().click();
          await page.waitForTimeout(800);
        }
      }
      await page.waitForTimeout(700);
    }
    assert(plan, 'plan saved');
    assert.equal(plan.steps[0].offering_id, HYPER3D_TEXTURE);
    assert(task, 'step task created');
    const taskOffering = task.offering_id ?? task.offering?.id;
    assert.equal(taskOffering, HYPER3D_TEXTURE, `task offering ${taskOffering}`);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-3-submitted.png`) });
    assert(plan.steps[0].native_quote?.total_amount === '2', `frozen quote ${JSON.stringify(plan.steps[0].native_quote)}`);
    return `plan ${plan.id} step ${plan.steps[0].offering_id}@${body.steps[0].expected_offering_revision} quote ${plan.steps[0].native_quote.total_amount} ${plan.steps[0].native_quote.unit_label}; panel approvals: ${approvals.join(' → ') || 'none'}; task ${task.id} ${task.state} offering ${taskOffering}`;
  });

  await step('Quick Cap (#185): after Tripo Semantic Segmentation, 部件补全 offers both Tripo completion services', async () => {
    const tripo = await rodinSource(context, `#321 quick cap ${engine} ${viewport}`, TRIPO_SOURCE);
    await page.goto(`${BASE}/?session=${encodeURIComponent(tripo.sessionID)}`);
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
    if (await tile.count()) await tile.click();
    await page.click('[data-testid=right-panel-tab-process]').catch(() => {});
    await panel.getByRole('checkbox', { name: label('mesh-segmentation') }).check({ timeout: 20000 });
    await panel.getByRole('checkbox', { name: label('mesh-completion') }).check();
    const segmentation = panel.locator('[data-testid=processing-offering-mesh-segmentation]');
    await segmentation.waitFor({ timeout: 5000 });
    const options = await openOptions(page, segmentation);
    assert(options.some((option) => /^Tripo3D · Tripo Semantic Segmentation/.test(option)), `segmentation: ${options.join(' | ')}`);
    await page.getByRole('option').filter({ hasText: /Tripo Semantic Segmentation/ }).click();
    await page.getByRole('option').first().waitFor({ state: 'detached', timeout: 5000 });
    const completion = panel.locator('[data-testid=processing-offering-mesh-completion]');
    await completion.waitFor({ timeout: 5000 });
    const completions = await openOptions(page, completion);
    await page.keyboard.press('Escape');
    assert.equal(completions.length, 2, `completion: ${completions.join(' | ')}`);
    assert(/^Tripo3D · Tripo AI Part Completion/.test(completions[0]) && /^Tripo3D · Tripo Quick Part Cap/.test(completions[1]), completions.join(' | '));
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-4-quick-cap.png`) });
    // Tripo Smart Segmentation is not a native segmentation task: no completion service accepts its result.
    await openOptions(page, segmentation);
    await page.getByRole('option').filter({ hasText: /Tripo Smart Segmentation/ }).click();
    await page.getByRole('option').first().waitFor({ state: 'detached', timeout: 5000 });
    await page.waitForTimeout(300);
    assert.equal(await panel.locator('[data-testid=processing-offering-mesh-completion]').count(), 0, 'completion still offers a choice after Smart Segmentation');
    return `${options.join(' | ')} → ${completions.join(' | ')}`;
  });

  await step('no page errors and no outside requests', async () => {
    assert.deepEqual(errors, []);
    assert.deepEqual(outside, []);
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
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), constants: { MESHY, HYPER3D_TEXTURE, TRIPO_TEXTURE, BANG_LOCAL, BANG_RODIN, TRIPO_SMART, TRIPO_SOURCE }, results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
console.log(`report: ${path.join(OUT, 'report.json')}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
