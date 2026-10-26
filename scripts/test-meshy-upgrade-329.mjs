#!/usr/bin/env node
/**
 * Real-browser checks for #329 (Meshy upgraded to the documented latest models). No credentials and no paid
 * upstream: a local server with mock providers and the mock Agent, e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18429 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then  BASE=http://127.0.0.1:18429 OUT=<scratch>/out node scripts/test-meshy-upgrade-329.mjs
 * Per engine and viewport (a fresh person each time):
 *  A. the 3D model picker lists Meshy 7.1 and Meshy T2.5 Smart Topology, and no retired Meshy model;
 *  B. a local pick of the retired Meshy 7 opens on Meshy 7.1 with the Schema notice; picking another model
 *     clears the notice;
 *  C. a session whose turn used Meshy 7, opened with no local pick, restores Meshy 7.1 with the notice;
 *  D. generating with Meshy 7.1 and Meshy T2.5 (public prepare → grant → submit) succeeds, and the chat with
 *     Meshy T2.5 picked runs a T2.5 task.
 * #329 follow-ups (same run):
 *  E. with Meshy 7.1 picked, two attached pictures stay attached and the composer says, in the Schema's words, that
 *     only the first is used; one picture clears it; the chat's approval card shows the uploaded picture's thumbnail
 *     (not the placeholder) and the approved task froze one picture;
 *     (dev-test follow-up E) the Agent's reply that brings the card, the card itself and the message that reports the
 *     finished model all say, in the Schema's reference-limit text, that only the first of the two pictures is used;
 *  F. the task API (no Agent) keeps the first of two pictures for Meshy 7.1 image to 3D and answers the Schema note;
 *     a request naming the retired Meshy 7 Offering is refused as invalid_input with the Schema reason;
 *  G. (note 99868/99888) the task API (no Agent) refuses five pictures for Meshy 7.1 Multiview (its max is four)
 *     as a localized user_facing invalid_input that names the public label and both counts, and refuses a Rodin
 *     Gen-2.5 picture request without extra-credit consent with the localized consent reason; neither creates a
 *     Task or pending approval and neither is submitted or approved here;
 *  H. (note 100006) with Meshy 7.1 Multiview picked and five uploaded pictures attached, the mock Agent's chat turn
 *     keeps all five attached and shows the Schema refusal in the turn's frozen locale (preserve means no silent
 *     cut), with no Task, no pending approval and no internal parameter or asset ID;
 *  (dev-test note 99647) every check runs in Chinese and in English (the UI locale cookie): the composer, the task
 *     API, the approval card, the pending-approval read and the finished message all carry the one Schema text
 *     (runtime.generation_messages.reference_limit_one for a model that takes one picture), in the reader's
 *     language, worded for one picture ("第 1 张", "only the first is used", never "picture(s)"); the finished
 *     message names the model once;
 *  C. a turn sent with ai_model=meshy-7 is recorded as Meshy 7.1 on the server, the session read names the retired
 *     model, and the workspace still shows the notice.
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"), LOCALES (default "zh-CN,en"),
 * PLAYWRIGHT_MODULE_PATH. Every browser request outside BASE is refused and counted.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { deflateSync } from 'node:zlib';
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
  console.error('BASE must be a loopback server, e.g. http://127.0.0.1:18429');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const LOCALES = (process.env.LOCALES || 'zh-CN,en').split(',').map((item) => item.trim()).filter(Boolean);
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'meshy-329-'));
mkdirSync(OUT, { recursive: true });

const { application, offerings: catalogOfferings } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const DEFAULT_LOCALE = application.localization.default_locale;
const LOCALE_COOKIE = application.localization.preference.cookie;
const bundle = (locale) => (locale === DEFAULT_LOCALE ? {} : application.localization.bundles[locale]);
/** A content message in the locale (the Schema's own text for the default locale). */
const text = (key, locale = DEFAULT_LOCALE) => {
  const value = bundle(locale)?.messages?.[key] ?? application.content.messages[key];
  assert(value, `missing Schema text ${key}`);
  return value;
};
/** A Schema field by its localization path, in the locale. */
const field = (pathName, locale = DEFAULT_LOCALE) => {
  const value = bundle(locale)?.fields?.[pathName] ?? pathName.split('.').reduce((node, key) => node?.[key], application);
  assert(typeof value === 'string' && value, `missing Schema field ${pathName}`);
  return value;
};
const fill = (template, values) => template.replace(/\{(\w+)\}/g, (_, name) => String(values[name]));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const offering = (id) => {
  const found = catalogOfferings.find((item) => item.id === id);
  assert(found, `offering ${id} is not in the Catalog`);
  return found;
};
const RETIRED = application.runtime.frontend.retired_generation_models.find((item) => item.provider_id === 'meshy' && item.model_id === 'meshy-7');
assert(RETIRED, 'the Schema lists no retired Meshy 7');
const LATEST = offering('meshy/meshy-7.1/text-to-3d');
const T25 = offering('meshy/meshy-t2.5/text-to-3d');
/** An Offering text (a model name) as the locale shows it. */
const nameIn = (name, locale) => bundle(locale)?.offering_texts?.[name] ?? name;
const noticeIn = (locale) => fill(text('ui.studio.picker.model_retired', locale), { from: nameIn(RETIRED.model_name, locale), to: nameIn(LATEST.model_name, locale) });
const PREFS_KEY = 'art-ai-workspace-prefs';
// "Meshy 7" not followed by ".1" (or any other version suffix).
const RETIRED_NAME = new RegExp(`${RETIRED.model_name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?![.\\d])`);

async function api(context, method, route, data, headers = {}) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE, ...headers } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return body;
}

async function login(context, user) {
  // Same-origin like the page: the context already carries the locale cookie.
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { headers: { Origin: BASE }, data: { name: user, email: `${user}@example.test` } });
  assert.equal(response.status(), 200, 'mock login');
}

async function submit(context, sessionID, capability, offeringID, input) {
  const prepared = await api(context, 'POST', routes.task_prepare, { session_id: sessionID, capability, offering_id: offeringID, input });
  const key = `meshy329-${prepared.prepared_input_id}`;
  const grant = await api(context, 'POST', routes.task_grants, { prepared_input_id: prepared.prepared_input_id }, { 'Idempotency-Key': key });
  if (grant.status === 'pending') await api(context, 'PUT', routes.task_grant_decision.replace('{requestID}', grant.request_id), { decision: 'approve' });
  const accepted = await api(context, 'POST', routes.tasks, { prepared_input_id: prepared.prepared_input_id, authorization_request_id: grant.request_id }, { 'Idempotency-Key': key });
  return accepted.task_id;
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

async function assetUploads(context, buffers) {
  const ids = [];
  for (const [index, buffer] of buffers.entries()) {
    const uploaded = await context.request.post(`${BASE}${routes.assets}`, {
      headers: { Origin: BASE },
      multipart: { file: { name: `refusal-${index + 1}.png`, mimeType: 'image/png', buffer } },
    });
    const body = await uploaded.json();
    assert(uploaded.ok(), `upload ${uploaded.status()} ${JSON.stringify(body)}`);
    ids.push(body.id);
  }
  return ids;
}

async function prepareRefusal(context, payload) {
  const response = await context.request.fetch(`${BASE}${routes.task_prepare}`, {
    method: 'POST', headers: { Origin: BASE }, data: payload,
  });
  return { status: response.status(), body: await response.json().catch(() => ({})) };
}

async function pendingApprovals(context, sessionID) {
  const listed = await api(context, 'GET', routes.chat_session_pending_approvals.replace('{sessionID}', encodeURIComponent(sessionID)));
  return listed.items ?? [];
}

async function chatMessages(context, sessionID) {
  const body = await api(context, 'GET', legacy.chat_messages.replace('{sessionID}', encodeURIComponent(sessionID)));
  return Array.isArray(body) ? body : (body.messages ?? body.items ?? []);
}

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

/** A solid-colour 64×64 PNG, so two pictures differ by their bytes. */
function png([red, green, blue]) {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (bytes) => {
    let c = 0xffffffff;
    for (const byte of bytes) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
    const out = Buffer.alloc(body.length + 8);
    out.writeUInt32BE(data.length, 0);
    body.copy(out, 4);
    out.writeUInt32BE(crc(body), body.length + 4);
    return out;
  };
  const size = 64;
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header[8] = 8; header[9] = 2;
  const rows = Buffer.alloc((size * 3 + 1) * size);
  for (let y = 0; y < size; y += 1) {
    for (let x = 0; x < size; x += 1) rows.set([red, green, blue], y * (size * 3 + 1) + 1 + x * 3);
  }
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk('IHDR', header), chunk('IDAT', deflateSync(rows)), chunk('IEND', Buffer.alloc(0))]);
}
const PICTURES = [png([200, 60, 40]), png([40, 90, 210])];
const FIVE_PICTURES = [
  png([200, 60, 40]), png([40, 90, 210]), png([30, 160, 80]), png([180, 160, 30]), png([110, 40, 170]),
];
const LATEST_IMAGE = offering('meshy/meshy-7.1/image-to-3d');
const MULTIVIEW = offering('meshy/meshy-7.1-multiview/image-to-3d');
const RODIN = offering('hyper3d/rodin-gen-2.5/image-to-3d');
const MULTIVIEW_REFERENCE = MULTIVIEW.parameters.find((wire) => wire.semantic === 'reference-image' && wire.type === 'asset-array');
assert(MULTIVIEW_REFERENCE && MULTIVIEW_REFERENCE.max_items === 4, `Multiview reference max is ${MULTIVIEW_REFERENCE?.max_items}, not the fixed four of this check`);
/** The one refusal text a preserve-policy model answers with, filled from the Schema. */
const referenceRefusalIn = (locale, actual = 5) => fill(field('runtime.generation_messages.reference_limit_exceeded', locale), {
  label: nameIn(MULTIVIEW_REFERENCE.label, locale), max: MULTIVIEW_REFERENCE.max_items, actual,
});
const IMAGE_BASE = application.chat.context.image_index_base;
// #329 follow-up E and note 99647: the one Schema text the composer, the task API, the card and the finished result
// carry (the one-picture form: Meshy 7.1 image to 3D takes one picture), in the reader's language.
const pictureNoteIn = (locale) => fill(field('runtime.generation_messages.reference_limit_one', locale), { model: nameIn(LATEST_IMAGE.model_name, locale), count: 1, total: 2 });
const finishedNoteIn = (locale) => fill(field('chat.pending_approvals.messages.succeeded_with_note', locale), { note: pictureNoteIn(locale) });
// The turn's locale is frozen; the mock Agent repeats the Schema text for that locale in the reply that brings the card.
for (const locale of LOCALES) {
  const note = pictureNoteIn(locale);
  assert(!note.includes('(s)') && !/first 1 |前 1 张/.test(note), `${locale} note is not worded for one picture: ${note}`);
  const finished = finishedNoteIn(locale);
  assert.equal(finished.split(nameIn(LATEST_IMAGE.model_name, locale)).length - 1, 1, `${locale} finished message names the model more than once: ${finished}`);
}

async function assistantContents(context, sessionID) {
  const list = await chatMessages(context, sessionID);
  return list.filter((message) => message.role === 'assistant').map((message) => String(message.content ?? ''));
}

const results = [];
async function record(engine, viewport, locale, name, fn) {
  const started = Date.now();
  try {
    const note = await fn();
    results.push({ engine, viewport, locale, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
  } catch (error) {
    results.push({ engine, viewport, locale, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
  }
}

async function guardedContext(browser, viewport, outside, locale) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block' });
  await context.addCookies([{ name: LOCALE_COOKIE, value: locale, url: BASE }]);
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

/** The model names of the 3D section of the open picker. */
async function modelRows(page) {
  const section = page.locator('[data-testid=workspace-model-picker-model]');
  await section.locator('button[role=menuitemradio]').first().waitFor({ timeout: 20000 });
  return section.locator('button[role=menuitemradio]').evaluateAll((rows) => rows.map((row) => {
    const first = row.firstElementChild;
    return ((first?.firstElementChild ?? first)?.textContent ?? '').trim();
  }));
}

async function openPicker(page) {
  await page.click('[data-testid=workspace-model-picker]');
  await page.locator('[data-testid=workspace-model-picker-panel]').waitFor({ timeout: 10000 });
}

async function closePicker(page) {
  await page.keyboard.press('Escape');
  await page.locator('[data-testid=workspace-model-picker-panel]').waitFor({ state: 'hidden', timeout: 5000 }).catch(() => {});
}

const trigger = (page) => page.locator('[data-testid=workspace-model-picker]');
const notice = (page) => page.locator('[data-testid=workspace-model-retired-notice]');

async function checks(engine, browser, [width, height], locale) {
  const viewport = `${width}x${height}`;
  const outside = [];
  const user = `meshy329-${engine}-${viewport}-${locale}-${Date.now()}`;
  const NOTICE = noticeIn(locale);
  const LATEST_NAME = nameIn(LATEST.model_name, locale);
  const T25_NAME = nameIn(T25.model_name, locale);
  const PICTURE_NOTE = pictureNoteIn(locale);
  const LIMIT_NOTICE = PICTURE_NOTE;
  const MULTIVIEW_NAME = nameIn(MULTIVIEW.model_name, locale);
  const REFERENCE_REFUSAL = referenceRefusalIn(locale);
  const FINISHED_NOTE = finishedNoteIn(locale);
  const context = await guardedContext(browser, { width, height }, outside, locale);
  await login(context, user);
  // B: the person had picked Meshy 7 on this device before it was retired.
  await context.addInitScript(([key, model]) => {
    try {
      if (!sessionStorage.getItem('meshy329-seeded')) {
        localStorage.setItem(key, JSON.stringify({ state: { modelByDomain: { image: null, 'model-3d': model } }, version: 2 }));
        sessionStorage.setItem('meshy329-seeded', '1');
      }
    } catch { /* storage unavailable */ }
  }, [PREFS_KEY, RETIRED.model_id]);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const step = (name, fn) => record(engine, viewport, locale, name, fn);
  const shot = (target, name) => target.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${locale}-${name}.png`) });
  const session = await api(context, 'POST', legacy.chat_sessions, { title: `#329 ${engine} ${viewport} ${locale}` });

  await step('B: a local pick of Meshy 7 opens on Meshy 7.1 with the Schema notice', async () => {
    await openStudio(page, session.id);
    await notice(page).first().waitFor({ timeout: 20000 });
    const said = (await notice(page).first().innerText()).trim();
    assert.equal(said, NOTICE);
    const shown = await trigger(page).innerText();
    assert(shown.includes(LATEST_NAME) && !RETIRED_NAME.test(shown), `trigger "${shown}"`);
    const stored = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || '{}'), PREFS_KEY);
    assert.equal(stored?.state?.modelByDomain?.['model-3d'], RETIRED.successor_model_id, `stored ${JSON.stringify(stored)}`);
    await shot(page, 'B-notice');
    return said;
  });

  await step('A: the 3D picker lists Meshy 7.1 and Meshy T2.5 and no retired Meshy model', async () => {
    await openPicker(page);
    const names = await modelRows(page);
    await shot(page, 'A-picker');
    await closePicker(page);
    assert(names.includes(LATEST_NAME), `rows ${names.join(' | ')}`);
    assert(names.includes(T25_NAME), `rows ${names.join(' | ')}`);
    assert(!names.some((name) => RETIRED_NAME.test(name)), `a retired model is listed: ${names.join(' | ')}`);
    // Only Meshy rows: another provider's English name may say "(Latest)" (Tripo H3 v3.1).
    const meshyRows = names.filter((name) => name.toLowerCase().startsWith('meshy'));
    for (const id of ['meshy-5', 'meshy-t1', 'latest']) assert(!meshyRows.some((name) => name.toLowerCase().includes(id)), `rows ${names.join(' | ')}`);
    return names.join(' | ');
  });

  await step('B: picking Meshy T2.5 clears the notice', async () => {
    await openPicker(page);
    const section = page.locator('[data-testid=workspace-model-picker-model]');
    await section.locator('button[role=menuitemradio]', { hasText: T25_NAME }).first().click();
    await closePicker(page);
    await notice(page).first().waitFor({ state: 'detached', timeout: 10000 });
    const shown = await trigger(page).innerText();
    assert(shown.includes(T25_NAME) || shown.includes('T2.5'), `trigger "${shown}"`);
    return shown.replace(/\s+/g, ' ');
  });

  await step('D: the chat with Meshy T2.5 picked runs a T2.5 task', async () => {
    const before = (await sessionTasks(context, session.id)).length;
    await page.locator('[data-testid=studio-composer] textarea').fill('生成一个 3D 模型：一张小木凳');
    await page.locator('[data-testid=composer-send]').click();
    const deadline = Date.now() + 60000;
    let task;
    while (!task) {
      const approve = page.locator('[data-testid=studio-approval-approve]');
      if (await approve.count()) await approve.first().click().catch(() => {});
      const tasks = await sessionTasks(context, session.id);
      task = tasks.length > before ? tasks.find((item) => item.offering?.startsWith('meshy/')) ?? tasks[0] : undefined;
      assert(Date.now() < deadline, 'the chat started no task');
      if (!task) await page.waitForTimeout(500);
    }
    assert.equal(task.offering, T25.id, `the chat ran ${task.offering}`);
    const done = await waitTask(context, task.id);
    assert(done.assets?.some((asset) => asset.role === 'model'), `assets ${JSON.stringify(done.assets?.map((asset) => asset.role))}`);
    await page.locator(`[data-testid=version-tile][data-key="n:${task.id}"]`).waitFor({ timeout: 30000 });
    await shot(page, 'D-chat');
    return `${task.id} ${task.offering}`;
  });

  await step('D: Meshy 7.1 and Meshy T2.5 generate through the public task path', async () => {
    const notes = [];
    for (const [capability, id, input] of [
      ['text-to-3d', LATEST.id, { prompt: 'A small wooden stool' }],
      ['text-to-3d', T25.id, { prompt: 'A small wooden stool', target_polycount: 8000 }],
    ]) {
      const taskID = await submit(context, session.id, capability, id, input);
      const task = await waitTask(context, taskID);
      assert.equal(task.offering_id ?? task.offering?.id, id);
      notes.push(`${id} ${task.state}`);
    }
    return notes.join('; ');
  });

  await step('E: two pictures with Meshy 7.1 stay attached and the composer says only the first is used', async () => {
    await openPicker(page);
    await page.locator('[data-testid=workspace-model-picker-model] button[role=menuitemradio]', { hasText: LATEST_NAME }).first().click();
    await closePicker(page);
    const composer = page.locator('[data-testid=studio-composer]');
    await composer.locator('input[type=file]').setInputFiles(PICTURES.map((buffer, index) => ({ name: `view-${index + 1}.png`, mimeType: 'image/png', buffer })));
    await waitFor(async () => (await page.locator('[data-testid=composer-attachment-chip]').count()) === 2, 'two pictures are not attached');
    const limit = page.locator('[data-testid=composer-reference-limit]');
    await limit.waitFor({ timeout: 10000 });
    assert.equal((await limit.innerText()).trim(), LIMIT_NOTICE);
    assert(!(await page.locator('[data-testid=composer-send]').isDisabled()), 'the send is off');
    await shot(page, 'E-limit');
    const removeSecond = fill(text('ui.chat.input.remove_attachment', locale), { index: IMAGE_BASE + 1 });
    await composer.getByRole('button', { name: removeSecond }).click();
    await limit.waitFor({ state: 'detached', timeout: 10000 });
    await composer.locator('input[type=file]').setInputFiles([{ name: 'view-2.png', mimeType: 'image/png', buffer: PICTURES[1] }]);
    await limit.waitFor({ timeout: 10000 });
    return (await limit.innerText()).trim();
  });

  await step('E: the approval card shows the uploaded picture and the task froze one picture', async () => {
    const before = (await sessionTasks(context, session.id)).length;
    await page.locator('[data-testid=studio-composer] textarea').fill('用这两张图生成一个 3D 模型');
    await page.locator('[data-testid=composer-send]').click();
    const card = page.locator('[data-testid=studio-approval-input]').first();
    await card.waitFor({ timeout: 60000 });
    const thumbs = page.locator('[data-testid=studio-approval-thumb]');
    await waitFor(async () => (await thumbs.evaluateAll((items) => items.every((item) => item.getAttribute('data-state') === 'image'))), 'an approval thumbnail is a placeholder', 10000);
    const loaded = await page.locator('[data-testid=studio-approval-thumb] img').evaluateAll((items) => items.map((item) => item.complete && item.naturalWidth > 0));
    assert(loaded.length > 0 && loaded.every(Boolean), `thumbnails loaded ${JSON.stringify(loaded)}`);
    await shot(page.locator('[data-testid=studio-approval-input]').first().locator('xpath=ancestor::article[1]'), 'E-card');
    // Follow-up E: the card says only the first picture is used, and so does the reply that brought it.
    const cardNote = page.locator('[data-testid=studio-approval-reference-note]').first();
    await cardNote.waitFor({ timeout: 10000 });
    assert.equal((await cardNote.innerText()).trim(), PICTURE_NOTE);
    assert.equal(await page.locator('[data-testid=studio-approval-input]').count(), 1, 'the card lists more than the one picture used');
    const listed = await api(context, 'GET', routes.chat_session_pending_approvals.replace('{sessionID}', encodeURIComponent(session.id)));
    assert.equal(listed.items?.[0]?.reference_note, PICTURE_NOTE, `pending approval ${JSON.stringify(listed.items?.[0] ?? {})}`);
    await waitFor(async () => (await assistantContents(context, session.id)).some((content) => content.includes(PICTURE_NOTE)),
      'the reply that brought the card does not say only the first picture is used', 20000);
    await page.locator('[data-testid=studio-approval-approve]').first().click();
    const task = await waitFor(async () => {
      const tasks = await sessionTasks(context, session.id);
      return tasks.length > before ? tasks.find((item) => item.offering === LATEST_IMAGE.id) : undefined;
    }, 'the approved step started no Meshy 7.1 image to 3D task', 60000);
    const done = await waitTask(context, task.id);
    const references = done.input?.reference_assets ?? done.inputs?.reference_assets;
    if (references !== undefined) assert.equal(references.length, 1, `frozen pictures ${JSON.stringify(references)}`);
    // Follow-up E: the message that reports the finished model says it too, in the chat and in the read.
    await waitFor(async () => (await assistantContents(context, session.id)).includes(FINISHED_NOTE),
      `no finished message "${FINISHED_NOTE}"`, 30000);
    await waitFor(async () => (await page.locator('[data-testid=chat-messages]').innerText()).includes(FINISHED_NOTE),
      'the chat does not show the finished message with the note', 20000);
    await shot(page, 'E-finished');
    return `${task.id} ${loaded.length} thumbnail(s); ${PICTURE_NOTE}`;
  });

  await step('F: the task API keeps the first picture and says so; the retired Offering is refused', async () => {
    const ids = [];
    for (const [index, buffer] of PICTURES.entries()) {
      const uploaded = await context.request.post(`${BASE}${routes.assets}`, { headers: { Origin: BASE }, multipart: { file: { name: `api-${index}.png`, mimeType: 'image/png', buffer } } });
      const body = await uploaded.json();
      assert(uploaded.ok(), `upload ${uploaded.status()} ${JSON.stringify(body)}`);
      ids.push(body.id);
    }
    const prepared = await api(context, 'POST', routes.task_prepare, { session_id: session.id, capability: 'image-to-3d', offering_id: LATEST_IMAGE.id, input: { reference_assets: ids } });
    // Note 99647: the task API answers the same Schema text as the composer and the card, in the reader's language.
    assert.equal(prepared.reference_note, PICTURE_NOTE, `task API note ${prepared.reference_note}`);
    const refused = await context.request.fetch(`${BASE}${routes.task_prepare}`, { method: 'POST', headers: { Origin: BASE },
      data: { session_id: session.id, capability: 'text-to-3d', offering_id: `meshy/${RETIRED.model_id}/text-to-3d`, input: { prompt: 'castle' } } });
    const problem = await refused.json();
    assert.equal(refused.status(), 400, JSON.stringify(problem));
    assert.equal(problem.code, 'invalid_input');
    assert.equal(problem.user_facing, true);
    assert.equal(problem.detail, fill(field('runtime.generation_messages.retired_model', locale), { from: nameIn(RETIRED.model_name, locale), to: LATEST_NAME }), `retired detail ${problem.detail}`);
    return prepared.reference_note;
  });

  // G: direct preparation is refused before any freeze, grant, task or provider dispatch. Only prepare is
  // called: nothing here is submitted or approved.
  await step('G: direct refusals name the public input and counts, or the extra-credit consent, without side effects', async () => {
    const beforeTasks = (await sessionTasks(context, session.id)).length;
    const beforePending = (await pendingApprovals(context, session.id)).length;
    const pictures = await assetUploads(context, FIVE_PICTURES);
    assert.equal(pictures.length, 5, 'the check uploads five distinct pictures');

    const multiview = await prepareRefusal(context, {
      session_id: session.id, capability: 'image-to-3d', offering_id: MULTIVIEW.id,
      input: { reference_assets: pictures },
    });
    assert.equal(multiview.status, 400, JSON.stringify(multiview.body));
    assert.equal(multiview.body.code, 'invalid_input');
    assert.equal(multiview.body.user_facing, true);
    const expectedLimit = referenceRefusalIn(locale);
    assert.equal(multiview.body.detail, expectedLimit, `multiview detail ${multiview.body.detail}`);
    // Independent facts: five were given, four fit, and the answer is not a generic validation failure.
    // The public label itself says "1–4"; remove it so the numbers checked are the actual and the maximum.
    const withoutLabel = multiview.body.detail.split(nameIn(MULTIVIEW_REFERENCE.label, locale)).join('');
    assert(/(^|\D)5(\D|$)/.test(withoutLabel), `actual count missing: ${multiview.body.detail}`);
    assert(/(^|\D)4(\D|$)/.test(withoutLabel), `maximum missing: ${multiview.body.detail}`);
    assert(locale === DEFAULT_LOCALE ? multiview.body.detail.includes('没有提交') : /not submitted/i.test(multiview.body.detail),
      `positive refusal missing: ${multiview.body.detail}`);
    assert(!multiview.body.detail.includes(MULTIVIEW_REFERENCE.name) && !multiview.body.detail.includes('reference_assets'),
      `internal parameter in refusal: ${multiview.body.detail}`);

    const rodin = await prepareRefusal(context, {
      session_id: session.id, capability: 'image-to-3d', offering_id: RODIN.id,
      input: { reference_assets: [pictures[0]] },
    });
    assert.equal(rodin.status, 400, JSON.stringify(rodin.body));
    assert.equal(rodin.body.code, 'invalid_input');
    assert.equal(rodin.body.user_facing, true);
    const expectedConsent = field('runtime.generation_messages.extra_credits_consent_required', locale);
    assert.equal(rodin.body.detail, expectedConsent, `Rodin detail ${rodin.body.detail}`);
    assert(locale === DEFAULT_LOCALE ? rodin.body.detail.includes('明确同意') : /approve|consent/i.test(rodin.body.detail),
      `next step missing: ${rodin.body.detail}`);
    assert(!rodin.body.detail.includes('authorize_extra_credits'), `internal consent parameter in refusal: ${rodin.body.detail}`);

    for (const detail of [multiview.body.detail, rodin.body.detail]) {
      for (const id of pictures) assert(!detail.includes(id), `asset ID ${id} in refusal ${detail}`);
      assert(!detail.includes('/api/v1/assets/'), `asset path in refusal ${detail}`);
    }
    assert.equal((await sessionTasks(context, session.id)).length, beforeTasks, 'a refused direct prepare created a Task');
    assert.equal((await pendingApprovals(context, session.id)).length, beforePending, 'a refused direct prepare created a pending approval');
    await shot(page, 'G-direct-refusals');
    return `multiview ${multiview.status}; Rodin ${rodin.status}`;
  });

  // H: the rendered Agent path preserves the selected views and refuses the step. The turn's frozen locale
  // selects the Schema refusal text this check reads from the Schema.
  await step('H: the Agent preserves five selected reference pictures and gives the Schema refusal, with no Task', async () => {
    await openPicker(page);
    await page.locator('[data-testid=workspace-model-picker-model] button[role=menuitemradio]', { hasText: MULTIVIEW_NAME }).first().click();
    await closePicker(page);
    assert((await trigger(page).innerText()).includes(MULTIVIEW_NAME), `the picker did not select ${MULTIVIEW_NAME}`);
    const composer = page.locator('[data-testid=studio-composer]');
    await waitFor(async () => (await page.locator('[data-testid=composer-attachment-chip]').count()) === 0,
      'an earlier picture is still attached; this check must attach exactly five');
    await composer.locator('input[type=file]').setInputFiles(FIVE_PICTURES.map((buffer, index) => ({
      name: `preserved-${index + 1}.png`, mimeType: 'image/png', buffer,
    })));
    await waitFor(async () => (await page.locator('[data-testid=composer-attachment-chip]').count()) === 5,
      'the composer did not attach all five pictures (the UI limit would block this check)', 30000);
    assert(!(await page.locator('[data-testid=composer-send]').isDisabled()), 'the five-picture send is off');
    assert.equal(await page.locator('[data-testid=composer-reference-limit]').count(), 0,
      'a preserve-policy model showed the cut note instead of refusing');
    const beforeTasks = (await sessionTasks(context, session.id)).length;
    const beforePending = (await pendingApprovals(context, session.id)).length;
    await page.locator('[data-testid=studio-composer] textarea').fill('用这五张图生成一个 3D 模型');
    await page.locator('[data-testid=composer-send]').click();
    await waitFor(async () => (await assistantContents(context, session.id)).some((content) => content.includes(REFERENCE_REFUSAL)),
      'the Agent reply does not carry the Schema refusal', 30000);
    await waitFor(async () => (await page.locator('[data-testid=chat-messages]').innerText()).includes(REFERENCE_REFUSAL),
      'the chat does not render the Schema refusal', 20000);
    const messages = await chatMessages(context, session.id);
    const turn = [...messages].reverse().find((message) => message.role === 'user' && String(message.content ?? '').includes('五张图'));
    assert(turn, 'the five-picture user turn is missing');
    const attachments = (turn.attachments ?? []).filter((attachment) => attachment.kind === 'image');
    const images = Array.isArray(turn.imageUrls) ? turn.imageUrls : (() => {
      try { return JSON.parse(turn.imageUrls || '[]'); } catch { return []; }
    })();
    assert.equal(Math.max(attachments.length, images.length), 5,
      `the turn kept ${attachments.length} attachment(s) and ${images.length} image(s) of five pictures`);
    const said = await page.locator('[data-testid=chat-messages]').innerText();
    for (const id of attachments.map((attachment) => attachment.assetId).filter(Boolean)) {
      assert(!said.includes(id), `the chat shows asset ID ${id}`);
    }
    for (const privateText of ['reference_assets', 'authorize_extra_credits', 'parameter "', '/api/v1/assets/']) {
      assert(!said.includes(privateText), `the chat shows ${privateText}`);
    }
    assert(!said.includes(field('chat.presentation.messages.generation_error', locale)),
      'the refusal rendered as the generic generation error');
    assert.equal((await sessionTasks(context, session.id)).length, beforeTasks, 'the refused Agent step created a Task');
    assert.equal((await pendingApprovals(context, session.id)).length, beforePending, 'the refused Agent step created a pending approval');
    await shot(page, 'H-agent-refusal');
    return `${attachments.length} pictures preserved; ${locale} Schema refusal`;
  });

  // C: a session whose turn used Meshy 7, opened on a device with no local pick (a second person's fresh
  // browser, so nothing from A, B or D carries over).
  const fresh = await guardedContext(browser, { width, height }, outside, locale);
  await login(fresh, `${user}-c`);
  const restored = await api(fresh, 'POST', legacy.chat_sessions, { title: `#329 restore ${engine} ${viewport} ${locale}` });
  await api(fresh, 'POST', `${legacy.chat_sessions}/${encodeURIComponent(restored.id)}/messages`,
    { content: '你好', ai_model: RETIRED.model_id, creative_domain: 'model-3d' });
  const recorded = await waitFor(async () => {
    const read = await api(fresh, 'GET', `${legacy.chat_sessions}/${encodeURIComponent(restored.id)}`);
    return (read.generationModels ?? read.generation_models)?.['model-3d'];
  }, 'the session does not record its 3D model');
  const second = await fresh.newPage();
  second.on('pageerror', (error) => errors.push(error.message));
  await step('C: a session that used Meshy 7 restores Meshy 7.1 with the notice', async () => {
    // #329 follow-up C: the server moved the turn to the successor and the session read names the retired model.
    assert.equal(recorded, RETIRED.successor_model_id, `recorded ${recorded}`);
    const read = await api(fresh, 'GET', `${legacy.chat_sessions}/${encodeURIComponent(restored.id)}`);
    assert.equal(read.generationModelsRetired?.['model-3d'], RETIRED.model_id, `retired member ${JSON.stringify(read.generationModelsRetired)}`);
    await openStudio(second, restored.id);
    await notice(second).first().waitFor({ timeout: 20000 }).catch(async (error) => {
      await shot(second, 'C-failed');
      const stored = await second.evaluate((key) => localStorage.getItem(key), PREFS_KEY);
      throw new Error(`no notice; trigger "${(await trigger(second).innerText()).replace(/\s+/g, ' ')}", stored ${stored}: ${error.message}`);
    });
    assert.equal((await notice(second).first().innerText()).trim(), NOTICE);
    const shown = await trigger(second).innerText();
    assert(shown.includes(LATEST_NAME) && !RETIRED_NAME.test(shown), `trigger "${shown}"`);
    await openPicker(second);
    const names = await modelRows(second);
    const checked = await second.locator('[data-testid=workspace-model-picker-model] button[role=menuitemradio][aria-checked=true]').innerText();
    await closePicker(second);
    assert(checked.includes(LATEST_NAME), `checked row "${checked}"`);
    assert(!names.some((name) => RETIRED_NAME.test(name)), `rows ${names.join(' | ')}`);
    await shot(second, 'C-restore');
    return shown.replace(/\s+/g, ' ');
  });
  await fresh.close();

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
      for (const locale of LOCALES) await checks(engine, browser, viewport, locale);
    }
  } finally {
    await browser.close();
  }
}
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), locales: LOCALES, results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.locale} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
console.log(`report: ${path.join(OUT, 'report.json')}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
