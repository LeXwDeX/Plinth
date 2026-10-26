#!/usr/bin/env node
/**
 * Real-browser check for #322 Scopes 2 and 5: the administrator's LLM settings and the session chat model.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no real model, no paid call),
 * two chat connections from the environment, and an allow-listed administrator e-mail, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   APP_ADDR=127.0.0.1:18324 WEB_ASSETS_ROOT=build/go-web DATABASE_PATH=<scratch>/a.db \
 *   STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs ADMIN_ALLOWLIST_EMAILS=llm-admin@example.test \
 *   LLM_BASE_URL=http://127.0.0.1:9/v1 LLM_API_KEY=sk-playwright-default-secret \
 *   LLM_CONNECTIONS=GLM GLM_BASE_URL=http://127.0.0.1:9/v1 GLM_API_KEY=sk-playwright-glm-secret \
 *   GLM_MODEL=glm-5.3-text-only GLM_CONTEXT_WINDOW_TOKENS=200000 go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18324 node scripts/test-llm-settings.mjs
 * The default connection keeps LLM_MODEL's Schema default `deepseek` (DeepSeek profile: low/high/max,
 * default low, 1M window); GLM is a GLM profile model (low/high/max) whose name makes the mock Agent
 * refuse pictures, so the connection test reports "no image input".
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"), OUT
 * (screenshots and report.json; default a temp folder), PLAYWRIGHT_MODULE_PATH, ADMIN_EMAIL, SECRETS
 * (comma separated values that must never reach the browser; defaults to the two keys and the address
 * above). Every request outside BASE is refused and counted.
 *
 * Per engine and viewport:
 *  1. The administrator resets the settings, sees both connections (variables shown as set, never their
 *     values), tests each (DeepSeek reads images, GLM does not), offers GLM only high and max with high
 *     as its default level, and publishes; the stored version says so.
 *  2. A person opens a session: the chat model picker offers both models; picking GLM switches the
 *     thinking levels to high/max (high) and the window to 200K; a reload keeps all three.
 *  3. The administrator takes GLM offline; after a reload the session falls back to deepseek with the
 *     Schema notice, the picker is gone (one model left), the level and the window follow deepseek.
 *  4. No page, network response or console message carries a key or a connection address; no page
 *     errors, no outside requests, no horizontal overflow in the composer.
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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18324');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'llm-settings-'));
const ADMIN_EMAIL = process.env.ADMIN_EMAIL || 'llm-admin@example.test';
const SECRETS = (process.env.SECRETS || 'sk-playwright-default-secret,sk-playwright-glm-secret,http://127.0.0.1:9/v1')
  .split(',').map((item) => item.trim()).filter(Boolean);
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const text = (key) => {
  const value = application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
const ui = (key) => {
  const value = application.llm_settings.ui[key];
  assert(value, `missing llm_settings.ui.${key}`);
  return value;
};
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const fields = application.chat.api.response_fields;
const admin = application.llm_settings.routes;
const sessionRoute = (template, sessionID) => template.replace('{sessionID}', encodeURIComponent(sessionID));
const GLM_MODEL = 'glm-5.3-text-only';

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

async function login(context, user, email) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email } });
  assert.equal(response.status(), 200, 'mock login');
}

async function waitFor(fn, message, timeout = 20000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await fn();
    if (value) return value;
    assert(Date.now() < deadline, message);
    await new Promise((resolve) => setTimeout(resolve, 300));
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

/** A browser context that refuses outside requests and records every same-origin text body and console line. */
async function watchedContext(browser, viewport, watch) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block' });
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    watch.outside.push(url);
    return route.abort();
  });
  context.on('response', (response) => {
    const type = response.headers()['content-type'] || '';
    if (!response.url().startsWith(BASE) || !/json|html|javascript|text/.test(type)) return;
    watch.pending.push(response.text().then((body) => { watch.bodies.push({ url: response.url(), body }); }).catch(() => undefined));
  });
  context.on('page', (page) => {
    page.on('console', (message) => watch.console.push(message.text()));
    page.on('pageerror', (error) => watch.errors.push(error.message));
  });
  return context;
}

const shows = (locator, value, message) => waitFor(async () => (await locator.innerText().catch(() => '')).trim() === value, message)
  .catch(async () => assert.fail(`${message}: shows "${(await locator.innerText().catch(() => '')).trim()}", want "${value}"`));

async function checks(engine, browser, [width, height]) {
  const viewport = `${width}x${height}`;
  const watch = { outside: [], bodies: [], pending: [], console: [], errors: [] };
  const adminContext = await watchedContext(browser, { width, height }, watch);
  const userContext = await watchedContext(browser, { width, height }, watch);
  await login(adminContext, `llm-admin-${engine}-${viewport}`, ADMIN_EMAIL);
  await login(userContext, `llm-user-${engine}-${viewport}-${Date.now()}`, `llm-user-${engine}-${viewport}-${Date.now()}@example.test`);
  const adminPage = await adminContext.newPage();
  const userPage = await userContext.newPage();
  let current = adminPage;
  const step = (name, fn) => record(engine, viewport, name, async () => {
    try {
      return await fn();
    } catch (error) {
      await current.screenshot({ path: path.join(OUT, `${engine}-${viewport}-failed-${results.length}.png`) }).catch(() => undefined);
      throw error;
    }
  });
  const openAdmin = async () => {
    await adminPage.goto(`${BASE}/admin/llm`);
    await adminPage.locator('[data-testid=llm-settings-admin] [data-testid=llm-connection-GLM]').waitFor({ timeout: 30000 });
  };
  const publish = async () => {
    await adminPage.locator('[data-testid=llm-publish]').click();
    await shows(adminPage.locator('[data-testid=llm-settings-notice]'), ui('published'), 'publish notice');
  };

  await step('the administrator tests both connections, narrows GLM to high/max (default high) and publishes', async () => {
    current = adminPage;
    const before = await ok(adminContext, 'GET', admin.admin);
    await ok(adminContext, 'POST', admin.reset, { expected_revision: before.revision });
    await openAdmin();
    const glm = adminPage.locator('[data-testid=llm-connection-GLM]');
    await shows(adminPage.locator('[data-testid=llm-configured-LLM]'), `${ui('configured')} · ${ui('context_window')}: 1M`, 'LLM configured');
    await shows(adminPage.locator('[data-testid=llm-configured-GLM]'), `${ui('configured')} · ${ui('context_window')}: 200K`, 'GLM configured');
    for (const name of ['GLM_BASE_URL', 'GLM_API_KEY', 'GLM_MODEL']) {
      assert((await glm.innerText()).includes(name), `GLM lists ${name}`);
    }
    await adminPage.locator('[data-testid=llm-test-GLM]').click();
    await waitFor(async () => (await adminPage.locator('[data-testid=llm-check-GLM]').innerText()).includes(ui('vision_unsupported')), 'GLM test result');
    assert((await adminPage.locator('[data-testid=llm-check-GLM]').innerText()).includes(ui('reachable')));
    await adminPage.locator('[data-testid=llm-test-LLM]').click();
    await waitFor(async () => (await adminPage.locator('[data-testid=llm-check-LLM]').innerText()).includes(ui('vision_supported')), 'LLM test result');
    await adminPage.locator('#llm-level-GLM-low').uncheck();
    await adminPage.locator('#llm-default-effort-GLM').click();
    await adminPage.getByRole('option', { name: 'high', exact: true }).click();
    await waitFor(async () => (await adminPage.locator('#llm-default-effort-GLM').innerText()).includes('high'), 'GLM default level high');
    await adminPage.screenshot({ path: path.join(OUT, `${engine}-${viewport}-1-admin.png`), fullPage: true });
    await publish();
    const stored = await ok(adminContext, 'GET', admin.admin);
    assert.deepEqual(stored.settings.models.GLM, { enabled: true, effort_levels: ['high', 'max'], default_effort: 'high' });
    assert.equal(stored.settings.default_model, 'LLM');
    assert.equal(stored.history[0].source, 'publish');
    return `revision ${stored.revision}`;
  });

  let sessionID = '';
  const chatPicker = userPage.locator('[data-testid=workspace-chat-model-picker]');
  const reasoningPicker = userPage.locator('[data-testid=workspace-reasoning-picker]');
  const meter = userPage.locator('[data-testid=composer-context-meter]');
  const openSession = async () => {
    await userPage.goto(`${BASE}/?session=${encodeURIComponent(sessionID)}`);
    await userPage.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
  };
  const meterShows = (window, message) => waitFor(async () => (await meter.innerText().catch(() => '')).trim().endsWith(`/ ${window}`), message)
    .catch(async () => assert.fail(`${message}: meter shows "${(await meter.innerText().catch(() => '')).trim()}"`));

  await step('a person picks GLM: levels become high/max (high), the window 200K, and a reload keeps them', async () => {
    current = userPage;
    sessionID = (await ok(userContext, 'POST', legacy.chat_sessions, { title: `#322 B ${engine} ${viewport}` })).id;
    await openSession();
    await shows(chatPicker, 'deepseek', 'the default model');
    await shows(reasoningPicker, 'low', 'DeepSeek default level');
    await meterShows('1M', 'DeepSeek window');
    await chatPicker.click();
    const panel = userPage.locator('[data-testid=workspace-chat-model-picker-panel]');
    await panel.waitFor({ timeout: 5000 });
    const rows = await panel.locator('button').filter({ hasText: /deepseek|glm/ }).allInnerTexts();
    assert.equal(rows.length, 2, `rows ${rows.join(' | ')}`);
    assert(rows[0].startsWith('deepseek') && rows[0].includes(text('ui.studio.chat_model.default_tag')), `default tag: ${rows[0]}`);
    await panel.getByText(GLM_MODEL, { exact: true }).click();
    await shows(chatPicker, GLM_MODEL, 'after picking GLM');
    await shows(reasoningPicker, 'high', 'GLM default level');
    await meterShows('200K', 'GLM window');
    await reasoningPicker.click();
    const levels = userPage.locator('[data-testid=workspace-reasoning-picker-panel]');
    await levels.waitFor({ timeout: 5000 });
    const offered = await levels.locator('button').filter({ hasText: /^(low|high|max)/ }).allInnerTexts();
    assert.deepEqual(offered.map((row) => row.split(/\s/)[0]), ['high', 'max'], `levels ${offered.join(' | ')}`);
    await userPage.keyboard.press('Escape');
    await userPage.screenshot({ path: path.join(OUT, `${engine}-${viewport}-2-glm.png`) });
    await openSession();
    await shows(chatPicker, GLM_MODEL, 'GLM after a reload');
    await shows(reasoningPicker, 'high', 'level after a reload');
    await meterShows('200K', 'window after a reload');
    const stored = await ok(userContext, 'GET', sessionRoute(routes.chat_session_model, sessionID));
    assert.equal(stored[fields.chat_model], 'GLM');
    assert.equal(stored[fields.chat_model_chosen], 'GLM');
  });

  await step('the administrator takes GLM offline: the session falls back to deepseek with the Schema notice', async () => {
    current = adminPage;
    await openAdmin();
    await adminPage.locator('#llm-enabled-GLM').uncheck();
    await publish();
    current = userPage;
    await openSession();
    const notice = userPage.locator('[data-testid=workspace-chat-model-notice]');
    await shows(notice, text('ui.studio.chat_model.fallback').replaceAll('{model}', 'deepseek'), 'fallback notice');
    assert.equal(await chatPicker.count(), 0, 'the picker shows with one model left');
    await shows(reasoningPicker, 'low', 'DeepSeek level after the fallback');
    await meterShows('1M', 'DeepSeek window after the fallback');
    const stored = await ok(userContext, 'GET', sessionRoute(routes.chat_session_model, sessionID));
    assert.equal(stored[fields.chat_model], 'LLM');
    assert.equal(stored[fields.chat_model_chosen], 'GLM');
    assert.equal(stored[fields.chat_model_fallback], true);
    await userPage.screenshot({ path: path.join(OUT, `${engine}-${viewport}-3-fallback.png`) });
  });

  await step('no key or connection address in pages, responses or console; no errors, outside requests or overflow', async () => {
    const overflow = await userPage.evaluate(() => {
      const pickers = document.querySelector('[data-testid=composer-pickers]');
      return pickers ? pickers.scrollWidth - pickers.clientWidth : 0;
    });
    await Promise.all(watch.pending);
    const pages = [await adminPage.content(), await userPage.content()];
    for (const secret of SECRETS) {
      const leaked = [...watch.bodies.filter((item) => item.body.includes(secret)).map((item) => item.url),
        ...watch.console.filter((line) => line.includes(secret)), ...pages.filter((html) => html.includes(secret)).map(() => 'page')];
      assert.equal(leaked.length, 0, `"${secret.slice(0, 12)}…" reached: ${leaked.join(' | ')}`);
    }
    assert(watch.bodies.some((item) => item.url.includes(admin.admin)), 'no management response was inspected');
    assert.equal(overflow, 0, `composer pickers overflow by ${overflow}px`);
    assert.equal(watch.errors.length, 0, `page errors: ${watch.errors.join(' | ')}`);
    assert.equal(watch.outside.length, 0, `outside requests: ${watch.outside.join(' | ')}`);
    return `${watch.bodies.length} responses and ${watch.console.length} console lines inspected`;
  });
  await adminContext.close();
  await userContext.close();
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
