#!/usr/bin/env node
// Called only by the explicitly executed isolated acceptance runner. No API
// response mocking or store mutation: every action below clicks the actual UI.
import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const contextPath = process.argv[process.argv.indexOf('--context') + 1];
if (!contextPath || !path.isAbsolute(contextPath)) throw Error('absolute --context is required');
const input = JSON.parse(await fs.readFile(contextPath, 'utf8'));
const base = new URL(input.base_url);
if (base.hostname !== '127.0.0.1' || base.port === '13333' || !/^[a-f0-9]{40}$/.test(input.source_sha)) {
  throw Error('browser fixture requires a source-bound isolated loopback server');
}
const allowed = new Set(['processing_retry', 'processing_unknown_resume', 'processing_unreconciled', 'format_prepare_paused',
  'format_frozen_catalog_b', 'format_frozen_catalog_a', 'image_insufficient_balance']);
if (!allowed.has(input.stage)) throw Error('unsupported browser stage');
const ready = JSON.parse(await fs.readFile(path.join(path.dirname(contextPath), 'ready.json'), 'utf8'));
if (ready.source_sha !== input.source_sha || ready.address !== base.host
  || !['live', 'offline-mock'].includes(ready.acceptance_mode)) throw Error('running fixture receipt mismatch');
const output = path.join(path.dirname(contextPath), 'browser-evidence');
const evidenceStem = input.stage + '-' + new Date().toISOString().replace(/[:.]/g, '-');
await fs.mkdir(output, { recursive: true, mode: 0o700 });
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright');
const browserName = process.env.ACCEPTANCE_BROWSER || 'chromium';
if (!['chromium', 'webkit'].includes(browserName)) throw Error('unsupported browser');
const report = { stage: input.stage, source_sha: input.source_sha, base_url: base.origin,
  session_id: input.session_id, plan_id: input.plan_id, browser: browserName, acceptance_mode: ready.acceptance_mode,
  test_binary_sha256: ready.test_binary_sha256, frontend_sha256: ready.frontend_sha256, catalog_sha256: ready.catalog_sha256,
  browser_module: process.env.PLAYWRIGHT_MODULE_PATH || 'playwright', writes: [], errors: [], result: 'FAIL' };
let browser;
try {
  browser = await pw[browserName].launch({ headless: true, args: browserName === 'chromium' ? ['--enable-automation'] : [] });
  report.browser_version = browser.version();
  report.browser_source = 'Playwright managed browser';
  if (browserName === 'chromium') {
    const cdp = await browser.newBrowserCDPSession();
    const command = await cdp.send('Browser.getBrowserCommandLine');
    report.browser_executable = command.arguments[0];
    await cdp.detach();
  } else {
    report.driver_executable = pw[browserName].executablePath();
  }
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 }, serviceWorkers: 'block' });
  await context.route('**/*', async (route) => route.continue({ headers: { ...route.request().headers(), 'cache-control': 'no-cache' } }));
  const auth = await context.request.post(base.origin + '/api/auth/mock', { data: {} });
  if (!auth.ok()) throw Error('isolated mock identity login failed');
  const snapshot = async () => {
    const result = {};
    for (const [name, endpoint] of [['tasks', '/api/v1/tasks?limit=200&session_id=' + encodeURIComponent(input.session_id)],
      ['pending', '/api/v1/chat/sessions/' + encodeURIComponent(input.session_id) + '/pending-approvals']]) {
      const response = await context.request.get(base.origin + endpoint);
      if (!response.ok()) throw Error('snapshot ' + name + ' ' + response.status());
      result[name] = await response.json();
    }
    return result;
  };
  report.before = await snapshot();
  const page = await context.newPage();
  page.setDefaultTimeout(30000);
  page.on('pageerror', error => report.errors.push(error.message));
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.origin === base.origin && !['GET', 'HEAD'].includes(request.method())) {
      report.writes.push({ method: request.method(), path: url.pathname });
    }
  });
  await page.goto(base.origin + '/?session=' + encodeURIComponent(input.session_id), { waitUntil: 'domcontentloaded' });
  await page.getByTestId('workspace-task-center').waitFor();
  const schemaResponse = await context.request.get(base.origin + '/api/v1/schema');
  const schemaPayload = await schemaResponse.json();
  const schema = schemaPayload.schema;
  // Public projection supplies all labels, including balance failures and actions.
  const messages = schema.content?.messages || schema.application?.content?.messages;
  if (!messages) throw Error('public Schema messages missing');
  const label = key => { if (!messages[key]) throw Error('Schema label missing: ' + key); return messages[key]; };
  if (input.stage === 'image_insufficient_balance') {
    await page.getByTestId('workspace-task-center').click();
    const expected = label('failure.insufficient_balance');
    const card = page.getByTestId('workspace-task-card').filter({ hasText: expected });
    await card.first().waitFor();
    if (await card.count() !== 1) throw Error('balance fixture must identify one failed task card');
    report.balance_text = await card.innerText();
    report.retry_buttons = await card.getByTestId('workspace-task-retry').count();
    if (report.retry_buttons !== 0) throw Error('balance failure offers retry');
    await page.screenshot({ path: path.join(output, evidenceStem + '.png') });
    const detailResponsePromise = page.waitForResponse(response => response.request().method() === 'GET'
      && /^\/api\/v1\/tasks\/task_[a-f0-9]+$/.test(new URL(response.url()).pathname));
    await card.getByTestId('workspace-task-open').click();
    const detailResponse = await detailResponsePromise;
    const detail = await detailResponse.json();
    if (detail.id !== input.task_id || detail.failure?.class !== 'insufficient_balance') {
      throw Error('balance card detail does not identify the exact fixture Task');
    }
    report.balance_task_id = detail.id;
    await page.getByRole('dialog').waitFor();
    await page.screenshot({ path: path.join(output, evidenceStem + '-detail.png') });
    report.after = await snapshot();
    if (JSON.stringify(report.before) !== JSON.stringify(report.after)) throw Error('balance read-only UI changed Tasks or pending approvals');
  } else {
    const tile = page.locator('[data-testid="version-tile"]').filter({
      has: page.locator('button[aria-label^="查看"]'),
    });
    await tile.first().waitFor();
    const sourceTile = page.locator('[data-testid="version-tile"][data-key*="' + input.source_task_id + '"]');
    await sourceTile.first().waitFor({ state: 'attached' });
    if (await sourceTile.count() !== 1) {
      report.visible_tile_keys = await tile.evaluateAll(nodes => nodes.map(node => node.getAttribute('data-key')));
      throw Error('exact source version tile is not unique');
    }
    await sourceTile.locator('button[aria-label^="查看"]').click();
    const format = input.stage.startsWith('format_');
    await page.getByRole('tab', { name: format ? '格式' : '加工', exact: true }).click();
    const panel = format ? page.locator('[data-testid="model-formats"][data-layout="pinned"]')
      : page.getByTestId('model-processing-panel');
    await panel.waitFor();
    report.before_panel = await panel.innerText();
    const action = format ? panel.getByTestId('model-formats-resume')
      : panel.getByRole('button', { name: new RegExp('^' + label(input.stage === 'processing_retry'
        ? 'ui.workspace.processing.plan_retry' : input.stage === 'processing_unknown_resume'
          ? 'ui.workspace.processing.plan_reconcile' : 'ui.workspace.processing.plan_reconcile_action').replace(/[.*+?^${}()|[\]\\]/g, '\\$&')) });
    await action.waitFor();
    if (!await action.isEnabled()) throw Error('expected action is disabled');
    await page.screenshot({ path: path.join(output, evidenceStem + '-before.png') });
    const suffix = input.stage === 'processing_retry' ? '/retry' : input.stage === 'processing_unreconciled' ? '/reconcile' : '/resume';
    const responsePromise = page.waitForResponse(response => response.request().method() === 'POST'
      && new URL(response.url()).pathname.endsWith('/' + input.plan_id + suffix));
    await action.click();
    const response = await responsePromise;
    const sent = response.request().postDataJSON();
    report.action_request = sent;
    if (sent.expected_plan_version !== input.expected_plan_version
      || (input.stage !== 'processing_unreconciled' && sent.expected_offering_revision !== input.expected_offering_revision)) {
      throw Error('UI action did not send the expected Plan version and Offering revision');
    }
    report.action_response = { status: response.status(), body: await response.json() };
    if (response.status() !== input.expected_http_status) throw Error('unexpected action response ' + response.status());
    await page.waitForTimeout(700);
    report.after_panel = await panel.innerText();
    await page.screenshot({ path: path.join(output, evidenceStem + '-after.png') });
    report.after = await snapshot();
    if (input.stage === 'format_frozen_catalog_b') {
      if (JSON.stringify(report.before) !== JSON.stringify(report.after)) throw Error('Catalog B resume changed Tasks or pending approvals');
      if (report.action_response.body.code !== 'processing_reconcile_frozen_changed') {
        throw Error('Catalog B was rejected for a different reason');
      }
      const expectedError = label('ui.workspace.processing.plan_reconcile_frozen_changed');
      if (!await panel.getByRole('alert').filter({ hasText: expectedError }).count()) {
        throw Error('Catalog B frozen-cost rejection is not visible');
      }
    }
  }
  if (report.errors.length) throw Error('page raised a JavaScript error');
  if (input.stage === 'image_insufficient_balance' || input.stage === 'format_frozen_catalog_b') {
    const unexpected = report.writes.filter(write => /\/tasks(?:\/|$)|\/processing-plans(?:\/|$)/.test(write.path)
      && !(input.stage === 'format_frozen_catalog_b' && write.path.endsWith('/' + input.plan_id + '/resume')));
    if (unexpected.length) throw Error('read-only or rejected UI flow issued an unexpected execution write');
  }
  report.result = 'PASS';
} catch (error) {
  report.failure = String(error.message).slice(0, 1000);
  process.exitCode = 1;
} finally {
  await browser?.close();
  await fs.writeFile(path.join(output, evidenceStem + '.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  await fs.writeFile(path.join(output, input.stage + '.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  console.log(JSON.stringify({ stage: report.stage, result: report.result, failure: report.failure }));
}
