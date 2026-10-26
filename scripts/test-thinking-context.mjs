#!/usr/bin/env node
/**
 * Real-browser check for #322: the session thinking level, the context token count under the input
 * box, and manual / automatic context compaction.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid
 * calls); the default chat model `deepseek` gives the DeepSeek profile (low/high/max, default low,
 * 1M window), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18322 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18322 node scripts/test-thinking-context.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * OUT (screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH. Every request
 * outside BASE is refused and counted.
 *
 * The mock Agent reports usage like an upstream; "[mock:usage=N]" in a message makes it report N
 * prompt tokens, and "[mock:compact_fail]" in the summarized part makes the summary call fail.
 * Per engine and viewport, in a session that also holds a mock Hyper3D Rodin result:
 *  1. The thinking picker shows DeepSeek's own levels (low default, high, max) with the Schema hover
 *     line; choosing "high" persists across a reload and on another device.
 *  2. The token count reads "— / 1M" before any reply and "356K / 1M" after one reporting 355,9xx.
 *  3. Six rounds: an uploaded picture (@img1), notes, and an open choice card. Compacting keeps the
 *     chat history, shows the divider and resets the count to "— / 1M"; a second compaction has
 *     nothing to do.
 *  4. After the compaction the open choice card still runs the chosen service, the `@` list still
 *     offers @img1 with the same number, and the Agent's refine_image call uses that very picture.
 *  5. A reply over 80% of the window colors the count; the next turn compacts automatically first
 *     (auto divider) and its count drops.
 *  6. In another session a failing summary call shows the Schema failure text and changes nothing.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import zlib from 'node:zlib';

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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18322');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'thinking-context-'));
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const fill = (template, values) => Object.entries(values).reduce((result, [key, value]) => result.replaceAll(`{${key}}`, String(value)), template);
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const fields = application.chat.api.response_fields;
const compaction = application.chat.context_compaction;
const serverText = application.chat.presentation.messages;
const SOURCE_OFFERING = 'hyper3d/rodin-gen-2.5/text-to-3d';
const BANG_RODIN = 'hyper3d/bang/rodin-task';
const sessionRoute = (template, sessionID) => template.replace('{sessionID}', encodeURIComponent(sessionID));

/** The composer's token format (#322), written out independently of src/lib/chat/token-count.ts. */
function tokens(value) {
  if (value < 1000) return String(Math.round(value));
  const unit = (amount, suffix) => {
    const tenths = Math.round(amount * 10);
    if (tenths < 1000) return `${Math.floor(tenths / 10)}${tenths % 10 ? `.${tenths % 10}` : ''}${suffix}`;
    return `${Math.round(amount)}${suffix}`;
  };
  if (value < 1e6 && Math.round(value / 1000) < 1000) return unit(value / 1000, 'K');
  return unit(value / 1e6, 'M');
}

function png(width, height, seed) {
  const rows = [];
  for (let y = 0; y < height; y += 1) {
    const row = Buffer.alloc(1 + width * 3);
    for (let x = 0; x < width; x += 1) {
      row[1 + x * 3] = (x * 255 / width + seed * 40) & 255;
      row[2 + x * 3] = (y * 255 / height) & 255;
      row[3 + x * 3] = (seed * 90) & 255;
    }
    rows.push(row);
  }
  const chunk = (type, data) => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(zlib.crc32(body));
    return Buffer.concat([length, body, crc]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; header[9] = 2;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header), chunk('IDAT', zlib.deflateSync(Buffer.concat(rows))), chunk('IEND', Buffer.alloc(0)),
  ]);
}

// The whole matrix comes from one client address, so the server's per-minute API limit may
// answer 429; such a call is repeated after the window moves on.
async function api(context, method, route, data, headers = {}) {
  for (let attempt = 1; ; attempt += 1) {
    const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
    if (response.status() === 429 && attempt < 5) {
      await new Promise((resolve) => setTimeout(resolve, 15000));
      continue;
    }
    const body = await response.json().catch(() => ({}));
    return { status: response.status(), body };
  }
}

async function ok(context, method, route, data, headers) {
  const { status, body } = await api(context, method, route, data, headers);
  assert(status >= 200 && status < 300, `${method} ${route}: HTTP ${status} ${JSON.stringify(body).slice(0, 200)}`);
  return body;
}

async function login(context, user) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
}

/** A mock Rodin text-to-3d result in a fresh session, through the public prepare → grant → submit path. */
async function rodinSource(context, title) {
  const session = await ok(context, 'POST', legacy.chat_sessions, { title });
  const prepared = await ok(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'text-to-3d', offering_id: SOURCE_OFFERING,
    input: { prompt: 'A simple low-poly wooden stool', authorize_extra_credits: true } });
  const key = `thinking-context-${prepared.prepared_input_id}`;
  const grant = await ok(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await ok(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await ok(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  const deadline = Date.now() + 90000;
  for (;;) {
    const task = await ok(context, 'GET', routes.task_item.replace('{taskID}', accepted.task_id));
    if (task.state === 'SUCCEEDED') return { sessionID: session.id, taskID: accepted.task_id };
    assert(!['FAILED', 'CANCELED'].includes(task.state), `source task ${task.state}`);
    assert(Date.now() < deadline, 'source task did not finish');
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

const contextOf = async (context, sessionID) => ok(context, 'GET', sessionRoute(routes.chat_session_context, sessionID));

async function sessionTasks(context, sessionID) {
  const listed = await ok(context, 'GET', `${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=50`);
  return listed.tasks ?? listed.items ?? [];
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

function studio(page) {
  const box = page.locator('[data-testid=studio-composer] textarea');
  const meter = page.locator('[data-testid=composer-context-meter]');
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
  const send = page.locator('[data-testid=composer-send]');
  const say = async (words) => {
    // The whole matrix shares one client address, so the server's per-minute API limit can
    // answer 429; the message is then not sent and the turn is tried again after a pause.
    for (let attempt = 1; ; attempt += 1) {
      // A turn that just ended may still reset the box; type again until it holds the words.
      await waitFor(async () => {
        if ((await box.inputValue()) !== words) await box.fill(words);
        await page.waitForTimeout(100);
        return (await box.inputValue()) === words && await send.isEnabled();
      }, `the box holds "${words}" and can send`, 20000);
      const posted = page.waitForResponse((response) => response.request().method() === 'POST' &&
        /\/api\/chat\/sessions\/[^/]+\/messages$/.test(new URL(response.url()).pathname), { timeout: 15000 });
      await send.click();
      const status = (await posted).status();
      if (status === 429 && attempt < 4) {
        await idle();
        await page.waitForTimeout(15000);
        continue;
      }
      assert.equal(status, 202, `sending "${words}"`);
      break;
    }
    await page.waitForFunction(() => document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 5000 }).catch(() => undefined);
    await idle();
  };
  const meterText = async (expected) => {
    await page.waitForFunction(([selector, want]) => document.querySelector(selector)?.textContent?.trim() === want,
      ['[data-testid=composer-context-meter]', expected], { timeout: 15000 });
    return meter.innerText();
  };
  return { box, meter, idle, say, meterText };
}

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside);
  const user = `thinking-context-${engine}-${viewport}-${Date.now()}`;
  await login(context, user);
  const source = await rodinSource(context, `#322 ${engine} ${viewport}`);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(`${BASE}/?session=${encodeURIComponent(source.sessionID)}`);
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
  let current = page;
  const step = (name, fn) => record(engine, viewport, name, async () => {
    try {
      return await fn();
    } catch (error) {
      await current.screenshot({ path: path.join(OUT, `${engine}-${viewport}-failed-${results.length}.png`) }).catch(() => undefined);
      throw error;
    }
  });
  const { box, meter, say, meterText } = studio(page);
  const pickerShows = (locator, level, message) =>
    waitFor(async () => (await locator.innerText()).trim() === level, message, 20000)
      .catch(async () => assert.fail(`${message}: picker shows "${(await locator.innerText()).trim()}", want "${level}"`));
  const window = tokens(1000000);
  const unknown = text('ui.studio.context.unknown');
  const picker = page.locator('[data-testid=workspace-reasoning-picker]');

  await step('the thinking picker lists the model\'s own levels; high persists across a reload and another device', async () => {
    await picker.waitFor({ timeout: 20000 });
    await pickerShows(picker, 'low', 'default level');
    await picker.hover();
    await page.getByText(text('ui.studio.reasoning.hint'), { exact: true }).first().waitFor({ timeout: 5000 });
    await picker.click();
    const panel = page.locator('[data-testid=workspace-reasoning-picker-panel]');
    await panel.waitFor({ timeout: 5000 });
    const rows = await panel.locator('[role=option], [role=menuitemradio], button').filter({ hasText: /^(low|high|max)/ }).allInnerTexts();
    assert.deepEqual(rows.map((row) => row.split(/\s/)[0]), ['low', 'high', 'max'], `rows ${rows.join(' | ')}`);
    assert(rows[0].includes(text('ui.studio.reasoning.default_tag')), `default tag on low: ${rows[0]}`);
    await panel.getByText('high', { exact: true }).click();
    await pickerShows(picker, 'high', 'after choosing high');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-1-reasoning.png`) });
    const stored = await ok(context, 'GET', sessionRoute(routes.chat_session_reasoning, source.sessionID));
    assert.equal(stored[fields.reasoning_effort], 'high');
    assert.deepEqual(stored[fields.reasoning_levels], ['low', 'high', 'max']);
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await pickerShows(picker, 'high', 'after a reload');
    const { cookies } = await context.storageState();
    const other = await guardedContext(browser, { width, height }, outside);
    await other.addCookies(cookies);
    const second = await other.newPage();
    await second.goto(`${BASE}/?session=${encodeURIComponent(source.sessionID)}`);
    const otherPicker = second.locator('[data-testid=workspace-reasoning-picker]');
    await otherPicker.waitFor({ timeout: 30000 });
    await pickerShows(otherPicker, 'high', 'on another device');
    await other.close();
  });

  await step('the token count reads "— / 1M" before any reply', async () => {
    await meterText(`${unknown} / ${window}`);
    assert.equal(await meter.getAttribute('data-level'), 'normal');
  });

  let historyBefore = 0;
  await step('six rounds: an uploaded picture (@img1), notes and an open choice card; the count reads 356K / 1M', async () => {
    await page.locator('input[type=file][multiple]').first().setInputFiles({ name: 'cat.png', mimeType: 'image/png', buffer: png(96, 96, 2) });
    await page.locator('[data-testid=composer-attachment-chip]').first().waitFor({ timeout: 20000 });
    await waitFor(async () => await page.locator('[data-testid=composer-send]').isEnabled(), 'the upload finishes', 20000);
    await say('记住：参考这张猫图');
    for (const round of [2, 3, 4]) await say(`记住：风格是水彩 ${round}`);
    const tile = page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first();
    await tile.waitFor({ timeout: 20000 });
    await tile.click();
    await say('给当前模型做模型分割');
    await page.locator('[data-testid=chat-choice-card][data-state=pending]').waitFor({ timeout: 20000 });
    await say('记住：保持低多边形 [mock:usage=355900]');
    const reported = (await contextOf(context, source.sessionID))[fields.context_tokens];
    assert(reported >= 355900 && reported < 356500, `reported ${reported}`);
    await meterText(`356K / ${window}`);
    historyBefore = await page.locator('[data-testid=chat-message]').count();
    const uploaded = await page.locator('[data-testid=chat-message][data-role=user]').first().innerText();
    return `reported ${reported}; ${historyBefore} messages; first user message "${uploaded.slice(0, 30)}"`;
  });

  await step('compacting keeps the history, shows the divider and resets the count', async () => {
    const button = page.locator('[data-testid=composer-context-compact]');
    assert.equal((await button.innerText()).trim(), text('ui.studio.context.compact'));
    await button.click();
    const divider = page.locator('[data-testid=chat-context-compacted][data-trigger=manual]');
    await divider.waitFor({ timeout: 30000 });
    assert.equal((await divider.innerText()).trim(), fill(text('ui.chat.context_compacted'), { rounds: 1 }));
    await page.getByText(text('ui.studio.context.compacted')).first().waitFor({ timeout: 10000 });
    await meterText(`${unknown} / ${window}`);
    assert.equal(await page.locator('[data-testid=chat-message]').count(), historyBefore, 'chat history changed');
    const state = await contextOf(context, source.sessionID);
    assert.equal(state[fields.compactions].length, 1);
    assert.equal(state[fields.compactions][0][fields.compacted_rounds], 1);
    // Nothing more lies before the five kept rounds.
    const again = await api(context, 'POST', sessionRoute(routes.chat_session_context_compaction, source.sessionID));
    assert.equal(again.status, 422);
    assert.equal(again.body[fields.error], fill(serverText.compaction_nothing, { rounds: compaction.keep_recent_rounds }));
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-2-compacted.png`) });
  });

  await step('after the compaction the open choice card still runs the chosen service', async () => {
    const card = page.locator('[data-testid=chat-choice-card][data-state=pending]');
    await card.waitFor({ timeout: 10000 });
    const radios = card.getByRole('radio');
    const ids = await radios.evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-option-id')));
    await radios.nth(ids.indexOf(BANG_RODIN)).click();
    await card.locator('[data-testid=chat-choice-confirm]').click();
    await studio(page).idle();
    await page.locator('[data-testid=chat-choice-card][data-state=answered]').first().waitFor({ timeout: 20000 });
    const tasks = await waitFor(async () => {
      const listed = await sessionTasks(context, source.sessionID);
      return listed.some((task) => (task.offering_id ?? task.offering?.id) === BANG_RODIN) ? listed : null;
    }, 'the chosen service did not run after the compaction');
    return `tasks ${tasks.map((task) => task.offering_id ?? task.offering?.id).join(', ')}`;
  });

  await step('the @ list still offers @img1 and the Agent\'s refine_image call uses that picture', async () => {
    await page.click('[data-testid=workspace-model-picker]');
    await page.locator('[data-testid=workspace-model-picker-image] [role=menu] [role=menuitemradio]').first().click();
    await page.keyboard.press('Escape');
    await box.click();
    await box.fill('');
    await box.type('@img');
    const option = page.locator('[data-testid=composer-mention-option][data-handle="@img1"]');
    await option.waitFor({ timeout: 10000 });
    assert.equal((await option.locator('[data-testid=composer-mention-source]').innerText()).trim(), text('ui.studio.composer.mention.source_history'));
    await option.click();
    assert((await box.inputValue()).startsWith('@img1'), `composer text ${await box.inputValue()}`);
    await box.press('End');
    await box.type(' 改成蓝色');
    const before = (await sessionTasks(context, source.sessionID)).map((task) => task.id);
    await page.locator('[data-testid=composer-send]').click();
    await studio(page).idle();
    const created = await waitFor(async () => {
      const listed = await sessionTasks(context, source.sessionID);
      return listed.find((task) => !before.includes(task.id) && task.capability !== 'mesh-segmentation') ?? null;
    }, 'no refine task after the mention');
    const messages = await ok(context, 'GET', `${sessionRoute(legacy.chat_messages, source.sessionID)}`);
    const list = messages[fields.messages] ?? messages;
    const firstUser = list.find((message) => message[fields.role] === 'user');
    const upload = (firstUser[fields.attachments] ?? []).find((attachment) => attachment.handle === '@img1');
    assert(upload, `the uploaded picture lost its handle: ${JSON.stringify(firstUser[fields.attachments])}`);
    // The session lineage records which asset the new task read: it must be the @img1 upload.
    const graph = await ok(context, 'GET', sessionRoute(routes.chat_session_lineage, source.sessionID));
    const taskNode = graph.nodes.find((node) => node.task_id === created.id);
    assert(taskNode, `the refine task is not in the lineage: ${created.id}`);
    const inputs = graph.edges.filter((edge) => edge.to === taskNode.id).map((edge) => edge.asset_id);
    assert(upload.assetId && inputs.includes(upload.assetId), `refine task inputs ${inputs.join(', ')} lack @img1 (${upload.assetId})`);
    return `task ${created.id} (${created.capability}) reads ${inputs.join(', ')} = @img1`;
  });

  await step('over 80% of the window the count turns critical; the next turn compacts automatically first', async () => {
    await say('记住：继续 [mock:usage=950000]');
    await meterText(`950K / ${window}`);
    assert.equal(await meter.getAttribute('data-level'), 'critical');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-3-critical.png`) });
    await say('记住：再来一轮');
    const divider = page.locator('[data-testid=chat-context-compacted][data-trigger=auto]');
    await divider.waitFor({ timeout: 30000 });
    const state = await contextOf(context, source.sessionID);
    const auto = state[fields.compactions].at(-1);
    assert.equal(auto[fields.compaction_trigger], 'auto');
    assert.equal((await divider.innerText()).trim(), fill(text('ui.chat.context_compacted_auto'), { rounds: auto[fields.compacted_rounds] }));
    const after = state[fields.context_tokens];
    assert(after !== null && after < 950000 * compaction.auto_threshold_ratio, `count after the auto compaction ${after}`);
    await meterText(`${tokens(after)} / ${window}`);
    assert.equal(await meter.getAttribute('data-level'), 'normal');
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-4-auto.png`) });
    return `auto compaction covers ${auto[fields.compacted_rounds]} rounds; count now ${after}`;
  });

  await step('the count, the level and both dividers survive a reload', async () => {
    const before = await meter.innerText();
    await page.reload();
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
    await meterText(before.trim());
    await waitFor(async () => (await page.locator('[data-testid=chat-context-compacted]').count()) === 2, 'dividers after a reload');
    await pickerShows(picker, 'high', 'after the last reload');
  });

  // The failure runs on a fresh page: navigating the busy page away would abort its pending asset loads.
  const overflow = await page.evaluate(() => {
    const grid = document.querySelector('[data-testid=composer-pickers]');
    return grid ? grid.scrollWidth - grid.clientWidth : -1;
  });
  await page.close();
  const failurePage = await context.newPage();
  failurePage.on('pageerror', (error) => errors.push(error.message));
  current = failurePage;
  await step('a failing summary call shows the Schema text and leaves the session unchanged', async () => {
    const page = failurePage;
    const { meter, say, meterText } = studio(page);
    const session = await ok(context, 'POST', legacy.chat_sessions, { title: `#322 failure ${engine} ${viewport}` });
    // The matrix shares one client address; a load answered 429 by the per-minute API limit
    // leaves the session unopened, so the page is loaded again after a pause.
    for (let attempt = 1; ; attempt += 1) {
      await page.goto(`${BASE}/?session=${encodeURIComponent(session.id)}`);
      await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
      const levelShown = page.locator('[data-testid=workspace-reasoning-picker]');
      const opened = await waitFor(async () => (await levelShown.innerText().catch(() => '')).trim() === 'low', 'level', 15000)
        .then(() => true, () => false);
      if (opened) break;
      assert(attempt < 4, 'the failure session never opened');
      await page.waitForTimeout(20000);
    }
    await say('记住：这一轮会让压缩失败 [mock:compact_fail]');
    for (const round of [2, 3, 4, 5]) await say(`记住：设定 ${round}`);
    await say('记住：设定 6 [mock:usage=123456]');
    await meterText(`${tokens((await contextOf(context, session.id))[fields.context_tokens])} / ${window}`);
    const shown = await meter.innerText();
    await page.locator('[data-testid=composer-context-compact]').click();
    await page.getByText(serverText.compaction_failed).first().waitFor({ timeout: 30000 });
    assert.equal(await page.locator('[data-testid=chat-context-compacted]').count(), 0, 'a divider after a failed compaction');
    assert.equal((await meter.innerText()).trim(), shown.trim(), 'the count changed after a failed compaction');
    assert.equal((await contextOf(context, session.id))[fields.compactions].length, 0);
    await page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-5-failed.png`) });
  });

  await step('no page errors, no outside requests, no horizontal overflow in the composer', async () => {
    assert.equal(overflow, 0, `composer pickers overflow by ${overflow}px`);
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
