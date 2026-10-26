// Local browser regression: uses the real SPA and intercepts HTTP with deterministic fixtures.
// Run with node scripts/test-frontend-recovery.mjs; PLAYWRIGHT_MODULE_PATH can select a local install.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
let playwrightPath = process.env.PLAYWRIGHT_MODULE_PATH;
if (!playwrightPath) {
  try { playwrightPath = require.resolve('playwright'); }
  catch { playwrightPath = path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'); }
}
const { chromium } = require(playwrightPath);
const { application: schema } = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete schema.runtime.configuration;
const virtualID = '\0frontend-recovery-fixture';
const server = await createServer({
  configFile: path.join(root, 'vite.go-web.config.mjs'),
  logLevel: 'silent',
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{
    name: 'frontend-recovery-fixture',
    resolveId(id) { if (id === 'virtual:frontend-recovery-fixture') return virtualID; },
    load(id) {
      if (id === virtualID) return `
        import { mutate } from 'swr';
        import { useChatStore } from '${path.join(root, 'src/store/chat-store.ts')}';
        window.frontendRecoveryFixture = { mutate, chatStore: useChatStore };
      `;
    },
    transformIndexHtml(html) {
      return html.replace('{{.Schema}}', JSON.stringify(schema))
        .replace('</body>', '<script type="module" src="/@id/virtual:frontend-recovery-fixture"></script></body>');
    },
  }],
});
await server.listen();
const base = server.resolvedUrls.local[0];
const browser = await chromium.launch({ headless: true });
const failures = [];

async function fixturePage(capability) {
  const page = await browser.newPage();
  const state = { errors: [], taskRoute: '', requests: new Map(), subscriptions: [], finished: new Set() };
  page.on('pageerror', (error) => state.errors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error') state.errors.push(message.text());
  });
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    let json = {};
    if (url.pathname === '/api/v1/schema') json = { schema };
    else if (url.pathname === '/api/auth/session') json = { user: { id: 'admin', name: 'Admin', email: 'test@example.com' }, isAdmin: true };
    else if (url.pathname === '/api/providers') {
      const cap = url.searchParams.get('capability') || capability;
      json = { providers: [{ id: 'test-model', name: 'Test Model', domain: 'image', capabilities: [cap], estimatedCredits: 0,
        paramSchemas: { [cap]: [{ name: 'aspect_ratio', label: 'Aspect ratio', type: 'select', default: '1:1', options: [{ value: '1:1', label: '1:1' }, { value: '16:9', label: '16:9' }] }] },
      }], defaultModel: 'test-model' };
    } else if (/\/history-[ab]$/.test(url.pathname)) {
      state.taskRoute = url.pathname;
      const second = url.pathname.endsWith('history-b');
      json = { id: second ? 'history-b' : 'history-a', status: 'SUCCEEDED', params: {
        prompt: second ? 'second historical prompt' : 'historical prompt', ai_model: 'test-model', aspect_ratio: second ? '1:1' : '16:9',
      } };
    } else if (url.pathname === '/api/version') json = { version: '20260904(1)' };
    else if (url.pathname === '/api/chat/sessions') json = [];
    else if (url.pathname === '/api/canvas/projects') json = [];
    else if (url.pathname.endsWith('/messages')) {
      const id = url.pathname.split('/').at(-2);
      const count = (state.requests.get(id) || 0) + 1;
      state.requests.set(id, count);
      if (count === 1 && id !== 'session-b') await new Promise((resolve) => setTimeout(resolve, 1000));
      const finished = state.finished.has(id) || id === 'session-b';
      json = [{ id: `${id}-message`, role: 'assistant', content: finished ? `completed ${id}` : '__GENERATING__', isPending: !finished, createdAt: '2026-09-04T00:00:00Z' }];
    } else if (url.pathname.endsWith('/stream')) {
      const id = url.pathname.split('/').at(-2);
      state.subscriptions.push(id);
      state.finished.add(id);
      await route.fulfill({ contentType: 'text/event-stream', body: 'data: {"type":"done"}\n\n' });
      return;
    }
    await route.fulfill({ json });
  });
  return { page, state };
}

async function check(name, test) {
  try { await test(); console.log(`PASS ${name}`); }
  catch (error) { failures.push(name); console.error(`FAIL ${name}: ${error.message}`); }
}

try {
  for (const capability of ['text-to-image', 'image-to-image']) {
    await check(`${capability}: history restore, editing, revalidation and task switch`, async () => {
      const { page, state } = await fixturePage(capability);
      try {
        await page.goto(`${base}${capability}?task=history-a`);
        const prompt = page.locator('textarea').first();
        await page.waitForFunction(() => document.querySelector('textarea')?.value === 'historical prompt');
        const ratio = page.getByRole('combobox').filter({ hasText: '16:9' });
        await ratio.waitFor();
        await prompt.fill('edited prompt');
        await ratio.click();
        await page.getByRole('option', { name: '1:1', exact: true }).click();
        await page.evaluate(async (route) => { await window.frontendRecoveryFixture.mutate(route); }, state.taskRoute);
        assert.equal(await prompt.inputValue(), 'edited prompt');
        assert.equal(await page.getByRole('combobox').filter({ hasText: '1:1' }).count(), 1);
        await page.evaluate(() => {
          history.pushState(null, '', '?task=history-b');
          dispatchEvent(new PopStateEvent('popstate'));
        });
        await page.waitForFunction(() => document.querySelector('textarea')?.value === 'second historical prompt');
        assert.equal(await page.getByRole('combobox').filter({ hasText: '1:1' }).count(), 1);
        assert.deepEqual(state.errors, []);
      } finally { await page.close(); }
    });
  }
  for (const route of ['chat', 'canvas']) {
    await check(`${route}: recover after one-second message load and reject late session response`, async () => {
      const { page, state } = await fixturePage(route);
      try {
        await page.goto(`${base}${route}`);
        await page.waitForFunction(() => !!window.frontendRecoveryFixture);
        await page.evaluate(() => window.frontendRecoveryFixture.chatStore.getState().setActiveSession('session-a'));
        await page.getByText('completed session-a', { exact: true }).waitFor({ timeout: 6000 });
        assert.deepEqual(state.subscriptions, ['session-a']);
        await page.evaluate(() => window.frontendRecoveryFixture.chatStore.getState().setActiveSession('session-old'));
        await page.waitForFunction(() => window.frontendRecoveryFixture.chatStore.getState().activeSessionId === 'session-old');
        await page.waitForTimeout(100);
        await page.evaluate(() => window.frontendRecoveryFixture.chatStore.getState().setActiveSession('session-b'));
        await page.getByText('completed session-b', { exact: true }).waitFor({ timeout: 6000 });
        await page.waitForTimeout(1100);
        assert.deepEqual(state.subscriptions, ['session-a']);
        assert.equal(await page.evaluate(() => window.frontendRecoveryFixture.chatStore.getState().messagesBySession['session-old']), undefined);
        assert.deepEqual(state.errors, []);
      } finally { await page.close(); }
    });
  }
} finally {
  await browser.close();
  await server.close();
}
assert.deepEqual(failures, [], 'browser regressions failed');
