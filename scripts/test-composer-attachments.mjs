#!/usr/bin/env node
/**
 * Loopback browser regression for #292: a picture attached in the composer is uploaded as the person's input
 * asset when the turn is sent, the message stores the asset address (never a data address), and the transcript
 * shows it through its preview after the send, after a reload and after switching session and back; the large
 * view opens the original. A message stored before #292 with a data address is shown too.
 *
 * Needs a local server with mock providers, mock Agent and mock login (no credentials, no paid calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18492 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * (build the web assets first with `pnpm run build`), then
 *   BASE=http://127.0.0.1:18492 node scripts/test-composer-attachments.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), OUT (evidence folder, default a new temporary folder),
 * PLAYWRIGHT_MODULE_PATH. Every request outside BASE is refused.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import zlib from 'node:zlib';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright');

const BASE = (process.env.BASE || '').replace(/\/$/, '');
if (!/^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):\d+$/.test(BASE)) {
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18492');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((name) => name.trim()).filter(Boolean);
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'composer-attachments-'));
mkdirSync(OUT, { recursive: true, mode: 0o700 });
const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const routes = application.runtime.http.routes;
const legacy = application.runtime.http.legacy_routes;
const requestFields = application.chat.api.fields;
const responseFields = application.chat.api.response_fields;
const preview = application.runtime.asset_delivery.asset_preview;
const assetPrefix = routes.asset_item.split('{assetID}')[0];

/** A real PNG of the given size (a two-colour gradient), so the browser decodes it and reports its size. */
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
  header[8] = 8; header[9] = 2; header[10] = 0; header[11] = 0; header[12] = 0;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(Buffer.concat(rows))),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

async function api(page, method, url, body) {
  // From the page, so the request carries the page origin the server checks.
  return page.evaluate(async ({ method, url, body }) => {
    const response = await fetch(url, { method, headers: body ? { 'Content-Type': 'application/json' } : {}, ...(body ? { body: JSON.stringify(body) } : {}) });
    const text = await response.text();
    return { status: response.status, body: text ? JSON.parse(text) : null };
  }, { method, url, body });
}

const messagesRoute = (sessionId) => legacy.chat_messages.replace('{sessionID}', encodeURIComponent(sessionId));

async function storedMessages(page, sessionId) {
  const { status, body } = await api(page, 'GET', messagesRoute(sessionId));
  assert.equal(status, 200, `list messages ${status}`);
  return Array.isArray(body) ? body : body[responseFields.messages];
}

async function waitForSettledTurn(page, sessionId) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const messages = await storedMessages(page, sessionId);
    const last = messages[messages.length - 1];
    if (last && last[responseFields.role] !== 'user' && !last[responseFields.is_pending]) return messages;
    await page.waitForTimeout(250);
  }
  throw new Error('the turn did not settle');
}

/** The user's sent pictures as the transcript draws them, once every one has loaded. */
async function sentPictures(page, count) {
  await page.waitForFunction((count) => {
    const images = [...document.querySelectorAll('[data-testid=chat-message][data-role=user] img')];
    return images.length >= count && images.every((image) => image.complete && image.naturalWidth > 0);
  }, count, { timeout: 30000 });
  return page.evaluate(() => [...document.querySelectorAll('[data-testid=chat-message][data-role=user] img')].map((image) => ({
    src: image.getAttribute('src').startsWith('data:') ? `data:…(${image.getAttribute('src').length} chars)` : image.getAttribute('src'),
    inline: image.hasAttribute('data-chat-picture-inline'),
    original: image.getAttribute('data-chat-picture'),
    width: image.naturalWidth,
    height: image.naturalHeight,
  })));
}

function checkPictures(pictures, label) {
  const legacyPicture = pictures.find((picture) => picture.inline);
  const uploaded = pictures.find((picture) => !picture.inline);
  assert(legacyPicture, `${label}: the picture of the message stored with a data address is shown`);
  assert(uploaded, `${label}: the uploaded picture is shown`);
  assert(uploaded.src.startsWith(assetPrefix), `${label}: shown from its asset address (${uploaded.src})`);
  assert(uploaded.src.includes(`${preview.query}=${preview.request_value}`), `${label}: through its preview (${uploaded.src})`);
  assert(uploaded.original.startsWith(assetPrefix) && !uploaded.original.includes('?'), `${label}: the original is marked for copying`);
  return { legacy: legacyPicture, uploaded };
}

async function openSession(page, title) {
  await page.click('[data-testid=studio-session-menu]');
  await page.locator('[data-testid=studio-session-row]', { hasText: title }).locator('button').first().click();
  await page.keyboard.press('Escape').catch(() => {});
}

async function run(engineName) {
  const browser = await pw[engineName].launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(`pageerror: ${error.message}`));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(`console: ${message.text()}`); });
  await context.route('**/*', (route) => (new URL(route.request().url()).origin === BASE ? route.continue() : route.abort()));
  const login = await page.request.post(`${BASE}${legacy.auth_mock}`, { data: {} });
  assert(login.ok(), `mock login ${login.status()}`);
  await page.goto(`${BASE}${routes.product_schema}`);

  const stamp = `${engineName}-${Date.now().toString(36)}`;
  const titleA = `acc-292-A-${stamp}`;
  const titleB = `acc-292-B-${stamp}`;
  const created = await api(page, 'POST', legacy.chat_sessions, { [requestFields.title]: titleA });
  assert(created.status < 300, `create session ${created.status}`);
  const sessionA = created.body[responseFields.id];
  const createdB = await api(page, 'POST', legacy.chat_sessions, { [requestFields.title]: titleB });
  assert(createdB.status < 300, `create session B ${createdB.status}`);

  // A message as clients before #292 sent it: the picture itself as a data address.
  const legacyPicture = `data:image/png;base64,${png(48, 32, 1).toString('base64')}`;
  const seeded = await api(page, 'POST', messagesRoute(sessionA), { [requestFields.content]: '旧消息（data 地址）', [requestFields.image_urls]: [legacyPicture] });
  assert(seeded.status < 300, `seed legacy message ${seeded.status}`);
  await waitForSettledTurn(page, sessionA);

  await page.goto(`${BASE}/?session=${encodeURIComponent(sessionA)}`);
  const box = page.locator('[data-testid=studio-composer] textarea');
  await box.waitFor({ timeout: 30000 });

  // Attach: nothing is uploaded until the turn is sent.
  const uploads = [];
  const turns = [];
  page.on('request', (request) => {
    if (request.method() !== 'POST') return;
    const url = new URL(request.url());
    if (url.pathname === routes.assets) uploads.push(request);
    if (url.pathname === messagesRoute(sessionA)) turns.push(JSON.parse(request.postData() || '{}'));
  });
  const picture = png(320, 200, 2);
  await page.locator('input[type=file][multiple]').first().setInputFiles({ name: 'attached.png', mimeType: 'image/png', buffer: picture });
  await page.locator('[data-testid=composer-attachment-chip]').first().waitFor({ timeout: 10000 });
  assert.equal(uploads.length, 0, 'attaching uploads nothing');
  await box.click();
  await box.fill('请看这张图');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelectorAll('[data-testid=chat-message][data-role=user]').length >= 2, null, { timeout: 30000 });

  assert.equal(uploads.length, 1, 'the picture is uploaded once, at send time');
  assert.equal(turns.length, 1, 'one turn is sent');
  const sentURLs = turns[0][requestFields.image_urls];
  assert.equal(sentURLs.length, 1);
  assert(sentURLs[0].startsWith(assetPrefix), `the turn sends the asset address (${sentURLs[0]})`);
  assert(!JSON.stringify(turns[0]).includes('data:'), 'the turn carries no data address');
  const assetId = decodeURIComponent(sentURLs[0].slice(assetPrefix.length));
  const claim = (turns[0][requestFields.attachments] || []).find((item) => Object.values(item).includes(assetId));
  assert(claim, 'the turn claims the uploaded asset');

  const evidence = { engine: engineName, sessionA, assetId, phases: {} };
  evidence.phases.afterSend = checkPictures(await sentPictures(page, 2), 'after send');
  await page.screenshot({ path: path.join(OUT, `${engineName}-1-after-send.png`) });
  const settled = await waitForSettledTurn(page, sessionA);
  const stored = settled.filter((message) => message[responseFields.role] === 'user');
  // The compatible message API returns the list as JSON text.
  const storedURLs = stored[stored.length - 1][responseFields.image_urls];
  assert.deepEqual(typeof storedURLs === 'string' ? JSON.parse(storedURLs) : storedURLs, sentURLs, 'the stored message holds the asset address');

  await page.reload();
  evidence.phases.afterReload = checkPictures(await sentPictures(page, 2), 'after reload');
  await page.screenshot({ path: path.join(OUT, `${engineName}-2-after-reload.png`) });

  await openSession(page, titleB);
  await page.waitForFunction((id) => new URL(location.href).searchParams.get('session') !== id, sessionA, { timeout: 15000 });
  await page.waitForTimeout(500);
  await openSession(page, titleA);
  await page.waitForFunction((id) => new URL(location.href).searchParams.get('session') === id, sessionA, { timeout: 15000 });
  evidence.phases.afterSwitch = checkPictures(await sentPictures(page, 2), 'after switching session and back');
  await page.screenshot({ path: path.join(OUT, `${engineName}-3-after-switch.png`) });

  // The large view opens the original, not the preview.
  await page.locator('[data-testid=chat-message][data-role=user] img[data-chat-picture]').last().click();
  const dialogImage = page.locator('[data-testid=result-detail-dialog] img').first();
  await dialogImage.waitFor({ timeout: 15000 });
  await page.waitForFunction(() => {
    const image = document.querySelector('[data-testid=result-detail-dialog] img');
    return image && image.complete && image.naturalWidth > 0;
  }, null, { timeout: 15000 });
  const large = await dialogImage.evaluate((image) => ({ src: image.getAttribute('src'), width: image.naturalWidth, height: image.naturalHeight }));
  assert.equal(large.src, sentURLs[0], 'the large view shows the original');
  assert.deepEqual([large.width, large.height], [320, 200], 'at its own size');
  evidence.phases.largeView = large;
  await page.screenshot({ path: path.join(OUT, `${engineName}-4-large-view.png`) });

  // An upload that fails sends nothing: the person is told why (with the request ID) and the draft stays.
  await page.keyboard.press('Escape');
  await page.locator('[data-testid=result-detail-dialog]').waitFor({ state: 'detached', timeout: 10000 });
  const userMessages = await page.locator('[data-testid=chat-message][data-role=user]').count();
  const failingRequestId = 'req_0123456789abcdef';
  await page.route(`${BASE}${routes.assets}`, (route) => (route.request().method() === 'POST'
    ? route.fulfill({ status: 500, contentType: 'application/json', headers: { 'X-Request-ID': failingRequestId }, body: JSON.stringify({ request_id: failingRequestId }) })
    : route.continue()));
  const turnsBefore = turns.length;
  await page.locator('input[type=file][multiple]').first().setInputFiles({ name: 'second.png', mimeType: 'image/png', buffer: png(64, 64, 3) });
  await page.locator('[data-testid=composer-attachment-chip]').first().waitFor({ timeout: 10000 });
  await box.click();
  await box.fill('这一条不会发出');
  await page.keyboard.press('Enter');
  const toast = page.locator('[data-sonner-toast]', { hasText: application.content.messages['ui.chat.input.attachment_upload_failed'] });
  await toast.first().waitFor({ timeout: 15000 });
  const toastText = await toast.first().innerText();
  assert(toastText.includes(failingRequestId), `the toast names the request (${toastText})`);
  assert.equal(turns.length, turnsBefore, 'no turn is sent');
  assert.equal(await page.locator('[data-testid=chat-message][data-role=user]').count(), userMessages, 'no message is added');
  assert.equal(await box.inputValue(), '这一条不会发出', 'the words stay in the box');
  assert.equal(await page.locator('[data-testid=composer-attachment-chip]').count(), 1, 'the picture stays attached');
  evidence.phases.failedUpload = { toast: toastText };
  await page.screenshot({ path: path.join(OUT, `${engineName}-5-failed-upload.png`) });
  await page.unroute(`${BASE}${routes.assets}`);

  // The failed upload's 500 is the one console error the browser logs for it.
  evidence.errors = errors.filter((error) => !(error.startsWith('console:') && error.includes('500')));
  assert.deepEqual(evidence.errors, [], `${engineName}: no page or console errors`);
  await browser.close();
  return evidence;
}

const report = [];
for (const engine of ENGINES) {
  const evidence = await run(engine);
  report.push(evidence);
  console.log(`${engine}: PASS`, JSON.stringify(evidence.phases));
}
writeFileSync(path.join(OUT, 'report.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
console.log(`evidence: ${OUT}`);
