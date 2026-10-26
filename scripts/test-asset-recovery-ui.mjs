// Local-only real-browser coverage for task asset recovery. No provider API or task service is called.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, readFile } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const { application: schema } = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete schema.runtime.configuration;
const labels = schema.task_history.asset_recovery;
for (const key of ['action', 'description', 'awaiting_approval', 'approve', 'deny', 'open_recovery_task']) assert.equal(typeof labels[key], 'string');

const virtualID = '\0asset-recovery-fixture';
const fixtureApp = `
  import React, { StrictMode } from 'react';
  import { createRoot } from 'react-dom/client';
  import '${path.join(root, 'src/app/globals.css')}';
  import { ProductSchemaProvider } from '${path.join(root, 'src/components/product-schema-provider.tsx')}';
  import { Toaster } from 'sonner';
  import { TaskRecoveryPanel } from '${path.join(root, 'src/components/result/task-recovery-panel.tsx')}';
  import { useWorkspaceStore } from '${path.join(root, 'src/store/workspace-store.ts')}';
  const schema = window.__RECOVERY_SCHEMA__;
  function Fixture() {
    const [visible, setVisible] = React.useState(true);
    const [identity, setIdentity] = React.useState({ taskId: 'source-1', sessionId: 'session-1' });
    const currentObject = useWorkspaceStore((s) => s.currentObject);
    const pendingPreviewTaskId = useWorkspaceStore((s) => s.pendingPreviewTaskId);
    window.__RECOVERY_STORE__ = () => { const s = useWorkspaceStore.getState(); return { currentObject: s.currentObject, pendingPreviewTaskId: s.pendingPreviewTaskId, trackedTasks: s.trackedTasks }; };
    return React.createElement('main', { className: 'mx-auto flex max-w-3xl flex-col gap-4 p-6', 'data-testid': 'fixture' },
      React.createElement('h1', { className: 'text-lg font-semibold' }, 'Asset recovery'),
      React.createElement('p', { className: 'text-xs text-muted-foreground' }, 'Failed source task · model delivery'),
      React.createElement('button', { 'data-testid': 'leave', onClick: () => setVisible(false) }, 'Close fixture'),
      React.createElement('button', { 'data-testid': 'return', onClick: () => { setIdentity({ taskId: 'source-1', sessionId: 'session-1' }); setVisible(true); } }, 'Return fixture'),
      React.createElement('button', { 'data-testid': 'switch-session', onClick: () => setIdentity({ taskId: 'source-1', sessionId: 'session-2' }) }, 'Switch session fixture'),
      React.createElement('button', { 'data-testid': 'switch-task', onClick: () => setIdentity({ taskId: 'source-2', sessionId: 'session-1' }) }, 'Switch task fixture'),
      React.createElement(Toaster),
      visible && React.createElement(TaskRecoveryPanel, { ...identity, capability: 'text-to-3d', prompt: 'saved prompt',
        onOpenTask: (taskId, sessionId, capability) => { window.__RECOVERY_OPENED__ = { taskId, sessionId, capability }; } }),
      React.createElement('output', { 'data-testid': 'stage-state' }, JSON.stringify({ currentObject, pendingPreviewTaskId })));
  }
  document.documentElement.classList.add('dark');
  createRoot(document.getElementById('root')).render(React.createElement(StrictMode, null,
    React.createElement(ProductSchemaProvider, { initialSchema: schema }, React.createElement(Fixture))));
`;
const output = process.env.ASSET_RECOVERY_UI_EVIDENCE_DIR || path.join(tmpdir(), 'asset-recovery-207-ui');
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
    name: 'asset-recovery-fixture',
    resolveId(id) { if (id === 'virtual:asset-recovery-fixture') return virtualID; },
    load(id) { return id === virtualID ? fixtureApp : null; },
    transformIndexHtml(html) { return html.replace('{{.Schema}}', JSON.stringify(schema)).replace('src="/src/main.tsx"', 'src="/@id/virtual:asset-recovery-fixture"'); },
  }],
});
await server.listen();
const base = server.resolvedUrls.local[0];
const report = [];

async function assertNoExternal(page, state, engine, viewport) {
  await page.waitForTimeout(100);
  assert.deepEqual(state.external, [], `external requests or JS errors at ${engine}/${viewport}`);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `horizontal overflow at ${engine}/${viewport}`);
}

try {
  for (const engine of ['chromium', 'webkit']) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      for (const { width, height } of [{ width: 1366, height: 768 }, { width: 1440, height: 900 }, { width: 1920, height: 1080 }]) {
        const viewport = `${width}x${height}`;
        const page = await browser.newPage({ viewport: { width, height }, serviceWorkers: 'block' });
        const state = { external: [], grantStatus: 'pending', eligible: true, recoveryTaskId: '', prepare: 0, grant: 0, grantDecisions: [], submitted: [], grantKeys: [], prepareBodies: [], postCount: 0 };
        const hold = async (stage) => {
          if (state.holdStage !== stage) return;
          state.held?.();
          await new Promise((resolve) => { state.release = resolve; });
          state.holdStage = null;
        };
        const wireRecovery = () => ({ source_task_id: 'source-1', eligible: true, receipt_digest: 'receipt-digest', session_id: 'session-1', capability: 'asset-recovery', idempotency_key: 'fixed-recovery-key' });
        page.on('pageerror', (error) => { state.external.push(`pageerror:${error.message}`); });
        await page.addInitScript((productSchema) => {
          window.__RECOVERY_SCHEMA__ = productSchema;
          window.__RECOVERY_OPENED__ = null;
        }, schema);
        await page.route('**/*', async (route) => {
          const request = route.request();
          const url = new URL(request.url());
          if (url.origin !== new URL(base).origin) { state.external.push(url.origin); return route.abort(); }
          if (!url.pathname.startsWith('/api/')) return route.continue();
          const json = (value, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) });
          if (request.method() === 'GET' && url.pathname === schema.runtime.http.routes.product_schema) return json({ schema });
          if (request.method() === 'GET' && url.pathname === schema.runtime.http.legacy_routes.version) return json({ version: schema.version });
          if (request.method() === 'GET' && url.pathname === '/api/v1/tasks/source-2/recovery') return json({ source_task_id: 'source-2', eligible: false });
          if (request.method() === 'GET' && url.pathname === '/api/v1/tasks/source-1/recovery') {
            return json({ source_task_id: 'source-1', eligible: state.eligible, recovery_task_id: state.recoveryTaskId || undefined,
              ...(state.eligible ? { receipt_digest: 'receipt-digest', session_id: 'session-1', capability: 'asset-recovery', offering_id: 'local/recovery', offering_revision: 'r1', idempotency_key: 'fixed-recovery-key', slots: [] } : {}) });
          }
          if (request.method() === 'POST' && url.pathname === '/api/v1/tasks/source-1/recovery/prepare') {
            state.prepare += 1; state.prepareBodies.push(request.postDataJSON());
            if (request.postDataJSON().receipt_digest !== 'receipt-digest') return json({ detail: 'bad recovery body' }, 400);
            await hold('prepare');
            if (state.rejectStage === 'prepare') return json({ detail: 'fixture prepare failure' }, 500);
            if (state.prepareExisting) return json({ recovery: { ...wireRecovery(), recovery_task_id: 'already-recovered' } });
            if (state.recoveryTaskId) return json({ recovery: { ...wireRecovery(), recovery_task_id: state.recoveryTaskId } });
            return json({ recovery: { source_task_id: 'source-1', eligible: true, receipt_digest: 'receipt-digest', session_id: 'session-1', capability: 'asset-recovery', idempotency_key: 'fixed-recovery-key' },
              prepared_input_id: 'prepared-recovery', input_hash: 'hash', session_id: 'session-1', capability: 'asset-recovery', offering_id: 'local/recovery', offering_revision: 'r1' }, 201);
          }
          if (request.method() === 'POST' && url.pathname === schema.runtime.http.routes.task_grants) {
            state.grant += 1; state.grantKeys.push(request.headers()['idempotency-key']);
            assert.deepEqual(request.postDataJSON(), { prepared_input_id: 'prepared-recovery' });
            await hold('grant');
            return json({ request_id: 'grant-1', status: state.grantStatus, approved_by_policy: state.grantStatus === 'approved' });
          }
          if (request.method() === 'PUT' && url.pathname === '/api/v1/tasks/grants/grant-1') {
            const body = request.postDataJSON(); state.grantDecisions.push(body.decision);
            await hold('decision');
            if (body.decision === 'approve') return json({ request_id: 'grant-1', status: 'approved' });
            return json({ request_id: 'grant-1', status: 'denied' });
          }
          if (request.method() === 'POST' && url.pathname === schema.runtime.http.routes.tasks) {
            state.postCount += 1; state.submitted.push(request.postDataJSON());
            assert.deepEqual(request.postDataJSON(), { prepared_input_id: 'prepared-recovery', authorization_request_id: 'grant-1' });
            state.recoveryTaskId = 'recovery-task';
            await hold('task');
            if (state.rejectStage === 'task') return json({ detail: 'fixture submit failure' }, 500);
            return json({ task_id: 'recovery-task' }, 201);
          }
          state.external.push(`unexpected api ${request.method()} ${url.pathname}`);
          return json({ detail: 'unexpected fixture route' }, 404);
        });

        await page.goto(base);
        await page.getByTestId('task-recovery-panel').waitFor();
        assert(await page.getByText(labels.description).count() === 1);
        assert.deepEqual(state.external, []);

        // Manual grant: prepare, reload while pending, then approve through the public grant path.
        await page.getByTestId('task-recovery-prepare').click();
        await page.getByTestId('task-recovery-approve').waitFor();
        assert.deepEqual(state.prepareBodies, [{ receipt_digest: 'receipt-digest' }]);
        assert.deepEqual(state.grantKeys, ['fixed-recovery-key']);
        await page.screenshot({ path: path.join(output, `${engine}-${viewport}-pending.png`), fullPage: true });
        await page.reload();
        await page.getByTestId('task-recovery-approve').waitFor();
        assert.equal(state.prepare, 1, 'reconnect must keep the prepared grant without repeating preparation');
        await page.getByTestId('task-recovery-approve').click();
        await page.waitForFunction(() => window.__RECOVERY_OPENED__?.taskId === 'recovery-task');
        assert.deepEqual(state.grantDecisions, ['approve']);
        assert.equal(state.postCount, 1);
        assert.equal(await page.getByTestId('stage-state').innerText(), JSON.stringify({ currentObject: null, pendingPreviewTaskId: null }));
        await page.reload();
        await page.getByTestId('task-recovery-open-existing').waitFor();
        await page.getByTestId('task-recovery-open-existing').click();
        await page.waitForFunction(() => window.__RECOVERY_OPENED__?.taskId === 'recovery-task');
        assert.equal(state.postCount, 1, 'repeat click after reload must open the existing recovery task');
        await assertNoExternal(page, state, engine, viewport);
        report.push(`${engine}-${viewport}: pending approval + reload/reconnect + idempotent existing task + no Stage mutation`);

        // A new, separate auto-approved request submits without a second grant decision.
        await page.evaluate(() => { sessionStorage.clear(); });
        state.grantStatus = 'approved'; state.recoveryTaskId = ''; state.postCount = 0; state.prepare = 0; state.grant = 0; state.grantDecisions = []; state.submitted = []; state.grantKeys = [];
        await page.reload();
        await page.getByTestId('task-recovery-prepare').click();
        await page.waitForFunction(() => window.__RECOVERY_OPENED__?.taskId === 'recovery-task');
        assert.deepEqual(state.grantDecisions, []);
        assert.equal(state.postCount, 1);
        await assertNoExternal(page, state, engine, viewport);
        report.push(`${engine}-${viewport}: auto-approved Grant submits without extra approval`);

        // Explicit denial leaves the source intact and never submits a Task.
        await page.evaluate(() => { sessionStorage.clear(); });
        state.grantStatus = 'pending'; state.recoveryTaskId = ''; state.postCount = 0; state.prepare = 0; state.grant = 0; state.grantDecisions = []; state.submitted = []; state.grantKeys = [];
        await page.reload();
        await page.getByTestId('task-recovery-prepare').click();
        await page.getByTestId('task-recovery-deny').click();
        await page.getByTestId('task-recovery-panel').waitFor({ state: 'hidden' });
        assert.deepEqual(state.grantDecisions, ['deny']);
        assert.equal(state.postCount, 0);
        await page.reload();
        assert.equal(await page.getByTestId('task-recovery-panel').count(), 0, 'a denied decision must not be re-offered on reload');
        await assertNoExternal(page, state, engine, viewport);
        report.push(`${engine}-${viewport}: denial persists for tab and submits no Task`);

        // An ineligible 200 projection cannot reveal a control or cause a write; ownership is covered by API tests.
        await page.evaluate(() => { sessionStorage.clear(); });
        state.eligible = false; state.recoveryTaskId = ''; state.prepare = 0; state.grant = 0; state.postCount = 0;
        await page.reload();
        await page.waitForTimeout(50);
        assert.equal(await page.getByTestId('task-recovery-panel').count(), 0);
        assert.equal(state.postCount, 0);
        state.eligible = true;
        await assertNoExternal(page, state, engine, viewport);
        report.push(`${engine}-${viewport}: ineligible source hides action and makes no write`);
        // Every request boundary is held while its initiating mount loses authority.
        // Reentry deliberately happens before release: equal IDs must not reactivate an old operation.
        for (const stage of ['prepare', 'prepare-error', 'existing', 'grant-pending', 'grant-approved', 'approve', 'deny', 'task', 'task-error']) {
          for (const departure of ['close', 'switch-session', 'switch-task', 'reenter']) {
            await page.evaluate(() => sessionStorage.clear());
            Object.assign(state, { grantStatus: stage === 'grant-approved' || stage.startsWith('task') ? 'approved' : 'pending', recoveryTaskId: '', prepare: 0, grant: 0, postCount: 0, grantDecisions: [], submitted: [], grantKeys: [], prepareBodies: [], prepareExisting: stage === 'existing', rejectStage: stage.endsWith('-error') ? stage.split('-')[0] : '', holdStage: null });
            await page.reload();
            await page.getByTestId('task-recovery-prepare').waitFor();
            if (stage === 'approve' || stage === 'deny') {
              await page.getByTestId('task-recovery-prepare').click();
              await page.getByTestId('task-recovery-approve').waitFor();
            }
            state.holdStage = stage.endsWith('-error') ? stage.split('-')[0] : stage === 'existing' ? 'prepare' : stage.startsWith('grant-') ? 'grant' : stage === 'approve' || stage === 'deny' ? 'decision' : stage;
            const held = new Promise((resolve) => { state.held = resolve; });
            await page.getByTestId(stage === 'approve' || stage === 'deny' ? `task-recovery-${stage}` : 'task-recovery-prepare').click();
            await Promise.race([held, new Promise((_, reject) => setTimeout(() => reject(new Error(`request boundary not reached: ${stage}`)), 5000))]);
            if (departure === 'close' || departure === 'reenter') await page.getByTestId('leave').click();
            else await page.getByTestId(departure).click();
            if (departure === 'reenter') await page.getByTestId('return').click();
            const before = await page.evaluate(() => ({ store: window.__RECOVERY_STORE__(), storage: { ...sessionStorage } }));
            const writes = { prepare: state.prepare, grant: state.grant, decisions: state.grantDecisions.length, tasks: state.postCount };
            const reply = page.waitForResponse((response) => response.request().method() !== 'GET' && new URL(response.url()).pathname.startsWith('/api/'));
            state.release();
            await reply;
            await assertNoExternal(page, state, engine, viewport);
            const after = await page.evaluate(() => ({ store: window.__RECOVERY_STORE__(), storage: { ...sessionStorage } }));
            assert.deepEqual(after, before, `${stage}/${departure}: stale response must not register, change Stage or mutate pending storage`);
            assert.equal(await page.evaluate(() => window.__RECOVERY_OPENED__), null, `${stage}/${departure}: stale callback`);
            assert.equal(await page.locator('[data-sonner-toast]').count(), 0, `${stage}/${departure}: stale toast`);
            assert.equal(await page.getByTestId('task-recovery-error-message').count(), 0, `${stage}/${departure}: stale error`);
            assert.deepEqual({ prepare: state.prepare, grant: state.grant, decisions: state.grantDecisions.length, tasks: state.postCount }, writes, `${stage}/${departure}: stale authority must not start another write`);
            report.push(`${engine}-${viewport}: delayed ${stage} + ${departure} has no stale effects or subsequent writes`);
          }
        }
        console.log(`PASS ${engine}-${viewport}: normal recovery flows + 36 stale-response boundaries`);
        await page.close();
      }
    } finally { await browser.close(); }
  }
} finally { await server.close(); }
for (const result of report) console.log(`PASS ${result}`);
console.log(`Evidence: ${output}`);
