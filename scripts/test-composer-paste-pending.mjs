#!/usr/bin/env node
/**
 * Loopback browser regression for the pasted-picture / send race (#313, #281 follow-up).
 *
 * The real ChatColumn and Composer are mounted in a Vite page. Every HTTP answer is a local fixture
 * (schema, empty chat, one delayed pasted picture, one asset upload and one turn); no Provider, model,
 * paid call or request outside this loopback origin is made.
 *
 * Run:
 *   node scripts/test-composer-paste-pending.mjs
 * Optional: PLAYWRIGHT_MODULE_PATH, OUT (evidence folder), ENGINES (default chromium,webkit).
 */
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import zlib from 'node:zlib';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright');
const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const schema = structuredClone(application);
delete schema.runtime.configuration;
delete schema.chat.harness.context_template;
const messages = schema.content.messages;
const fields = schema.chat.api.fields;
const responseFields = schema.chat.api.response_fields;
const routes = schema.runtime.http.routes;
const legacy = schema.runtime.http.legacy_routes;
const eventTypes = schema.chat.stream.event_types;
const out = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'composer-paste-pending-'));
mkdirSync(out, { recursive: true, mode: 0o700 });

/** A real PNG so prepareImageUpload keeps the bytes and the browser can decode it. */
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

const PICTURES = {
  '/fixture/pasted-a.png': png(64, 48, 1),
  '/fixture/pasted-b.png': png(48, 64, 2),
  '/fixture/pasted-forbidden.png': png(32, 32, 3),
  '/fixture/pasted-native.png': png(24, 40, 4),
};
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');

const sessionA = { id: 's1', title: 'Paste session A', createdAt: '2026-10-21T00:00:00Z', updatedAt: '2026-10-21T00:00:00Z' };
const sessionB = { id: 's2', title: 'Paste session B', createdAt: '2026-10-21T00:00:00Z', updatedAt: '2026-10-21T00:00:00Z' };

const newFixture = () => ({
  delay: { '/fixture/pasted-a.png': 1200, '/fixture/pasted-b.png': 1200, '/fixture/pasted-forbidden.png': 1200 },
  status: {},
  pasteRequests: [],
  pasteCompletions: [],
  uploads: [],
  turns: [],
  /** A per-session promise a test can leave pending to hold the accept response of a turn. */
  turnHold: {},
  streamRequests: 0,
  external: [],
  unexpectedWrites: [],
});
let fixture = newFixture();
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);

function multipartFile(buffer) {
  const body = buffer.toString('latin1');
  const filenameAt = body.indexOf('filename="');
  if (filenameAt < 0) return null;
  const bytesAt = body.indexOf('\r\n\r\n', filenameAt);
  if (bytesAt < 0) return null;
  const end = body.indexOf('\r\n--', bytesAt + 4);
  if (end < 0) return null;
  return {
    filename: body.slice(filenameAt + 10, body.indexOf('"', filenameAt + 10)),
    bytes: Buffer.from(body.slice(bytesAt + 4, end), 'latin1'),
  };
}

const server = await createServer({
  configFile: path.join(root, 'vite.go-web.config.mjs'),
  cacheDir: path.join(out, 'vite-cache'),
  logLevel: 'error',
  server: { host: '127.0.0.1', port: 0, hmr: false },
  plugins: [{
    name: 'composer-paste-pending-fixture',
    enforce: 'pre',
    resolveId(value) {
      if (value === 'virtual:composer-paste-pending') return '\0composer-paste-pending';
      for (const name of ['model-picker', 'skill-picker', 'agent-scope-picker', 'params-popover']) {
        if (new RegExp(`/studio/${name}(?:\\.tsx)?$`).test(value)) return `\0fixture-${name}`;
      }
      return null;
    },
    load(value) {
      if (value.startsWith('\0fixture-')) {
        const name = value.slice('\0fixture-'.length);
        if (name === 'model-picker') return 'export const ModelPicker = () => null; export const GenerationModelNotice = () => null;';
        const exportName = { 'skill-picker': 'SkillPicker', 'agent-scope-picker': 'AgentScopePicker', 'params-popover': 'ParamsPopover' }[name];
        return `export const ${exportName} = () => null;`;
      }
      if (value !== '\0composer-paste-pending') return null;
      return `
import React from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import { ProductSchemaProvider } from ${JSON.stringify(path.join(root, 'src/components/product-schema-provider.tsx'))};
import { ChatColumn } from ${JSON.stringify(path.join(root, 'src/components/studio/chat-column.tsx'))};
import { useChatStore } from ${JSON.stringify(path.join(root, 'src/store/chat-store.ts'))};
import { useStudioStore } from ${JSON.stringify(path.join(root, 'src/store/studio-store.ts'))};
import ${JSON.stringify(path.join(root, 'src/app/globals.css'))};
const schema = ${JSON.stringify(schema)};
useChatStore.setState({
  activeSessionId: 's1', activeSessionRevision: 7, sessionsLoaded: true,
  sessions: ${JSON.stringify([sessionA, sessionB])},
  messagesBySession: { s1: [], s2: [] }, olderCursorBySession: { s1: '', s2: '' }, streamingBuffers: {},
});
useStudioStore.setState({ chatCollapsed: false, chatWidth: 400 });
window.__chatStore = useChatStore;
window.__studioStore = useStudioStore;
createRoot(document.getElementById('root')).render(
  React.createElement('div', { style: { height: '100vh', display: 'flex' } },
    React.createElement(ProductSchemaProvider, { initialSchema: schema },
      React.createElement(ChatColumn),
      React.createElement(Toaster, { duration: 60000 }))),
);`;
    },
    transformIndexHtml(html) {
      return html
        .replace('{{.Schema}}', JSON.stringify(schema))
        .replace('src="/src/main.tsx"', 'src="/@id/virtual:composer-paste-pending"');
    },
    // WebKit's route.request().postDataBuffer() drops multipart file bytes, so uploads reach this middleware
    // over the real loopback connection and are recorded here for every engine.
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const pathname = new URL(request.url ?? '/', 'http://127.0.0.1').pathname;
        if (request.method !== 'POST' || pathname !== routes.assets) return next();
        const chunks = [];
        request.on('data', (chunk) => chunks.push(chunk));
        request.on('end', () => {
          const part = multipartFile(Buffer.concat(chunks));
          if (!part) {
            fixture.unexpectedWrites.push('POST assets without a file part');
            response.statusCode = 400;
            response.end('missing file part');
            return;
          }
          const id = `a${fixture.uploads.length + 1}`;
          fixture.uploads.push({ id, filename: part.filename, hash: sha256(part.bytes), bytes: part.bytes.length });
          response.setHeader('content-type', 'application/json');
          response.end(JSON.stringify({ id }));
        });
      });
    },
  }],
});
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, '');
const origin = new URL(base).origin;

async function handle(route) {
  const request = route.request();
  const url = new URL(request.url());
  if (url.origin !== origin) {
    fixture.external.push(request.url());
    return route.abort();
  }
  const method = request.method();
  if (method === 'GET' && PICTURES[url.pathname]) {
    const requestedAt = Date.now();
    fixture.pasteRequests.push(url.pathname);
    await new Promise((resolve) => setTimeout(resolve, fixture.delay[url.pathname] ?? 0));
    fixture.pasteCompletions.push({ path: url.pathname, requestedAt, completedAt: Date.now() });
    const status = fixture.status[url.pathname] ?? 200;
    return route.fulfill({
      status,
      contentType: status === 200 ? 'image/png' : 'text/plain',
      body: status === 200 ? PICTURES[url.pathname] : 'forbidden',
    });
  }
  if (method === 'GET' && url.pathname === routes.product_schema) {
    return route.fulfill({ json: { schema } });
  }
  if (method === 'GET' && url.pathname === legacy.version) {
    return route.fulfill({ json: { version: 'fixture' } });
  }
  if (method === 'GET' && url.pathname === legacy.auth_session) {
    return route.fulfill({ json: { user: { id: 'fixture-owner', name: 'Fixture owner', email: 'fixture@example.invalid' }, isAdmin: false } });
  }
  if (method === 'GET' && url.pathname === legacy.chat_sessions) {
    return route.fulfill({ json: { sessions: [sessionA, sessionB], next_cursor: '', total: 2 } });
  }
  if (method === 'GET' && /^\/api\/chat\/sessions\/[^/]+$/.test(url.pathname)) {
    const current = url.pathname.endsWith('/s2') ? sessionB : sessionA;
    return route.fulfill({ json: current });
  }
  if (method === 'GET' && /^\/api\/chat\/sessions\/[^/]+\/messages$/.test(url.pathname)) {
    return route.fulfill({ json: { messages: [], next_cursor: '' } });
  }
  if (method === 'GET' && /^\/api\/v1\/chat\/sessions\/[^/]+\/approval$/.test(url.pathname)) {
    return route.fulfill({ json: { approvalMode: 'manual', estimatedCredits: 0 } });
  }
  if (method === 'GET' && /^\/api\/v1\/chat\/sessions\/[^/]+\/notes$/.test(url.pathname)) {
    return route.fulfill({ json: { notes: '' } });
  }
  if (method === 'GET' && /^\/api\/v1\/chat\/sessions\/[^/]+\/pending-approvals$/.test(url.pathname)) {
    return route.fulfill({ json: { items: [] } });
  }
  if (method === 'GET' && /^\/api\/v1\/chat\/sessions\/[^/]+\/lineage$/.test(url.pathname)) {
    return route.fulfill({ json: { nodes: [], edges: [], groups: [], next_cursor: '' } });
  }
  if (method === 'GET' && /^\/api\/v1\/chat\/sessions\/[^/]+\/three-views$/.test(url.pathname)) {
    return route.fulfill({ json: { items: [] } });
  }
  if (method === 'GET' && url.pathname === routes.chat_skills) {
    return route.fulfill({ json: { revision: 'fixture', skills: [] } });
  }
  if (method === 'GET' && url.pathname === routes.offerings) {
    return route.fulfill({ json: { offerings: [] } });
  }
  if (method === 'GET' && url.pathname === routes.agent_params) {
    return route.fulfill({ json: {} });
  }
  if (method === 'GET' && /^\/api\/v1\/chat\/sessions\/[^/]+\/harness$/.test(url.pathname)) {
    const sessionId = url.pathname.split('/')[5];
    return route.fulfill({ json: {
      status: 'free', reason: '', missing_parameters: [], can_propose: false,
      selection: {
        session_id: sessionId, revision: 1, digest: 'fixture', enabled_operations: [], sources: [],
        fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: false, updated_at: '2026-10-21T00:00:00Z',
      },
    } });
  }
  if (method === 'GET' && /^\/api\/chat\/sessions\/[^/]+\/stream$/.test(url.pathname)) {
    fixture.streamRequests += 1;
    return route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
      body: `data: ${JSON.stringify({ [schema.chat.stream.event_fields.type]: eventTypes.done })}\n\n`,
    });
  }
  if (method === 'POST' && url.pathname === routes.assets) {
    return route.continue();
  }
  if (method === 'POST' && /^\/api\/chat\/sessions\/[^/]+\/messages$/.test(url.pathname)) {
    const sessionId = url.pathname.split('/')[4];
    const body = JSON.parse(request.postData() || '{}');
    const turn = { sessionId, body, requestedAt: Date.now(), acceptedAt: null };
    fixture.turns.push(turn);
    if (fixture.turnHold[sessionId]) await fixture.turnHold[sessionId];
    turn.acceptedAt = Date.now();
    return route.fulfill({ json: { [responseFields.message_id]: `turn-${fixture.turns.length}`, [responseFields.session_id]: sessionId } });
  }
  if (method === 'GET' && url.pathname.startsWith('/api/')) {
    return route.fulfill({ json: {} });
  }
  if (method !== 'GET') {
    fixture.unexpectedWrites.push(`${method} ${url.pathname}`);
    return route.abort();
  }
  return route.continue();
}

let page;
let pageErrors = [];

const box = () => page.locator('[data-testid=studio-composer] textarea');
const send = () => page.locator('[data-testid=composer-send]');
const chips = () => page.locator('[data-testid=composer-attachment-chip]');
const toastCount = () => page.locator('[data-sonner-toast]').count();
const pause = (milliseconds) => page.waitForTimeout(milliseconds);

async function waitTurns(count) {
  const until = Date.now() + 15000;
  while (Date.now() < until) {
    if (fixture.turns.length >= count) return;
    await pause(50);
  }
  throw new Error(`expected ${count} turn(s), saw ${fixture.turns.length}`);
}

async function waitTurn(sessionId, count, accepted) {
  const until = Date.now() + 15000;
  while (Date.now() < until) {
    const turns = fixture.turns.filter((turn) => turn.sessionId === sessionId);
    if (turns.length >= count && (!accepted || turns[count - 1].acceptedAt !== null)) return turns[count - 1];
    await pause(50);
  }
  throw new Error(`expected ${count} ${accepted ? 'accepted ' : ''}turn(s) in ${sessionId}`);
}

async function noTurns(expected, label, milliseconds = 300) {
  await pause(milliseconds);
  assert.equal(fixture.turns.length, expected, `${label}: no turn may be sent before the pasted inputs are ready`);
}

async function dispatchPaste({ html = '', text = '', file = null }) {
  await page.evaluate(({ html, text, file }) => {
    const textarea = document.querySelector('[data-testid=studio-composer] textarea');
    textarea.focus();
    const data = new DataTransfer();
    if (text) data.setData('text/plain', text);
    if (html) data.setData('text/html', html);
    if (file) data.items.add(new File([Uint8Array.from(atob(file.bytes), (character) => character.charCodeAt(0))], file.name, { type: file.type }));
    const event = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'clipboardData', { value: data });
    textarea.dispatchEvent(event);
    window.__lastPastePrevented = event.defaultPrevented;
  }, { html, text, file });
}

async function waitForChips(count) {
  const until = Date.now() + 15000;
  while (Date.now() < until) {
    if (await chips().count() >= count) return;
    await pause(50);
  }
  throw new Error(`expected ${count} attachment chip(s), saw ${await chips().count()}`);
}

async function switchSession(id) {
  await page.waitForFunction(() => window.__chatStore?.getState, null, { timeout: 15000 });
  await page.evaluate((id) => window.__chatStore.getState().setActiveSession(id), id);
  await page.waitForFunction((id) => window.__chatStore.getState().activeSessionId === id, id);
  await pause(250);
}

async function sendAndWait(expectedTurns) {
  await send().click();
  await waitTurns(expectedTurns);
  await page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), null, { timeout: 15000 });
}

const results = [];

/** PC remains the product target; this only proves the composer still renders in a narrowed window. */
async function checkNarrow(engine, browser, errors) {
  const narrow = await browser.newPage({ viewport: { width: 640, height: 720 }, serviceWorkers: 'block' });
  narrow.setDefaultTimeout(20000);
  narrow.on('pageerror', (error) => errors.push(`narrow:${error.message}`));
  try {
    await narrow.route('**/*', handle);
    await narrow.goto(base);
    await narrow.locator('[data-testid=studio-composer] textarea').waitFor();
    await narrow.waitForFunction(() => document.querySelector('[data-testid=composer-send]') !== null);
    const layout = await narrow.evaluate(() => {
      const input = document.querySelector('[data-testid=studio-composer] textarea').getBoundingClientRect();
      const button = document.querySelector('[data-testid=composer-send]').getBoundingClientRect();
      const viewport = document.documentElement.clientWidth;
      return {
        viewport,
        input: { width: input.width, height: input.height, left: input.left, right: input.right },
        send: { width: button.width, height: button.height, left: button.left, right: button.right },
        overflow: document.documentElement.scrollWidth - viewport,
      };
    });
    assert(layout.input.width > 0 && layout.input.height > 0, 'narrow window: the composer input renders');
    assert(layout.input.left >= 0 && layout.input.right <= layout.viewport, 'narrow window: the composer input stays on screen');
    assert(layout.send.width > 0 && layout.send.height > 0, 'narrow window: the send control renders');
    assert(layout.overflow <= 1, `narrow window: no horizontal overflow (${layout.overflow}px)`);
    await narrow.screenshot({ path: path.join(out, `${engine}-narrow.png`) });
    return layout;
  } finally {
    await narrow.close();
  }
}

try {
  for (const engine of ENGINES) {
    fixture = newFixture();
    pageErrors = [];
    const checks = [];
    const started = Date.now();
    let browser;
    let version = '';
    console.error(`START ${engine}`);
    try {
      browser = await pw[engine].launch({ headless: true });
      version = browser.version();
      page = await browser.newPage({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' });
      page.setDefaultTimeout(20000);
      page.on('pageerror', (error) => pageErrors.push(error.message));
      await page.route('**/*', handle);

      await page.goto(base);
      await box().waitFor();
      await page.waitForFunction(() => document.querySelector('[data-testid=composer-send]') !== null);

      // 1. A delayed pasted picture plus an immediate Enter and click: nothing may leave until it is attached,
      //    and the turn must carry that picture, not the next draft.
      await box().fill('first draft');
      await dispatchPaste({ html: '<meta charset="utf-8"><img src="/fixture/pasted-a.png">' });
      await pause(80);
      assert.equal(await send().isDisabled(), true, 'send is disabled while the pasted picture is being read');
      await send().evaluate((button) => button.click());
      await box().press('Enter');
      await noTurns(0, 'delayed paste');
      await waitForChips(1);
      assert.equal(await box().inputValue(), 'first draft', 'the words stay in the box while the picture is read');
      await sendAndWait(1);
      assert.equal(fixture.turns[0].body[fields.content], 'first draft');
      assert.deepEqual(fixture.turns[0].body[fields.image_urls], [`${routes.asset_item.replace('{assetID}', 'a1')}`]);
      assert.equal(fixture.uploads[0]?.hash, sha256(PICTURES['/fixture/pasted-a.png']), 'the sent picture is the pasted one');
      checks.push('delayed paste locks send click and Enter; the turn carries the pasted picture');

      // 2. A picture that cannot be read: words stay, the failure is shown, the lock is released and the next
      //    Enter sends only the words.
      await box().fill('keep this text');
      fixture.status['/fixture/pasted-forbidden.png'] = 403;
      await dispatchPaste({ html: '<img src="/fixture/pasted-forbidden.png">' });
      await pause(80);
      assert.equal(await send().isDisabled(), true, 'send is disabled while the bad picture is being read');
      await box().press('Enter');
      await noTurns(1, 'unreadable paste', 400);
      await page.waitForFunction(() => document.querySelector('[data-testid=composer-send]')?.disabled === false, null, { timeout: 15000 });
      await page.waitForFunction(() => document.querySelectorAll('[data-sonner-toast]').length > 0, null, { timeout: 15000 });
      const unreadable = messages['ui.chat.input.paste.image_unreadable'].replace('{count}', '1');
      assert((await page.locator('[data-sonner-toast]').allTextContents()).some((text) => text.includes(unreadable)));
      assert.equal(await box().inputValue(), 'keep this text', 'a failed read keeps the words');
      assert.equal(await chips().count(), 0, 'a failed read attaches nothing');
      await sendAndWait(2);
      assert.equal(fixture.turns[1].body[fields.content], 'keep this text');
      assert.equal(fixture.turns[1].body[fields.image_urls], undefined, 'the turn carries no picture after the failed read');
      checks.push('failed read keeps words, shows the unreadable count, attaches nothing and unlocks');

      // 3. Multiple pastes: the first picture finishing must not unlock the send while the second is still read.
      await box().fill('two pictures');
      fixture.delay['/fixture/pasted-a.png'] = 300;
      fixture.delay['/fixture/pasted-b.png'] = 1500;
      await dispatchPaste({ html: '<img src="/fixture/pasted-a.png">' });
      await pause(80);
      await dispatchPaste({ html: '<img src="/fixture/pasted-b.png">' });
      await noTurns(2, 'multiple pastes');
      await pause(500);
      await box().press('Enter');
      await noTurns(2, 'one of two pastes ready', 300);
      await waitForChips(2);
      await sendAndWait(3);
      assert.equal(fixture.turns[2].body[fields.content], 'two pictures');
      assert.equal(fixture.turns[2].body[fields.image_urls].length, 2);
      assert.deepEqual(
        [fixture.uploads.at(-2)?.hash, fixture.uploads.at(-1)?.hash],
        [sha256(PICTURES['/fixture/pasted-a.png']), sha256(PICTURES['/fixture/pasted-b.png'])],
      );
      checks.push('multiple pastes stay locked until both pictures are ready');

      // 4. A paste started in A finishes after A -> B -> A: it must not attach to the new visit of A (or B),
      //    and a late failure must not show a toast in another session.
      await box().fill('A draft');
      fixture.delay['/fixture/pasted-a.png'] = 1400;
      await dispatchPaste({ html: '<img src="/fixture/pasted-a.png">' });
      await switchSession('s2');
      await box().fill('B draft');
      await switchSession('s1');
      await page.waitForFunction(() => document.querySelector('[data-testid=studio-composer] textarea')?.value && document.querySelector('[data-testid=studio-composer] textarea').value === 'A draft', null, { timeout: 10000 });
      await box().fill('A again');
      const toastsBeforeLate = await toastCount();
      await pause(1800);
      assert.equal(await chips().count(), 0, 'a paste from the previous visit of A does not attach to the new draft');
      assert.equal(await box().inputValue(), 'A again');
      assert.equal(await toastCount(), toastsBeforeLate, 'no late toast after the draft was replaced');

      await box().fill('A failed draft');
      fixture.delay['/fixture/pasted-forbidden.png'] = 1200;
      fixture.status['/fixture/pasted-forbidden.png'] = 403;
      await dispatchPaste({ html: '<img src="/fixture/pasted-forbidden.png">' });
      await switchSession('s2');
      await box().fill('B stays');
      const toastsBeforeCross = await toastCount();
      await pause(1600);
      assert.equal(await chips().count(), 0, 'the failed paste does not attach in B');
      assert.equal(await box().inputValue(), 'B stays');
      assert.equal(await toastCount(), toastsBeforeCross, 'a failed read from A does not toast in B');
      checks.push('A-B-A and late failure stay in their own session visit');

      // 5. A draft replaced while its picture is being read: the failed read must not report into the new draft.
      await switchSession('s1');
      await box().fill('old draft');
      const toastsBeforePrefill = await toastCount();
      fixture.delay['/fixture/pasted-forbidden.png'] = 1000;
      fixture.status['/fixture/pasted-forbidden.png'] = 403;
      await dispatchPaste({ html: '<img src="/fixture/pasted-forbidden.png">' });
      await page.evaluate(() => window.__studioStore.getState().prefillComposer('replacement draft'));
      await pause(1400);
      assert.equal(await box().inputValue(), 'replacement draft', 'the prefill replaces the draft');
      assert.equal(await chips().count(), 0, 'the old read does not attach to the replacement draft');
      assert.equal(await toastCount(), toastsBeforePrefill, 'the old read does not toast into the replacement draft');
      checks.push('prefill replaces the draft; the stale read adds no chip or toast');

      // 6. A native picture file on the clipboard still takes the usual immediate upload path.
      await box().fill('native file');
      const detached = await chips().count();
      const native = PICTURES['/fixture/pasted-native.png'].toString('base64');
      await dispatchPaste({ file: { name: 'native.png', type: 'image/png', bytes: native } });
      await waitForChips(detached + 1);
      await sendAndWait(4);
      assert.equal(fixture.turns[3].body[fields.content], 'native file');
      assert.equal(fixture.uploads.at(-1)?.hash, sha256(PICTURES['/fixture/pasted-native.png']));
      checks.push('native file paste uploads immediately and is sent with the words');

      // 7. Plain text is not handled: the browser's own paste stays responsible and nothing is locked.
      await box().fill('plain words');
      await dispatchPaste({ text: 'plain words' });
      assert.equal(await page.evaluate(() => window.__lastPastePrevented), false, 'plain text is left to the text box');
      assert.equal(await chips().count(), 0, 'plain text adds no attachment');
      assert.equal(await send().isDisabled(), false);
      checks.push('plain text paste is left to the text box and adds no attachment');

      // 8. A turn accepted in A while B is still reading a pasted picture must not invalidate B's read:
      //    B keeps its own words and sends the intended bytes, not only the text.
      await switchSession('s1');
      await box().fill('A waits for acceptance');
      const s1Turns = fixture.turns.filter((turn) => turn.sessionId === 's1').length;
      let releaseA;
      fixture.turnHold.s1 = new Promise((resolve) => { releaseA = resolve; });
      await send().click();
      await waitTurn('s1', s1Turns + 1, false);
      await switchSession('s2');
      await box().fill('B keeps its picture');
      fixture.delay['/fixture/pasted-b.png'] = 1600;
      await dispatchPaste({ html: '<img src="/fixture/pasted-b.png">' });
      await pause(100);
      releaseA();
      const acceptedA = await waitTurn('s1', s1Turns + 1, true);
      await waitForChips(1);
      const readB = fixture.pasteCompletions.filter((item) => item.path === '/fixture/pasted-b.png').at(-1);
      assert(readB && acceptedA.acceptedAt <= readB.completedAt, 'A is accepted while B is still reading the picture');
      assert.equal(await box().inputValue(), 'B keeps its picture', 'B keeps its own words');
      await sendAndWait(6);
      const turnB = fixture.turns.filter((turn) => turn.sessionId === 's2').at(-1);
      const uploadB = fixture.uploads.at(-1);
      assert.equal(turnB.body[fields.content], 'B keeps its picture');
      assert.equal(uploadB.hash, sha256(PICTURES['/fixture/pasted-b.png']), 'B sends the pasted bytes');
      assert.deepEqual(turnB.body[fields.image_urls], [routes.asset_item.replace('{assetID}', uploadB.id)]);
      checks.push('A acceptance during B paste keeps B words and pasted bytes');

      assert.deepEqual(fixture.unexpectedWrites, [], 'no unexpected write');
      const narrow = await checkNarrow(engine, browser, pageErrors);
      checks.push(`narrow ${narrow.viewport}px window renders the composer without horizontal overflow`);
      assert.deepEqual(fixture.external, [], 'no request leaves the loopback origin');
      assert.deepEqual(pageErrors, [], 'no page errors');
      results.push({
        engine, version, viewport: '1440x900', narrow, passed: true, ms: Date.now() - started, checks,
        turns: fixture.turns.length, uploads: fixture.uploads, pastedPictures: fixture.pasteRequests,
        external: fixture.external.length, unexpectedWrites: fixture.unexpectedWrites.length, pageErrors: pageErrors.length,
      });
      console.log(`PASS ${engine} ${version}`);
    } catch (error) {
      await page?.screenshot({ path: path.join(out, `failure-${engine}.png`), fullPage: true }).catch(() => {});
      results.push({
        engine, version, viewport: '1440x900', passed: false, ms: Date.now() - started, checks,
        error: String(error?.message ?? error).split('\n')[0],
        turns: fixture.turns.length, uploads: fixture.uploads, pageErrors,
        external: fixture.external, unexpectedWrites: fixture.unexpectedWrites,
      });
      console.error(`FAIL ${engine}${version ? ` ${version}` : ''}`);
      console.error(error);
    } finally {
      await browser?.close();
    }
  }

  writeFileSync(path.join(out, 'report.json'), JSON.stringify({
    scope: 'local mounted ChatColumn with fixture HTTP; no deployed, Provider or Agent execution claims',
    viewport: '1440x900',
    narrowViewport: '640x720',
    engines: results,
  }, null, 2), { mode: 0o600 });
  const passed = results.length === ENGINES.length && results.every((item) => item.passed);
  console.log(JSON.stringify({
    passed,
    output: out,
    engines: results.map((item) => ({
      engine: item.engine, version: item.version, passed: item.passed,
      checks: item.checks.length, turns: item.turns, uploads: item.uploads.length,
    })),
  }));
  if (!passed) process.exitCode = 1;
} finally {
  await server.close();
}
