// #264: exercise the real ModelViewer3D when its model request fails (network drop, HTTP error,
// unreadable bytes) or is abandoned (source switched or viewer unmounted while loading).
// Run with node scripts/test-model-viewer-load-failure.mjs (MODEL_VIEWER_BROWSER=Chromium|WebKit for one).
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
let playwrightPath = process.env.PLAYWRIGHT_MODULE_PATH;
if (!playwrightPath) {
  try { playwrightPath = require.resolve('playwright'); }
  catch { playwrightPath = path.join(
    process.env.HOME || '',
    '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright',
  ); }
}
const playwright = require(playwrightPath);

/** One coloured triangle as a binary glTF: small enough to inline, real enough to load and have a material. */
function triangleGlb(brokenTexture = false) {
  const positions = Buffer.alloc(36);
  [0, 0, 0, 1, 0, 0, 0, 1, 0].forEach((value, index) => positions.writeFloatLE(value, index * 4));
  const json = {
    asset: { version: '2.0' },
    scene: 0,
    scenes: [{ nodes: [0] }],
    nodes: [{ mesh: 0 }],
    meshes: [{ primitives: [{ attributes: { POSITION: 0 }, material: 0 }] }],
    materials: [{ pbrMetallicRoughness: { baseColorFactor: [0.8, 0.3, 0.2, 1] } }],
    buffers: [{ byteLength: positions.length }],
    bufferViews: [{ buffer: 0, byteOffset: 0, byteLength: positions.length }],
    accessors: [{ bufferView: 0, componentType: 5126, count: 3, type: 'VEC3', min: [0, 0, 0], max: [1, 1, 0] }],
  };
  let binary = positions;
  if (brokenTexture) {
    // PNG signature with a truncated body: valid GLB structure, but an undecodable requested image.
    const image = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
    binary = Buffer.concat([positions, image]);
    json.buffers[0].byteLength = binary.length;
    json.bufferViews.push({ buffer: 0, byteOffset: positions.length, byteLength: image.length });
    json.images = [{ bufferView: 1, mimeType: 'image/png' }];
    json.textures = [{ source: 0 }];
    json.materials[0].pbrMetallicRoughness.baseColorTexture = { index: 0 };
  }
  let text = Buffer.from(JSON.stringify(json));
  if (text.length % 4) text = Buffer.concat([text, Buffer.alloc(4 - (text.length % 4), 0x20)]);
  const header = Buffer.alloc(12);
  header.writeUInt32LE(0x46546c67, 0);
  header.writeUInt32LE(2, 4);
  header.writeUInt32LE(12 + 8 + text.length + 8 + binary.length, 8);
  const chunk = (type, body) => { const head = Buffer.alloc(8); head.writeUInt32LE(body.length, 0); head.writeUInt32LE(type, 4); return Buffer.concat([head, body]); };
  return Buffer.concat([header, chunk(0x4e4f534a, text), chunk(0x004e4942, binary)]);
}
const glb = triangleGlb();
const brokenTextureGlb = triangleGlb(true);

const catalog = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete catalog.application.runtime.configuration;
const messages = catalog.application.content.messages;
const virtualID = '\0model-viewer-load-failure-test';
const testPlugin = {
  name: 'model-viewer-load-failure-test',
  resolveId(id) { if (id === 'virtual:model-viewer-load-failure-test') return virtualID; },
  load(id) {
    if (id !== virtualID) return;
    return `
      import React, { useCallback, useState } from 'react';
      import { createRoot } from 'react-dom/client';
      import '${path.join(root, 'src/app/globals.css')}';
      import { ProductSchemaProvider } from '${path.join(root, 'src/components/product-schema-provider.tsx')}';
      import { ModelViewer3D } from '${path.join(root, 'src/components/viewer/model-viewer-3d.tsx')}';
      import { Stage3DViews } from '${path.join(root, 'src/components/studio/stage-3d-views.tsx')}';
      const observations = { ready: [], loads: 0, captures: [], captureErrors: 0 };
      document.addEventListener('load', event => {
        if (event.target.localName === 'model-viewer') observations.loads++;
      }, true);
      function Fixture() {
        const [src, setSrc] = useState(null);
        const [mounted, setMounted] = useState(true);
        const [control, setControl] = useState(null);
        // Stable references: an inline callback would change every render and re-publish the control.
        const onControl = useCallback(value => { observations.ready.push(value?.ready ?? false); setControl(value); }, []);
        const onCapture = useCallback(async blob => observations.captures.push({ type: blob.type, size: blob.size, signature: Array.from(new Uint8Array(await blob.slice(0, 8).arrayBuffer())) }), []);
        const onCaptureError = useCallback(() => { observations.captureErrors++; }, []);
        window.loadFailureHarness = { setSource: setSrc, unmount: () => setMounted(false), control, observations };
        return React.createElement(ProductSchemaProvider, { initialSchema: ${JSON.stringify(catalog.application)} },
          React.createElement('main', { style: { position: 'relative', width: '420px', height: '480px', padding: '10px', background: '#20242b' } },
            mounted && src ? React.createElement(ModelViewer3D, {
              key: 'viewer', src, alt: 'Load failure test model', autoRotate: false,
              onControl,
              captureLabel: 'Capture model', captureLongEdgePixels: 512,
              onCapture, onCaptureError,
            }) : null,
            React.createElement(Stage3DViews, { mode: 'texture', onChange: () => undefined, loaded: control?.ready ?? false, supported: control?.viewModes ?? false }),
          ),
        );
      }
      createRoot(document.getElementById('root')).render(React.createElement(Fixture));
    `;
  },
  transformIndexHtml(html) {
    return html
      .replace('{{.Locale}}', 'en')
      .replace('{{.Description}}', 'Model viewer load failure test')
      .replace('{{.Favicon}}', '/favicon.ico')
      .replace('{{.Title}}', 'Model viewer load failure test')
      .replace('{{.Schema}}', JSON.stringify(catalog.application))
      .replace('<script type="module" src="/src/main.tsx"></script>', '<script type="module" src="/@id/virtual:model-viewer-load-failure-test"></script>');
  },
};

const failures = [];
async function until(condition, message, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error(message);
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}
async function check(name, test) {
  try { await test(); console.log(`PASS ${name}`); }
  catch (error) { failures.push(name); console.error(`FAIL ${name}: ${error.stack || error.message}`); }
}

/**
 * Each model path answers from `plan`: 'ok' (the GLB), 'drop' (connection lost), 'http' (503),
 * 'garbage' (unreadable model), 'texture' (corrupt embedded PNG), or 'hold' (wait, then drop).
 */
async function withPage(browserType, run) {
  const cacheDir = await mkdtemp(path.join(tmpdir(), 'art-ai-model-viewer-failure-vite-'));
  const server = await createServer({
    configFile: path.join(root, 'vite.go-web.config.mjs'),
    cacheDir,
    logLevel: 'error',
    server: { host: '127.0.0.1', port: 0 },
    plugins: [testPlugin],
  });
  await server.listen();
  const base = server.resolvedUrls.local[0];
  const origin = new URL(base).origin;
  const browser = await browserType.launch({ headless: true });
  const context = await browser.newContext({ serviceWorkers: 'block', viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  const plan = new Map();
  const held = [];
  const modelRequests = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') consoleErrors.push(message.text()); });
  await page.addInitScript(() => {
    window.unhandledRejections = [];
    window.addEventListener('unhandledrejection', (event) => {
      window.unhandledRejections.push(String(event.reason?.message ?? event.reason));
    });
  });
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) { await route.abort('blockedbyclient'); return; }
    if (url.pathname.startsWith('/models/')) {
      modelRequests.push(url.pathname);
      const answer = plan.get(url.pathname) ?? 'ok';
      if (answer === 'drop') { await route.abort('internetdisconnected'); return; }
      if (answer === 'http') { await route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' }); return; }
      if (answer === 'garbage') { await route.fulfill({ status: 200, contentType: 'model/gltf-binary', body: Buffer.from('this is not a model at all') }); return; }
      if (answer === 'texture') { await route.fulfill({ status: 200, contentType: 'model/gltf-binary', body: brokenTextureGlb }); return; }
      if (answer === 'hold') { await new Promise((resolve) => held.push(resolve)); await route.abort('internetdisconnected').catch(() => undefined); return; }
      await route.fulfill({ status: 200, contentType: 'model/gltf-binary', body: glb });
      return;
    }
    if (url.pathname === '/api/v1/schema') { await route.fulfill({ json: { schema: catalog.application } }); return; }
    if (url.pathname === '/api/version') { await route.fulfill({ json: { version: 'offline-fixture' } }); return; }
    if (url.pathname.startsWith('/api/')) { await route.fulfill({ status: 404, json: { error: 'not in fixture' } }); return; }
    await route.continue();
  });
  try {
    await page.goto(base, { waitUntil: 'domcontentloaded' });
    await page.waitForFunction(() => window.loadFailureHarness !== undefined);
    await run({ page, plan, held, modelRequests, pageErrors, consoleErrors });
  } finally {
    await context.close();
    await browser.close();
    await server.close();
    await rm(cacheDir, { recursive: true, force: true });
  }
}

const errorPanel = (page) => page.getByTestId('model-viewer-error');
async function shownFailure(page) {
  await errorPanel(page).waitFor({ timeout: 30000 });
  return (await errorPanel(page).innerText()).trim();
}
async function viewReason(page) {
  // The rail button is disabled; its hint wrapper still answers the pointer.
  await page.getByTestId('stage-3d-view-clay').hover({ force: true });
  const tooltip = page.getByRole('tooltip');
  await tooltip.waitFor({ timeout: 5000 });
  return (await tooltip.innerText()).trim();
}
function assertNoScriptText(text) {
  for (const raw of ['Cannot read properties', 'undefined is not an object', 'reading \'scene\'', 'TypeError']) {
    assert.ok(!text.includes(raw), `viewer shows a script error: ${JSON.stringify(text)}`);
  }
}

async function runBrowser(name, browserType) {
  await check(`${name}: requested corrupt embedded texture fails, same-URL retry loads and captures`, () => withPage(browserType, async ({ page, plan, modelRequests, pageErrors, consoleErrors }) => {
    const url = '/models/broken-texture.glb';
    plan.set(url, 'texture');
    await page.evaluate(src => window.loadFailureHarness.setSource(src), url);
    const text = await shownFailure(page);
    assertNoScriptText(text);
    assert.ok(text.includes(messages['ui.viewer.load_failed_parse']), 'texture failure must use the Schema parse message');
    assert.ok(consoleErrors.some(message => message.includes('Model texture could not be decoded')), 'must reach the strict requested-texture check, not an unrelated GLB failure');
    // Observe after the error as well, catching a late false-success callback before Retry.
    await page.waitForTimeout(500);
    const failed = await page.evaluate(() => ({
      ...window.loadFailureHarness.observations,
      currentReady: window.loadFailureHarness.control?.ready,
      loaded: document.querySelector('model-viewer')?.loaded,
    }));
    assert.equal(failed.currentReady, false);
    assert.equal(failed.loaded, false);
    assert.ok(failed.ready.length > 0 && failed.ready.every(value => value === false), 'broken texture must never report ready');
    assert.equal(failed.loads, 0, 'broken texture must never emit a model load success');
    const capture = page.getByRole('button', { name: 'Capture model', exact: true });
    assert.equal(await capture.isDisabled(), true, 'capture must remain disabled after texture failure');
    assert.deepEqual(failed.captures, []);
    assert.equal(await page.getByTestId('model-viewer-loading').count(), 0);
    assert.deepEqual(modelRequests, [url]);

    plan.set(url, 'ok');
    await page.getByTestId('model-viewer-retry').click();
    await page.waitForFunction(() => window.loadFailureHarness.control?.ready === true && document.querySelector('model-viewer')?.loaded === true);
    assert.deepEqual(modelRequests, [url, url], 'Retry must fetch the same asset URL again');
    assert.equal(await errorPanel(page).count(), 0);
    assert.equal(await capture.isEnabled(), true);
    await capture.click();
    await page.waitForFunction(() => window.loadFailureHarness.observations.captures.length === 1);
    const recovered = await page.evaluate(() => window.loadFailureHarness.observations);
    assert.equal(recovered.loads, 1);
    assert.equal(recovered.captureErrors, 0);
    assert.equal(recovered.captures[0].type, 'image/png');
    assert.ok(recovered.captures[0].size > 8);
    assert.deepEqual(recovered.captures[0].signature, [137, 80, 78, 71, 13, 10, 26, 10]);
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(await page.evaluate(() => window.unhandledRejections), []);
    console.log(`  ${name} corrupt texture: ready never true, 0 load events, capture disabled; retry: 2 same-URL requests, 1 load, PNG ${recovered.captures[0].size} bytes`);
  }));

  await check(`${name}: network drop shows the network message, retry refetches and loads`, () => withPage(browserType, async ({ page, plan, modelRequests, pageErrors }) => {
    plan.set('/models/dropped.glb', 'drop');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/dropped.glb'));
    const text = await shownFailure(page);
    console.log(`  ${name} network drop shows: ${JSON.stringify(text)}`);
    assertNoScriptText(text);
    assert.ok(text.includes(messages['ui.viewer.load_failed_network']), 'network failure must use ui.viewer.load_failed_network');
    const reason = await viewReason(page);
    assert.ok(reason.includes(messages['ui.studio.stage.view.not_loaded']), `clay reason after failure: ${JSON.stringify(reason)}`);
    plan.set('/models/dropped.glb', 'ok');
    const before = modelRequests.length;
    await page.getByTestId('model-viewer-retry').click();
    await page.waitForFunction(() => document.querySelector('model-viewer')?.loaded === true, null, { timeout: 30000 });
    assert.ok(modelRequests.length > before, 'retry must request the model again instead of reusing the failed load');
    assert.equal(await errorPanel(page).count(), 0);
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(await page.evaluate(() => window.unhandledRejections), []);
  }));

  await check(`${name}: HTTP error and unreadable bytes map to their own messages`, () => withPage(browserType, async ({ page, plan, pageErrors }) => {
    plan.set('/models/unavailable.glb', 'http');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/unavailable.glb'));
    const httpText = await shownFailure(page);
    console.log(`  ${name} HTTP 503 shows: ${JSON.stringify(httpText)}`);
    assertNoScriptText(httpText);
    assert.ok(httpText.includes(messages['ui.viewer.load_failed_http'].replace('{status}', '503')), 'HTTP failure must use ui.viewer.load_failed_http');

    plan.set('/models/garbage.glb', 'garbage');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/garbage.glb'));
    await page.waitForFunction((expected) => document.querySelector('[data-testid="model-viewer-error"]')?.textContent?.includes(expected), messages['ui.viewer.load_failed_parse'], { timeout: 30000 });
    const parseText = await shownFailure(page);
    console.log(`  ${name} unreadable bytes show: ${JSON.stringify(parseText)}`);
    assertNoScriptText(parseText);
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(await page.evaluate(() => window.unhandledRejections), []);
  }));

  await check(`${name}: switching or unmounting during a load that later drops writes nothing`, () => withPage(browserType, async ({ page, plan, held, modelRequests, pageErrors, consoleErrors }) => {
    plan.set('/models/slow-a.glb', 'hold');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/slow-a.glb'));
    await page.waitForFunction(() => document.querySelector('[data-testid="model-viewer-loading"]') !== null);
    await until(() => held.length === 1, 'the held model request never arrived');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/fine-b.glb'));
    await page.waitForFunction(() => document.querySelector('model-viewer')?.loaded === true, null, { timeout: 30000 });
    while (held.length) held.shift()();
    await page.waitForTimeout(1500);
    assert.equal(await errorPanel(page).count(), 0, 'a dropped load of the previous source must not show an error over the current model');
    // The abandoned load failed while nobody listened; it must not stay cached as a failure for its URL.
    plan.set('/models/slow-a.glb', 'ok');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/slow-a.glb'));
    await page.waitForFunction(() => {
      const element = document.querySelector('model-viewer');
      return element?.loaded === true && element.src === '/models/slow-a.glb';
    }, null, { timeout: 30000 }).catch(async (error) => {
      const state = await page.evaluate(() => {
        const element = document.querySelector('model-viewer');
        return { src: element?.src, loaded: element?.loaded, error: document.querySelector('[data-testid="model-viewer-error"]')?.textContent ?? null };
      });
      throw new Error(`${error.message}\nviewer: ${JSON.stringify(state)}\nmodel requests: ${JSON.stringify(modelRequests)}`);
    });
    assert.equal(await errorPanel(page).count(), 0, 'reopening a model whose abandoned load dropped must download it again');
    assert.equal(modelRequests.filter((pathname) => pathname === '/models/slow-a.glb').length, 2, `requests: ${JSON.stringify(modelRequests)}`);

    plan.set('/models/slow-c.glb', 'hold');
    await page.evaluate(() => window.loadFailureHarness.setSource('/models/slow-c.glb'));
    await page.waitForFunction(() => document.querySelector('[data-testid="model-viewer-loading"]') !== null);
    await until(() => held.length === 1, 'the held model request never arrived');
    await page.evaluate(() => window.loadFailureHarness.unmount());
    await page.waitForFunction(() => document.querySelector('model-viewer') === null);
    while (held.length) held.shift()();
    await page.waitForTimeout(1500);
    assert.equal(await errorPanel(page).count(), 0);
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(await page.evaluate(() => window.unhandledRejections), []);
    const reactWarnings = consoleErrors.filter((message) => /unmounted component|Warning:/i.test(message));
    assert.deepEqual(reactWarnings, []);
  }));
}

const browserNames = process.env.MODEL_VIEWER_BROWSER ? [process.env.MODEL_VIEWER_BROWSER] : ['Chromium', 'WebKit'];
for (const name of browserNames) {
  await runBrowser(name, name === 'WebKit' ? playwright.webkit : playwright.chromium);
}
if (failures.length) {
  console.error(`${failures.length} check(s) failed`);
  process.exit(1);
}
