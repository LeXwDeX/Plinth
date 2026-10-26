// Local mounted-component regression. Real Harness UI/hooks/controls, fixture HTTP only.
// No Provider, Generation, approval, or non-loopback request is permitted.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, mkdir } from 'node:fs/promises';
import { homedir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const { application: schema } = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete schema.runtime.configuration;
delete schema.chat.harness.context_template;
schema.chat.harness.operations = schema.chat.harness.operations.filter(op => op.id === 'remesh');
schema.chat.harness.default_enabled_operations = ['remesh'];
const label = key => { assert.equal(typeof schema.content.messages[key], 'string', `missing ${key}`); return schema.content.messages[key]; };
const modes = schema.chat.harness.task_source_modes;
const modeLabel = mode => modes.find(item => item.id === mode).label;
const offering = { id: 'fixture/remesh', revision: 'r1', provider_id: 'fixture', provider_name: 'Fixture service', model_id: 'fixture-model', model_name: 'Fixture model', capability: 'remesh', available: true,
  parameters: [{ name: 'source_task', type: 'task', semantic: 'source-task', label: 'Task' },
    { name: 'source_asset', type: 'asset', semantic: 'source-asset', label: 'Asset', uploaded_model: { profile_id: schema.runtime.input_assets.model_profile.id } },
    { name: 'target', type: 'number', label: 'Target', default: 100 }] };
const initial = () => ({ session_id: 'session', revision: 1, digest: 'original-digest', enabled_operations: [], sources: [], fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: false, updated_at: '' });
const virtualID = '\0harness-agent-fixture';
const server = await createServer({ configFile: path.join(root, 'vite.go-web.config.mjs'), logLevel: 'error', server: { host: '127.0.0.1', port: 0 },
  plugins: [{ name: 'harness-agent-fixture', resolveId(id) { if (id === 'virtual:harness-agent-fixture') return virtualID; },
    load(id) { if (id === virtualID) return `
      import React, { StrictMode } from 'react'; import { createRoot } from 'react-dom/client';
      import '${path.join(root, 'src/app/globals.css')}';
      import { ProductSchemaProvider } from '${path.join(root, 'src/components/product-schema-provider.tsx')}';
      import { HarnessPanel } from '${path.join(root, 'src/components/workspace/harness-panel.tsx')}';
      document.documentElement.classList.add('dark');
      createRoot(document.getElementById('root')).render(React.createElement(StrictMode,null,
        React.createElement(ProductSchemaProvider,{initialSchema:window.__ART_AI_PRODUCT_SCHEMA__},
          React.createElement('aside',{style:{width:300,height:'100vh'}},React.createElement(HarnessPanel,{sessionId:'session',object:null})))));
    `; },
    transformIndexHtml(html) { return html.replace('{{.Schema}}', JSON.stringify(schema)).replace('src="/src/main.tsx"', 'src="/@id/virtual:harness-agent-fixture"'); },
  }],
});
const output = process.env.HARNESS_UI_EVIDENCE_DIR;
if (output) { assert(path.isAbsolute(output)); await mkdir(output, { recursive: true, mode: 0o700 }); }
const assertions = [];
const fixtureSource = { kind: 'task_asset', selection_mode: 'agent', parameter: '*' };
const sourcePair = { kind: 'task_asset', parameter: 'source_task', task_id: 'model-2', asset_id: 'asset-2' };
async function pageFor(browser, options = {}) {
  const page = await browser.newPage({ viewport: options.viewport || { width: 1440, height: 900 }, serviceWorkers: 'block' });
  const state = { selection: structuredClone(options.selection || initial()), puts: [], forbidden: [], errors: [], conflict: Boolean(options.conflict) };
  page.on('pageerror', error => state.errors.push(error.message));
  const count = options.count ?? 2;
  await page.route('**/*', async route => {
    const request = route.request(); const url = new URL(request.url());
    if (url.origin !== new URL(base).origin) { state.forbidden.push('external'); return route.abort(); }
    if (!url.pathname.startsWith('/api/')) { if (request.method() !== 'GET') state.forbidden.push('static-write'); return route.continue(); }
    const json = value => route.fulfill({ json: value });
    if (request.method() === 'GET') {
      if (url.pathname === schema.runtime.http.routes.product_schema) return json({ schema });
      if (url.pathname === schema.runtime.http.legacy_routes.version) return json({ version: 'fixture' });
      if (url.pathname === schema.runtime.http.legacy_routes.auth_session) return json({ user: { id: 'owner', name: 'Fixture owner', email: 'fixture@example.invalid' }, isAdmin: false });
      if (url.pathname === schema.runtime.http.routes.offerings) return json({ offerings: [offering] });
      if (url.pathname === schema.runtime.http.routes.account_resources) return json({ tasks: [], next_cursor: '', uploads: { data: [{ id: 'upload', asset_type: schema.runtime.input_assets.model_profile.asset_type, original_filename: 'owned.glb', media_type: 'model/gltf-binary', size: 100, created_at: '', sha256: 'f'.repeat(64) }], total: 1, page: 1, page_size: 20 } });
      if (url.pathname === schema.runtime.http.routes.chat_session_lineage.replace('{sessionID}', 'session')) return json({ nodes: Array.from({ length: count }, (_, i) => ({ id: `model-${i + 1}`, task_id: `model-${i + 1}`, kind: 'task', state: 'SUCCEEDED', created_at: `2026-10-16T00:00:0${i}Z`, version: i + 1, assets: [{ id: `asset-${i + 1}`, role: 'model', variant: 'glb', media_type: 'model/gltf-binary', url: `/api/v1/assets/asset-${i + 1}` }] })), edges: [], groups: [], next_cursor: '' });
      if (url.pathname === schema.runtime.http.routes.chat_session_harness.replace('{sessionID}', 'session')) return json({ selection: state.selection, status: !state.selection.operation_id ? 'free' : options.status || (count === 0 ? 'needs_input' : 'ready'), reason: count === 0 ? schema.chat.harness.source_clarification_message : '', missing_parameters: count === 0 ? ['source_task'] : [], can_propose: false, estimated_credits: 0, source_views: [] });
    }
    if (request.method() === 'PUT' && url.pathname === schema.runtime.http.routes.chat_session_harness.replace('{sessionID}', 'session')) {
      const body = request.postDataJSON(); state.puts.push(body);
      assert.equal(body.expected_revision, state.selection.revision);
      if (state.conflict) { state.conflict = false; return route.fulfill({ status: 409, json: { detail: 'error.harness_conflict' } }); }
      state.selection = { ...body.selection, session_id: 'session', revision: state.selection.revision + 1, digest: 'saved-' + state.puts.length, updated_at: '' };
      return json({ selection: state.selection, status: state.selection.operation_id ? 'ready' : 'free', reason: '', missing_parameters: [], can_propose: false, estimated_credits: 0, source_views: [] });
    }
    state.forbidden.push(request.method() + ' ' + url.pathname); return route.abort();
  });
  await page.goto(base); await page.getByTestId('harness-panel').waitFor();
  return { page, state };
}
async function choose(page, name, option) {
  await page.getByRole('combobox', { name, exact: true }).click();
  await page.getByRole('option', { name: option, exact: true }).click();
  await page.getByRole('listbox').waitFor({ state: 'hidden' });
}
async function checkState(page, state) {
  assert.deepEqual(state.forbidden, []); assert.deepEqual(state.errors, []);
  assert(!await page.getByTestId('harness-panel').innerText().then(text => /source_candidates|source_fingerprints|parameter:\s*\*|original-digest/.test(text)));
}
await server.listen();
const base = server.resolvedUrls.local[0];
try {
  const browsers = (process.env.HARNESS_UI_BROWSERS || 'chromium,webkit').split(',');
  const sizes = process.env.HARNESS_UI_ONE_SIZE ? [[1440, 900]] : [[1366, 768], [1440, 900], [1920, 1080]];
  for (const engine of browsers) {
    assert(['chromium', 'webkit'].includes(engine)); const browser = await pw[engine].launch({ headless: true });
    try { for (const [width, height] of sizes) {
      const { page, state } = await pageFor(browser, { viewport: { width, height } });
      try {
        await page.getByRole('radio', { name: /调整模型拓扑和面数/ }).check();
        await page.getByRole('combobox', { name: label('ui.harness.task_source_mode'), exact: true }).filter({ hasText: modeLabel('agent') }).waitFor();
        assert.equal(await page.getByRole('combobox', { name: label('ui.harness.source_model'), exact: true }).count(), 0);
        assert.equal(state.puts.length, 0);
        await page.getByRole('button', { name: label('ui.harness.save'), exact: true }).click();
        await page.getByText(label('ui.harness.saved'), { exact: true }).waitFor();
        assert.deepEqual(state.puts[0].selection.sources, [fixtureSource]);
        assert.deepEqual(state.puts[0].selection.fillable_parameters, ['target']);
        await choose(page, label('ui.harness.task_source_mode'), modeLabel('fixed'));
        assert.equal(state.puts.length, 1);
        await choose(page, label('ui.harness.source_model'), label('ui.harness.model_version').replace('{version}', '2'));
        await choose(page, label('ui.harness.source_asset'), 'glb');
        await page.getByRole('button', { name: label('ui.harness.save'), exact: true }).click();
        await page.getByText(label('ui.harness.saved'), { exact: true }).waitFor();
        assert.deepEqual(state.puts[1].selection.sources, [sourcePair]);
        await choose(page, label('ui.harness.task_source_mode'), modeLabel('agent'));
        await choose(page, label('ui.harness.source_kind'), schema.chat.harness.source_kinds.find(item => item.id === 'input_asset').label);
        await choose(page, label('ui.harness.upload.existing'), 'owned.glb');
        assert.equal(await page.getByRole('combobox', { name: label('ui.harness.task_source_mode'), exact: true }).count(), 0);
        assert.equal(await page.getByTestId('harness-model-preview-open').isEnabled(), true);
        await page.getByRole('button', { name: label('ui.harness.save'), exact: true }).click();
        await page.getByText(label('ui.harness.saved'), { exact: true }).waitFor();
        assert.deepEqual(state.puts[2].selection.sources, [{ kind: 'input_asset', parameter: 'source_asset', asset_id: 'upload' }]);
        await choose(page, label('ui.harness.source_kind'), schema.chat.harness.source_kinds.find(item => item.id === 'task_asset').label);
        await page.getByTestId('harness-agent-source-help').waitFor();
        assert.equal(state.puts.length, 3);
        if (output) await page.screenshot({ path: path.join(output, `${engine}-${width}-agent.png`) });
        await page.getByRole('button', { name: label('ui.harness.clear'), exact: true }).click();
        await page.getByText(label('ui.harness.saved'), { exact: true }).waitFor();
        assert.deepEqual(state.puts[3].selection.sources, []); assert.equal(state.puts[3].selection.operation_id, undefined);
        await checkState(page, state); assertions.push(`${engine}-${width}: mounted default/switch/fixed/upload/clear`);
      } finally { await page.close(); }
    }
    for (const count of [0, 1, 2]) {
      const selection = { ...initial(), operation_id: 'remesh', offering_id: offering.id, offering_revision: offering.revision, enabled_operations: ['remesh'], sources: [fixtureSource] };
      const { page, state } = await pageFor(browser, { selection, count });
      try { await page.getByTestId('harness-agent-source-help').waitFor(); assert.equal(state.puts.length, 0);
        assert.equal(await page.getByRole('combobox', { name: label('ui.harness.source_model'), exact: true }).count(), 0);
        if (!count) await page.getByText(schema.chat.harness.source_clarification_message, { exact: true }).waitFor();
        await checkState(page, state); assertions.push(`${engine}: ${count} projected-source fixture remains read-only`);
      } finally { await page.close(); }
    }
    // A persisted empty source remains empty; rendering must not silently migrate it.
    {
      const selection = { ...initial(), operation_id: 'remesh', offering_id: offering.id, offering_revision: offering.revision, enabled_operations: ['remesh'], sources: [] };
      const { page, state } = await pageFor(browser, { selection });
      try { await page.getByRole('combobox', { name: label('ui.harness.task_source_mode'), exact: true }).filter({ hasText: modeLabel('fixed') }).waitFor();
        assert.deepEqual(state.selection.sources, []); assert.equal(state.selection.digest, 'original-digest'); assert.equal(state.puts.length, 0);
        await checkState(page, state); assertions.push(`${engine}: historical empty source is not migrated`);
      } finally { await page.close(); }
    }
    const fixed = { ...initial(), operation_id: 'remesh', offering_id: offering.id, offering_revision: offering.revision, enabled_operations: ['remesh'], sources: [sourcePair], fillable_parameters: ['target'] };
    const { page, state } = await pageFor(browser, { selection: fixed, conflict: true });
    try { await page.getByRole('combobox', { name: label('ui.harness.task_source_mode'), exact: true }).filter({ hasText: modeLabel('fixed') }).waitFor();
      assert.deepEqual(state.selection.sources, [sourcePair]); assert.equal(state.puts.length, 0);
      await page.getByRole('combobox', { name: label('ui.harness.task_source_mode'), exact: true }).press('ArrowDown');
      await page.getByRole('listbox').waitFor({ state: 'visible', timeout: 30000 });
      await page.keyboard.press('Home');
      const agentOption = page.getByRole('option', { name: modeLabel('agent'), exact: true });
      await page.waitForFunction(element => document.activeElement === element, await agentOption.elementHandle(), { timeout: 30000 });
      await page.keyboard.press('Enter');
      await page.getByRole('listbox').waitFor({ state: 'hidden' });
      await page.getByTestId('harness-agent-source-help').waitFor();
      await page.getByRole('button', { name: label('ui.harness.save'), exact: true }).click();
      await page.getByText(label('ui.harness.conflict'), { exact: true }).waitFor();
      assert.deepEqual(state.selection.sources, [sourcePair]); assert.equal(state.selection.digest, 'original-digest');
      assert.equal(state.puts.length, 1); await checkState(page, state); assertions.push(`${engine}: historical identity/conflict preserved`);
    } finally { await page.close(); }
    } finally { await browser.close(); }
  }
} finally { await server.close(); }
for (const result of assertions) console.log('PASS ' + result);
