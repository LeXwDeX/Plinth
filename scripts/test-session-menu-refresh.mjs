#!/usr/bin/env node
// Mounted, loopback-only browser regression for #289: the header session menu must show current sessions.
//
// The real SessionMenu, chat browser client, session pages, chat store and SWR run in Chromium and WebKit
// (1440x900). The session API is a fixture served from this process with keyset paging in the same order
// as the server (updated time, then ID, both newest first); nothing leaves the loopback page and no model
// or Provider is called. The fixture checks list freshness, not the full workspace.
//
//   node scripts/test-session-menu-refresh.mjs
//
// Optional env: PLAYWRIGHT_MODULE_PATH (default: the repo's playwright), SESSION_MENU_EVIDENCE_DIR
// (default: a new temporary folder), ENGINES (default "chromium,webkit").
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || 'playwright');
const { application: schema } = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete schema.runtime.configuration;
// The pager summary is declared in the Product Schema in the integrated change; this loopback fixture injects the
// recommended wording so the mounted menu can be checked before the shared catalog is updated.
schema.content.messages['ui.studio.session.pagination_summary'] = '第 {page}/{pages} 页 · 共 {total} 个会话';
schema.content.messages['ui.studio.session.pagination_matches'] = '匹配 {total} 个会话 · 第 {page}/{pages} 页';
const routes = schema.runtime.http;
const fields = schema.chat.api.response_fields;
const pageSize = schema.runtime.frontend.session_menu_page_size;
const debounce = schema.runtime.frontend.session_menu_search_debounce_milliseconds;
const text = (key) => {
  const value = schema.content.messages[key];
  assert.equal(typeof value, 'string', `missing schema message ${key}`);
  return value;
};

const virtualID = '\0session-menu-refresh-fixture';
const fixtureApp = `
  import React, { StrictMode } from 'react';
  import { createRoot } from 'react-dom/client';
  import '${path.join(root, 'src/app/globals.css')}';
  import { ProductSchemaProvider } from '${path.join(root, 'src/components/product-schema-provider.tsx')}';
  import { SessionMenu } from '${path.join(root, 'src/components/studio/session-menu.tsx')}';
  import { useChatStore } from '${path.join(root, 'src/store/chat-store.ts')}';
  const schema = window.__ART_AI_PRODUCT_SCHEMA__;
  const first = window.__FIXTURE_ACTIVE_SESSION__;
  useChatStore.getState().setSessions([first]);
  useChatStore.getState().setActiveSession(first.id);
  window.__chatStore = useChatStore;
  document.documentElement.classList.add('dark');
  createRoot(document.getElementById('root')).render(React.createElement(StrictMode, null,
    React.createElement(ProductSchemaProvider, { initialSchema: schema },
      React.createElement('main', { className: 'p-6' },
        React.createElement('header', { className: 'flex h-12 items-center' }, React.createElement(SessionMenu)),
        React.createElement('p', { 'data-testid': 'outside', className: 'mt-[600px]' }, 'outside')))));
`;

const output = process.env.SESSION_MENU_EVIDENCE_DIR || await mkdtemp(path.join(tmpdir(), 'session-menu-refresh-'));
assert(path.isAbsolute(output), 'evidence directory must be absolute');
await mkdir(output, { recursive: true, mode: 0o700 });

const server = await createServer({
  configFile: false,
  root: path.join(root, 'web'),
  publicDir: path.join(root, 'public'),
  cacheDir: path.join(output, '.vite'),
  resolve: { alias: { '@': path.join(root, 'src') } },
  logLevel: 'error',
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{
    name: 'session-menu-refresh-fixture',
    resolveId(id) { if (id === 'virtual:session-menu-refresh-fixture') return virtualID; },
    load(id) { return id === virtualID ? fixtureApp : null; },
    transformIndexHtml(html) {
      return html.replace('{{.Schema}}', JSON.stringify(schema)).replace('src="/src/main.tsx"', 'src="/@id/virtual:session-menu-refresh-fixture"');
    },
  }],
});
await server.listen();
const base = server.resolvedUrls.local[0];

/** The session store behind the fixture API, ordered like the server's keyset query. */
class SessionServer {
  constructor() {
    this.sessions = [];
    this.clock = Date.parse('2026-10-20T06:00:00Z');
    this.listRequests = [];
    this.deleteRequests = [];
    this.failNextLists = 0;
    this.listDelayMilliseconds = 0;
    this.failNextDeletes = 0;
    this.totalOverride = null;
    this.deleteDelayMilliseconds = 0;
  }
  tick() { this.clock += 60_000; return new Date(this.clock).toISOString(); }
  add(title, id = `session-${this.sessions.length + 1}-${title}`) {
    const at = this.tick();
    const session = { id, title, createdAt: at, updatedAt: at };
    this.sessions.push(session);
    return session;
  }
  touch(id) { this.find(id).updatedAt = this.tick(); }
  rename(id, title) { this.find(id).title = title; }
  remove(id) { this.sessions = this.sessions.filter((item) => item.id !== id); }
  find(id) { const session = this.sessions.find((item) => item.id === id); assert(session, `no session ${id}`); return session; }
  ordered() {
    return [...this.sessions].sort((a, b) => (a.updatedAt === b.updatedAt ? (a.id < b.id ? 1 : -1) : a.updatedAt < b.updatedAt ? 1 : -1));
  }
  page(search, cursor, limit) {
    let rows = this.ordered().filter((item) => item.title.toLowerCase().includes(search.trim().toLowerCase()));
    // The server counts the owner-visible matches before applying the cursor.
    const total = rows.length;
    if (cursor) {
      const [updatedAt, id] = JSON.parse(Buffer.from(cursor, 'base64url').toString());
      rows = rows.filter((item) => item.updatedAt < updatedAt || (item.updatedAt === updatedAt && item.id < id));
    }
    const shown = rows.slice(0, limit);
    const last = shown[shown.length - 1];
    const next = rows.length > limit ? Buffer.from(JSON.stringify([last.updatedAt, last.id])).toString('base64url') : '';
    return { sessions: shown.map(wire), next_cursor: next, total: this.totalOverride ?? total };
  }
}
const wire = (session) => ({
  [fields.id]: session.id,
  [fields.title]: session.title,
  [fields.created_at]: session.createdAt,
  [fields.updated_at]: session.updatedAt,
});

const sessionPath = (pathname) => {
  const prefix = routes.legacy_routes.chat_sessions + '/';
  return pathname.startsWith(prefix) && !pathname.slice(prefix.length).includes('/') ? decodeURIComponent(pathname.slice(prefix.length)) : null;
};

async function openPage(browser, data) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' });
  page.setDefaultTimeout(15_000);
  const problems = [];
  page.on('pageerror', (error) => problems.push(`pageerror:${error.message}`));
  const active = data.ordered()[0];
  await page.addInitScript(({ initialSchema, session }) => {
    window.__ART_AI_PRODUCT_SCHEMA__ = initialSchema;
    window.__FIXTURE_ACTIVE_SESSION__ = session;
  }, { initialSchema: schema, session: { id: active.id, title: active.title, createdAt: active.createdAt, updatedAt: active.updatedAt } });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== new URL(base).origin) { problems.push(`external:${url.origin}`); return route.abort(); }
    if (!url.pathname.startsWith('/api/')) return route.continue();
    const method = request.method();
    if (url.pathname === routes.routes.product_schema) return route.fulfill({ json: { schema } });
    if (url.pathname === routes.legacy_routes.version) return route.fulfill({ json: { version: 'fixture' } });
    if (url.pathname === routes.routes.tasks) return route.fulfill({ json: { tasks: [] } });
    if (url.pathname === routes.legacy_routes.chat_sessions && method === 'GET') {
      const search = url.searchParams.get('search') ?? '';
      const cursor = url.searchParams.get('cursor') ?? '';
      data.listRequests.push({ search, cursor });
      if (data.listDelayMilliseconds > 0) await new Promise((resolve) => setTimeout(resolve, data.listDelayMilliseconds));
      if (data.failNextLists > 0) {
        data.failNextLists -= 1;
        return route.fulfill({ status: 500, json: { [fields.error]: 'fixture failure' } });
      }
      return route.fulfill({ json: data.page(search, cursor, Number(url.searchParams.get('limit'))) });
    }
    if (url.pathname === routes.legacy_routes.chat_sessions && method === 'POST') {
      // The real server stores the Schema default title when the client sends an empty one.
      const title = JSON.parse(request.postData() || '{}')[fields.title];
      return route.fulfill({ status: 201, json: wire(data.add(title || schema.chat.defaults.title)) });
    }
    const id = sessionPath(url.pathname);
    if (id !== null && method === 'GET') return route.fulfill({ json: wire(data.find(id)) });
    if (id !== null && method === 'PATCH') {
      data.rename(id, JSON.parse(request.postData() || '{}')[fields.title]);
      return route.fulfill({ json: wire(data.find(id)) });
    }
    if (id !== null && method === 'DELETE') {
      data.deleteRequests.push(id);
      if (data.failNextDeletes > 0) {
        data.failNextDeletes -= 1;
        return route.fulfill({ status: 500, json: { [fields.error]: 'fixture delete failure' } });
      }
      if (data.deleteDelayMilliseconds > 0) await new Promise((resolve) => setTimeout(resolve, data.deleteDelayMilliseconds));
      data.remove(id);
      return route.fulfill({ status: 204 });
    }
    problems.push(`unexpected:${method} ${url.pathname}`);
    return route.fulfill({ status: 404, json: {} });
  });
  await page.goto(base);
  await page.getByTestId('studio-session-menu').waitFor();
  return { page, problems };
}

const rows = (page) => page.getByTestId('studio-session-row');
async function shownTitles(page) {
  return rows(page).evaluateAll((items) => items.map((item) => item.querySelector('button span span')?.textContent ?? ''));
}
/** Wait until the menu has settled on exactly these titles, in order, with no read in flight. */
async function expectTitles(page, titles, message) {
  try {
    await page.waitForFunction(
      (expected) => {
        if (document.querySelector('[data-testid="studio-session-loading"]')) return false;
        const shown = [...document.querySelectorAll('[data-testid="studio-session-row"]')]
          .map((item) => item.querySelector('button span span')?.textContent ?? '');
        return JSON.stringify(shown) === JSON.stringify(expected);
      },
      titles,
      { timeout: 10_000 },
    );
  } catch {
    assert.deepEqual(await shownTitles(page), titles, message);
  }
}
async function openMenu(page) {
  await page.getByTestId('studio-session-menu').click();
  await page.getByRole('searchbox').waitFor();
}
async function closeMenu(page) {
  await page.keyboard.press('Escape');
  await page.getByRole('searchbox').waitFor({ state: 'detached' });
}
async function search(page, term) {
  await page.getByRole('searchbox').fill(term);
  await page.waitForTimeout(debounce + 50);
}
const settle = (page) => page.waitForTimeout(150);

const report = [];
const engines = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
try {
  for (const engine of engines) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      // 1. The case from the issue: one cached session, a second one created from the menu, reopened after
      //    SWR's 2 s dedupe window, then a search and a cleared search.
      {
        const data = new SessionServer();
        data.add('逆水寒风男性角色', 'session-a');
        const { page, problems } = await openPage(browser, data);
        await openMenu(page);
        await expectTitles(page, ['逆水寒风男性角色'], 'first open');
        assert.equal(data.listRequests.length, 1, 'first open reads one page');
        await page.getByTestId('studio-session-new').click();
        await page.getByRole('searchbox').waitFor({ state: 'detached' });
        const created = data.ordered()[0];
        data.rename(created.id, '细化角色原画结构与材质');
        await page.waitForTimeout(2300);
        await openMenu(page);
        await expectTitles(page, ['细化角色原画结构与材质', '逆水寒风男性角色'], 'reopen after create shows both');
        await search(page, '细化');
        await expectTitles(page, ['细化角色原画结构与材质'], 'Chinese search');
        assert.equal(await page.getByTestId('studio-session-page').innerText(), '匹配 1 个会话 · 第 1/1 页', 'search footer uses the matches wording');
        await search(page, '不存在');
        await expectTitles(page, [], 'empty search result');
        await page.getByText(text('ui.studio.session.empty'), { exact: true }).waitFor();
        assert.equal(await page.getByTestId('studio-session-page').innerText(), '匹配 0 个会话 · 第 1/1 页', 'empty search footer');
        data.add('另一标签页新建');
        await search(page, '');
        await expectTitles(page, ['另一标签页新建', '细化角色原画结构与材质', '逆水寒风男性角色'], 'cleared search reads the list again');
        await closeMenu(page);
        // Another tab adds a session: reopening shows it at once.
        data.add('跨标签页');
        await openMenu(page);
        await expectTitles(page, ['跨标签页', '另一标签页新建', '细化角色原画结构与材质', '逆水寒风男性角色'], 'reopen shows another tab\'s session');
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: create from menu, reopen after dedupe window, search/empty/clear, other tab`);
      }

      // 2. Renames (header pencil and automatic title), a finished turn and deletes keep the list current,
      //    including while the menu stays open.
      {
        const data = new SessionServer();
        data.add('旧会话', 'session-old');
        data.add('当前会话', 'session-current');
        const { page, problems } = await openPage(browser, data);
        await openMenu(page);
        await expectTitles(page, ['当前会话', '旧会话'], 'initial list');
        await closeMenu(page);
        await page.getByTestId('studio-session-rename').click();
        await page.getByRole('textbox', { name: text('ui.workspace.session.rename') }).fill('手动改名');
        await page.keyboard.press('Enter');
        await page.getByTestId('studio-session-name').getByText('手动改名', { exact: true }).waitFor();
        await openMenu(page);
        await expectTitles(page, ['手动改名', '旧会话'], 'header rename shows in the menu');
        // The automatic title arrives while the menu is open.
        data.rename('session-old', '自动标题');
        await page.evaluate(() => window.__chatStore.getState().updateSessionTitle('session-old', '自动标题'));
        await expectTitles(page, ['手动改名', '自动标题'], 'automatic title while open');
        // A turn in the older session ends while the menu is open: it moves to the top.
        data.touch('session-old');
        await page.evaluate(() => window.__chatStore.getState().finishStreaming('session-old'));
        await expectTitles(page, ['自动标题', '手动改名'], 'finished turn reorders while open');
        // Delete the session that is not current.
        await page.getByRole('button', { name: `${text('ui.studio.session.delete')}：自动标题` }).click();
        await page.getByRole('button', { name: text('ui.studio.session.delete_action') }).click();
        await expectTitles(page, ['手动改名'], 'delete non-current');
        await closeMenu(page);
        await openMenu(page);
        await expectTitles(page, ['手动改名'], 'reopen after delete');
        // Delete the current session: the menu creates a new one since none is left.
        await page.getByRole('button', { name: `${text('ui.studio.session.delete')}：手动改名` }).click();
        await page.getByRole('button', { name: text('ui.studio.session.delete_action') }).click();
        await page.getByRole('searchbox').waitFor({ state: 'detached' });
        await page.getByTestId('studio-session-name').getByText(schema.chat.defaults.title, { exact: true }).waitFor();
        await openMenu(page);
        await expectTitles(page, [schema.chat.defaults.title], 'delete current creates a new one');
        assert.equal(await page.getByTestId('studio-session-page').innerText(), '第 1/1 页 · 共 1 个会话', 'one session is one page');
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: header rename, automatic title and finished turn while open, delete non-current/current`);
      }

      // 3. More than one page: turning back reuses loaded pages, a change while open keeps the page and its
      //    cursor right with no repeats or gaps, a reopen starts again from page 1 and a failed read keeps a retry.
      {
        const data = new SessionServer();
        for (let index = 1; index <= pageSize + 5; index += 1) data.add(`会话${String(index).padStart(2, '0')}`, `session-${String(index).padStart(2, '0')}`);
        const expected = () => data.ordered().map((item) => item.title);
        const { page, problems } = await openPage(browser, data);
        await openMenu(page);
        await expectTitles(page, expected().slice(0, pageSize), 'page 1');
        await page.getByTestId('studio-session-next').click();
        await expectTitles(page, expected().slice(pageSize), 'page 2');
        const turned = data.listRequests.length;
        await page.getByTestId('studio-session-previous').click();
        await expectTitles(page, expected().slice(0, pageSize), 'back to page 1');
        await page.getByTestId('studio-session-next').click();
        await expectTitles(page, expected().slice(pageSize), 'page 2 again');
        await settle(page);
        assert.equal(data.listRequests.length, turned, 'turning back to loaded pages sends no request');
        // A turn ends in a session on page 2 while page 2 is shown: it moves to page 1, page 2 shifts.
        const moved = data.ordered()[pageSize + 2];
        data.touch(moved.id);
        await page.evaluate((id) => window.__chatStore.getState().finishStreaming(id), moved.id);
        await expectTitles(page, expected().slice(pageSize), 'page 2 after the change');
        assert.equal(await page.getByTestId('studio-session-page').innerText(), `第 2/2 页 · 共 ${pageSize + 5} 个会话`);
        await page.getByTestId('studio-session-previous').click();
        await expectTitles(page, expected().slice(0, pageSize), 'page 1 after the change');
        assert.equal((await shownTitles(page))[0], moved.title);
        await closeMenu(page);
        // Reopen: page 1 again, one request.
        const beforeReopen = data.listRequests.length;
        await openMenu(page);
        await expectTitles(page, expected().slice(0, pageSize), 'reopen at page 1');
        await settle(page);
        assert.deepEqual(data.listRequests.slice(beforeReopen), [{ search: '', cursor: '' }], 'reopen reads only page 1');
        await closeMenu(page);
        // A failed read on reopen keeps the old rows with an error and retry, then retry shows current rows.
        data.add('失败后新增');
        data.failNextLists = 1;
        await openMenu(page);
        await page.getByTestId('studio-session-retry').waitFor();
        assert(!(await shownTitles(page)).includes('失败后新增'), 'old rows stay while the read failed');
        assert.equal(await page.getByTestId('studio-session-page').innerText(), text('ui.studio.session.load_failed'), 'failed read shows no confirmed total');
        await page.getByTestId('studio-session-retry').getByRole('button').click();
        await expectTitles(page, expected().slice(0, pageSize), 'retry shows current rows');
        await page.screenshot({ path: path.join(output, `${engine}-paging.png`) });
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: paging budget, change while on page 2, reopen at page 1, failure and retry`);
      }
      // 4. #306: the footer counts every owner-visible match, not the loaded rows, across empty, one page,
      //    several pages, search and clearing, create/rename/delete, and deleting the last row of the last page.
      {
        const data = new SessionServer();
        for (let index = 1; index <= pageSize + 1; index += 1) data.add(`会话${String(index).padStart(2, '0')}`, `session-${String(index).padStart(2, '0')}`);
        const expected = () => data.ordered().map((item) => item.title);
        const { page, problems } = await openPage(browser, data);
        const footer = () => page.getByTestId('studio-session-page').innerText();
        await openMenu(page);
        assert.equal(await footer(), `第 1/2 页 · 共 ${pageSize + 1} 个会话`, 'first page total');
        assert(await page.getByTestId('studio-session-previous').isDisabled(), 'previous on page 1');
        assert(!(await page.getByTestId('studio-session-next').isDisabled()), 'next on page 1');
        await page.getByTestId('studio-session-next').click();
        await expectTitles(page, expected().slice(pageSize), 'last page');
        assert.equal(await footer(), `第 2/2 页 · 共 ${pageSize + 1} 个会话`, 'last page keeps the full total');
        // Delete the only row on the last page: the pager falls back to page 1/1 and the new total.
        await page.getByRole('button', { name: `${text('ui.studio.session.delete')}：${expected()[pageSize]}` }).click();
        await page.getByTestId('studio-session-delete-confirm-action').click();
        await expectTitles(page, expected().slice(0, pageSize), 'last row deleted');
        assert.equal(await footer(), `第 1/1 页 · 共 ${pageSize} 个会话`, 'no page 2 after the last row is deleted');
        assert(await page.getByTestId('studio-session-next').isDisabled(), 'next on the only page');
        // Search counts matches, and an empty result is 1/1 with zero, never 1/0.
        await search(page, '会话1');
        await expectTitles(page, expected().filter((title) => title.includes('会话1')), 'search matches');
        assert.equal(await footer(), '匹配 7 个会话 · 第 1/1 页', 'search total');
        await search(page, '不存在');
        await expectTitles(page, [], 'empty search');
        await page.getByText(text('ui.studio.session.empty'), { exact: true }).waitFor();
        assert.equal(await footer(), '匹配 0 个会话 · 第 1/1 页', 'empty result');
        assert(await page.getByTestId('studio-session-previous').isDisabled(), 'previous with no result');
        assert(await page.getByTestId('studio-session-next').isDisabled(), 'next with no result');
        // A create, a rename out of the search and a delete all change the count after the debounce.
        await search(page, '会话1');
        data.add('会话10 新建', 'session-new');
        await page.evaluate((session) => window.__chatStore.getState().addSession(session), data.find('session-new'));
        await expectTitles(page, expected().filter((title) => title.includes('会话1')), 'created session matches');
        assert.equal(await footer(), '匹配 8 个会话 · 第 1/1 页', 'create raises the search total');
        data.rename('session-10', '改名后');
        await page.evaluate(() => window.__chatStore.getState().updateSessionTitle('session-10', '改名后'));
        await expectTitles(page, expected().filter((title) => title.includes('会话1')), 'rename leaves the search');
        assert.equal(await footer(), '匹配 7 个会话 · 第 1/1 页', 'rename lowers the search total');
        await page.getByRole('button', { name: `${text('ui.studio.session.delete')}：会话11` }).click();
        await page.getByTestId('studio-session-delete-confirm-action').click();
        await expectTitles(page, expected().filter((title) => title.includes('会话1')), 'delete leaves the search');
        assert.equal(await footer(), '匹配 6 个会话 · 第 1/1 页', 'delete lowers the search total');
        await search(page, '');
        await expectTitles(page, expected().slice(0, pageSize), 'cleared search');
        assert.equal(await footer(), '第 1/1 页 · 共 15 个会话', 'cleared search counts every session');
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: pager total for empty, one page, several pages, search, create/rename/delete, last-row delete`);
      }

      // 5. #304: the title area is one fixed width. Short, long, Chinese and English titles and the rename field
      //    keep the pencil, the menu arrow and the popover anchor in place; long titles truncate with the full text.
      {
        const data = new SessionServer();
        data.add('这是非常非常长的中文会话标题用于验证顶栏截断和铅笔箭头位置稳定', 'zh');
        data.add('A very long English session title used to check the header truncation and anchor', 'en');
        data.add('短', 'short');
        data.add('Medium title', 'mid');
        const { page, problems } = await openPage(browser, data);
        const menu = page.getByTestId('studio-session-menu');
        const rename = page.getByTestId('studio-session-rename');
        const name = page.getByTestId('studio-session-name');
        const header = async (nameArea = name) => ({
          menu: await menu.boundingBox(),
          rename: await rename.boundingBox(),
          name: await nameArea.boundingBox(),
        });
        const near = (a, b) => Math.abs(a - b) < 0.5;
        const initial = await header();
        for (const [id, title] of [['zh', data.find('zh').title], ['en', data.find('en').title], ['short', '短'], ['mid', 'Medium title']]) {
          await page.evaluate((sessionID) => window.__chatStore.getState().setActiveSession(sessionID), id);
          await page.waitForFunction((expected) => document.querySelector('[data-testid="studio-session-name"]')?.textContent === expected, title);
          const current = await header();
          assert(near(current.menu.x, initial.menu.x) && near(current.rename.x, initial.rename.x), `${id}: pencil and arrow stay put`);
          assert(near(current.name.width, initial.name.width), `${id}: name area keeps its width`);
        }
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('zh'));
        await page.waitForFunction(() => document.querySelector('[data-testid="studio-session-name"]')?.textContent === '这是非常非常长的中文会话标题用于验证顶栏截断和铅笔箭头位置稳定');
        const truncated = await name.evaluate((element) => ({ scroll: element.scrollWidth, client: element.clientWidth, text: element.textContent }));
        assert(truncated.scroll > truncated.client, 'long Chinese title is visually truncated');
        assert.equal(truncated.text, '这是非常非常长的中文会话标题用于验证顶栏截断和铅笔箭头位置稳定', 'the stored title is complete');
        // Rename keeps the same header slots; Escape cancels without saving.
        await rename.click();
        const field = page.getByRole('textbox', { name: text('ui.workspace.session.rename') });
        await field.waitFor();
        const editing = await header(field);
        assert(near(editing.menu.x, initial.menu.x), 'rename does not move the menu arrow');
        const fieldBox = await field.boundingBox();
        assert(near(fieldBox.x, initial.name.x), 'rename field starts where the name was');
        assert(near(fieldBox.width, initial.name.width), 'rename field keeps the name area width');
        await page.keyboard.press('Escape');
        await name.waitFor();
        assert(near((await menu.boundingBox()).x, initial.menu.x), 'cancel keeps the arrow in place');
        // The popover anchor is the menu button: its content starts at the same x for every title and mode.
        const anchorX = async () => {
          await openMenu(page);
          const box = await page.getByRole('searchbox').boundingBox();
          await closeMenu(page);
          return box.x;
        };
        const firstAnchor = await anchorX();
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('en'));
        assert(near(await anchorX(), firstAnchor), 'popover anchor stays put for a long English title');
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('short'));
        assert(near(await anchorX(), firstAnchor), 'popover anchor stays put for a short title');
        // The full title is available through the shared hover hint, not a native title attribute.
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('zh'));
        await name.hover();
        const fullHint = page.getByTestId('hint-name').filter({ hasText: '这是非常非常长的中文会话标题用于验证顶栏截断和铅笔箭头位置稳定' });
        await fullHint.waitFor();
        assert.equal(await fullHint.innerText(), '这是非常非常长的中文会话标题用于验证顶栏截断和铅笔箭头位置稳定', 'hover shows the full title');
        await page.mouse.move(1, 1);
        // A wide PC window keeps the header inside the viewport.
        await page.setViewportSize({ width: 1920, height: 1080 });
        const wide = await header();
        assert(wide.menu.x + wide.menu.width <= 1920, 'menu stays inside a wide window');
        // A narrower PC window does not overflow the header.
        await page.setViewportSize({ width: 1024, height: 768 });
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('zh'));
        const narrow = await header();
        const viewport = page.viewportSize();
        assert(narrow.menu.x + narrow.menu.width <= viewport.width, 'menu stays inside a narrow window');
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'no horizontal overflow');
        await openMenu(page);
        assert((await page.getByTestId('studio-session-pager').boundingBox()).x + (await page.getByTestId('studio-session-pager').boundingBox()).width <= viewport.width, 'pager stays inside');
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: fixed title width, short/long/Chinese/English, rename slots, popover anchor, 1024 viewport`);
      }

      // 6. #305: Enter confirms, Escape cancels and restores focus, the row keeps the mouse path, a failed delete
      //    stays retryable, and a held delete accepts only one submission.
      {
        const data = new SessionServer();
        data.add('保留我', 'keep');
        data.add('删除我', 'del');
        data.add('重复删除', 'repeat');
        const { page, problems } = await openPage(browser, data);
        // Keep a session other than the rows being deleted current, so deleting them does not close the menu.
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('del'));
        await page.waitForFunction(() => document.querySelector('[data-testid="studio-session-name"]')?.textContent === '删除我');
        await openMenu(page);
        const deleteButton = (title) => page.getByRole('button', { name: `${text('ui.studio.session.delete')}：${title}` });
        const confirm = page.getByTestId('studio-session-delete-confirm-action');
        await deleteButton('保留我').click();
        await confirm.waitFor();
        assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('data-testid')), 'studio-session-delete-confirm-action', 'confirm is focused');
        await page.keyboard.press('Escape');
        await confirm.waitFor({ state: 'detached' });
        assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('aria-label')), `${text('ui.studio.session.delete')}：保留我`, 'Escape returns focus to the delete button');
        assert.equal(data.deleteRequests.length, 0, 'Escape did not delete');
        // The mouse path (delete then the confirm button) still works.
        await deleteButton('保留我').click();
        await confirm.click();
        await expectTitles(page, ['重复删除', '删除我'], 'mouse delete');
        assert.equal(data.deleteRequests.length, 1, 'mouse path sends one delete');
        // A held delete ignores a second Enter while it is in flight.
        data.deleteDelayMilliseconds = 300;
        await deleteButton('重复删除').click();
        await confirm.press('Enter');
        await confirm.press('Enter');
        await page.waitForTimeout(400);
        assert.equal(data.deleteRequests.filter((id) => id === 'repeat').length, 1, 'one delete while in flight');
        await expectTitles(page, ['删除我'], 'held delete finished');
        data.deleteDelayMilliseconds = 0;
        // Two clicks dispatched in one task (React has not rendered the disabled state yet) still send one
        // delete: the guard is a ref, not the button state.
        data.add('同tick删除', 'same-tick');
        await page.evaluate((session) => window.__chatStore.getState().addSession(session), data.find('same-tick'));
        await expectTitles(page, ['同tick删除', '删除我'], 'same-tick row appears');
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('del'));
        data.deleteDelayMilliseconds = 300;
        await deleteButton('同tick删除').click();
        await page.evaluate(() => {
          const button = document.querySelector('[data-testid="studio-session-delete-confirm-action"]');
          button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
          button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        });
        await page.waitForTimeout(400);
        assert.equal(data.deleteRequests.filter((id) => id === 'same-tick').length, 1, 'same-tick clicks send one delete');
        await expectTitles(page, ['删除我'], 'same-tick delete finished');
        data.deleteDelayMilliseconds = 0;
        // A failed delete keeps the confirmation and the row; Enter tries again.
        data.add('失败重试', 'retry');
        await page.evaluate((session) => window.__chatStore.getState().addSession(session), data.find('retry'));
        await expectTitles(page, ['失败重试', '删除我'], 'retry row appears');
        // addSession made the new row current; keep the row under test away from the current one so deleting it
        // does not close the menu.
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('del'));
        data.failNextDeletes = 1;
        await deleteButton('失败重试').click();
        await confirm.press('Enter');
        await expectTitles(page, ['失败重试', '删除我'], 'failed delete keeps the row');
        // The confirmation stays attached and the same button becomes available again (the toast is the existing
        // feedback path; this fixture has no Toaster mounted).
        await page.waitForFunction(() => {
          const button = document.querySelector('[data-testid="studio-session-delete-confirm-action"]');
          return button instanceof HTMLButtonElement && !button.disabled;
        });
        assert.equal(await confirm.isDisabled(), false, 'failed delete can be retried');
        assert.equal(await page.getByTestId('studio-session-delete-confirm').count(), 1, 'failed delete keeps the confirmation');
        await confirm.press('Enter');
        await expectTitles(page, ['删除我'], 'retry deletes');
        // Escape in the unconfirmed list still closes the menu and does not delete anything.
        await page.keyboard.press('Escape');
        await page.getByRole('searchbox').waitFor({ state: 'detached' });
        assert.equal(data.deleteRequests.filter((id) => id === 'del').length, 0, 'no delete from an unconfirmed Escape');
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: delete confirm Enter/Escape/focus, mouse path, in-flight guard, failure retry`);
      }

      // 7. #306 footer states: no confirmed zero while the read is loading or failed; a large total wraps
      //    completely and keeps both paging buttons reachable.
      {
        const data = new SessionServer();
        data.add('等待加载');
        data.listDelayMilliseconds = 600;
        const { page, problems } = await openPage(browser, data);
        const footer = page.getByTestId('studio-session-page');
        await openMenu(page);
        assert.equal(await footer.innerText(), text('ui.studio.session.loading_more'), 'loading footer');
        assert(!(await footer.innerText()).includes('共'), 'loading footer shows no confirmed zero');
        await expectTitles(page, ['等待加载'], 'loaded after the delay');
        assert.equal(await footer.innerText(), '第 1/1 页 · 共 1 个会话', 'loaded footer');
        await closeMenu(page);
        // The first read fails: no confirmed zero, just the existing error text.
        data.listDelayMilliseconds = 0;
        data.failNextLists = 1;
        await openMenu(page);
        await page.getByTestId('studio-session-retry').waitFor();
        assert.equal(await footer.innerText(), text('ui.studio.session.load_failed'), 'failed footer');
        assert(!(await footer.innerText()).includes('共'), 'failed footer shows no confirmed zero');
        await page.getByTestId('studio-session-retry').getByRole('button').click();
        await expectTitles(page, ['等待加载'], 'retry loaded');
        await page.waitForFunction(() => document.querySelector('[data-testid="studio-session-page"]')?.textContent?.includes('共'));
        assert.equal(await footer.innerText(), '第 1/1 页 · 共 1 个会话', 'retry footer');
        // A large number wraps instead of being truncated; the buttons stay visible.
        data.totalOverride = 123456789012;
        await closeMenu(page);
        await openMenu(page);
        await page.waitForFunction(() => document.querySelector('[data-testid="studio-session-page"]')?.textContent?.includes('123456789012'));
        const longText = await footer.innerText();
        assert(longText.includes('123456789012'), 'large total is complete');
        assert(longText.includes('8230452601'), 'large page count is complete');
        assert(await page.getByTestId('studio-session-previous').isVisible(), 'previous stays reachable');
        assert(await page.getByTestId('studio-session-next').isVisible(), 'next stays reachable');
        assert(await footer.evaluate((element) => element.scrollWidth <= element.clientWidth), 'footer text is not clipped');
        const pagerBox = await page.getByTestId('studio-session-pager').boundingBox();
        const viewport = page.viewportSize();
        assert(pagerBox.x >= 0 && pagerBox.x + pagerBox.width <= viewport.width, 'footer stays inside a narrow window');
        await page.screenshot({ path: path.join(output, `${engine}-footer-large-total.png`) });
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: footer loading/error states and a complete large total`);
      }

      // 8. #305: a delete in flight disables every other delete entry, so a second confirmation cannot be
      //    opened and cleared by the first delete's receipt. A failed delete restores the entries.
      {
        const data = new SessionServer();
        data.add('删除我', 'del');
        data.add('挂起A', 'delete-a');
        data.add('同刻B', 'delete-b');
        const { page, problems } = await openPage(browser, data);
        await page.evaluate(() => window.__chatStore.getState().setActiveSession('del'));
        await page.waitForFunction(() => document.querySelector('[data-testid="studio-session-name"]')?.textContent === '删除我');
        await openMenu(page);
        const deleteButton = (title) => page.getByRole('button', { name: `${text('ui.studio.session.delete')}：${title}` });
        const confirm = page.getByTestId('studio-session-delete-confirm-action');
        data.deleteDelayMilliseconds = 300;
        await deleteButton('挂起A').click();
        await confirm.press('Enter');
        await page.waitForTimeout(50);
        assert(await deleteButton('同刻B').isDisabled(), 'other delete entries are disabled while A is in flight');
        // Even a click that React has not seen as disabled yet must not replace A's confirmation.
        await page.evaluate(() => {
          document.querySelector('[aria-label="删除会话：同刻B"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        });
        const confirmation = page.getByTestId('studio-session-delete-confirm');
        assert.equal(await confirmation.count(), 1, 'only one confirmation stays open');
        assert((await confirmation.innerText()).includes('挂起A'), 'A confirmation is kept');
        await expectTitles(page, ['同刻B', '删除我'], 'A deleted');
        assert(!(await deleteButton('同刻B').isDisabled()), 'delete entries come back after A');
        // A failed delete keeps its confirmation and restores the entries for a retry.
        data.deleteDelayMilliseconds = 0;
        data.failNextDeletes = 1;
        await deleteButton('同刻B').click();
        await confirm.press('Enter');
        await page.waitForFunction(() => {
          const button = document.querySelector('[data-testid="studio-session-delete-confirm-action"]');
          return button instanceof HTMLButtonElement && !button.disabled;
        });
        assert.equal(await page.getByTestId('studio-session-delete-confirm').count(), 1, 'failed delete keeps B confirmation');
        assert((await page.getByTestId('studio-session-delete-confirm').innerText()).includes('同刻B'), 'B confirmation stays');
        assert(!(await deleteButton('删除我').isDisabled()), 'other entries return after the failure');
        await page.keyboard.press('Escape');
        await page.getByTestId('studio-session-delete-confirm').waitFor({ state: 'detached' });
        assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('aria-label')), `${text('ui.studio.session.delete')}：同刻B`, 'Escape returns focus to B');
        await deleteButton('同刻B').click();
        await confirm.press('Enter');
        await expectTitles(page, ['删除我'], 'B retried and deleted');
        assert.equal(data.deleteRequests.filter((id) => id === 'delete-b').length, 2, 'B failed once then succeeded');
        await closeMenu(page);
        assert.deepEqual(problems, []);
        await page.close();
        report.push(`${engine}: in-flight delete locks other entries, failure restores them, focus and retry`);
      }

    } finally {
      await browser.close();
    }
  }
} finally {
  await server.close();
  await writeFile(path.join(output, 'report.json'), JSON.stringify({ scope: 'mounted loopback fixture; no deployment claim', report }, null, 2), { mode: 0o600 });
}
for (const line of report) console.log(`PASS ${line}`);
console.log(`Evidence: ${output}`);
