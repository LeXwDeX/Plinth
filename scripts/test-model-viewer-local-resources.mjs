// Exercise the real ModelViewer3D component with same-origin compressed fixtures.
// Run with node scripts/test-model-viewer-local-resources.mjs.
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
const fixtureRoot = path.join(root, 'scripts/fixtures/model-viewer-carconcept');
const fixtureFiles = new Map();
for (const name of [
  'CarConcept.gltf', 'CarConcept.data.bin', 'Dash_E.ktx2', 'Dot_C.ktx2',
  'Dot_N.ktx2', 'Hex_N.ktx2', 'Khronos_C.ktx2', 'Mechanical_N.ktx2',
  'Mechanical_ORM.ktx2', 'Occlusion.ktx2', 'Powdercoat_N.ktx2', 'Rib_N.ktx2',
  'Thickness.ktx2', 'Tireside_C.ktx2', 'Tireside_N.ktx2', 'Tiretread_N.ktx2',
]) fixtureFiles.set(`/fixtures/model-viewer-carconcept/${name}`, await readFile(path.join(fixtureRoot, name)));

const modelViewerRequire = createRequire(require.resolve('@google/model-viewer'));
const decoderFiles = new Map([
  ['/model-viewer/draco/draco_decoder.js', 'three/examples/jsm/libs/draco/draco_decoder.js'],
  ['/model-viewer/draco/draco_decoder.wasm', 'three/examples/jsm/libs/draco/draco_decoder.wasm'],
  ['/model-viewer/draco/draco_wasm_wrapper.js', 'three/examples/jsm/libs/draco/draco_wasm_wrapper.js'],
  ['/model-viewer/basis/basis_transcoder.js', 'three/examples/jsm/libs/basis/basis_transcoder.js'],
  ['/model-viewer/basis/basis_transcoder.wasm', 'three/examples/jsm/libs/basis/basis_transcoder.wasm'],
]);
const decoderContents = new Map([...decoderFiles].map(([url, dependencyPath]) => [
  url,
  readFile(modelViewerRequire.resolve(dependencyPath)),
]));
for (const [url, contents] of decoderContents) decoderContents.set(url, await contents);

const catalog = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete catalog.application.runtime.configuration;
const virtualID = '\0model-viewer-local-resources-test';
const testPlugin = {
  name: 'model-viewer-local-resources-test',
  resolveId(id) { if (id === 'virtual:model-viewer-local-resources-test') return virtualID; },
  load(id) {
    if (id !== virtualID) return;
    return `
      import React, { useState } from 'react';
      import { createRoot } from 'react-dom/client';
      import '${path.join(root, 'src/app/globals.css')}';
      import { ProductSchemaProvider } from '${path.join(root, 'src/components/product-schema-provider.tsx')}';
      import { ModelViewer3D } from '${path.join(root, 'src/components/viewer/model-viewer-3d.tsx')}';
      import { captureLoadedModelHighResolution } from '${path.join(root, 'src/lib/workspace/model-capture.ts')}';
      function Fixture() {
        const [src, setSrc] = useState('/fixtures/model-viewer-carconcept/CarConcept.gltf?revision=1');
        const [captures, setCaptures] = useState([]);
        window.modelViewerLocalResources = {
          setSource: (revision) => setSrc('/fixtures/model-viewer-carconcept/CarConcept.gltf?revision=' + revision),
          captures,
          inspectCapture: async () => {
            const element = document.querySelector('model-viewer');
            const { ModelViewerElement } = await import('@google/model-viewer');
            return captureLoadedModelHighResolution(element, getComputedStyle(element).backgroundColor, 512, window.devicePixelRatio || 1, ModelViewerElement);
          },
        };
        return React.createElement(ProductSchemaProvider, { initialSchema: ${JSON.stringify(catalog.application)} },
          React.createElement('main', { style: { width: '420px', height: '480px', padding: '10px', background: '#20242b' } },
            React.createElement(ModelViewer3D, {
              src,
              alt: 'Compressed test model',
              autoRotate: false,
              captureLabel: 'Capture model',
              captureLongEdgePixels: 512,
              onCapture: async (blob, view) => setCaptures((current) => [...current, { type: blob.type, size: blob.size, view }]),
              onCaptureError: () => { window.modelViewerCaptureError = true; },
            }),
          ),
        );
      }
      createRoot(document.getElementById('root')).render(React.createElement(Fixture));
    `;
  },
  transformIndexHtml(html) {
    return html
      .replace('{{.Locale}}', 'en')
      .replace('{{.Description}}', 'Model viewer local resources test')
      .replace('{{.Favicon}}', '/favicon.ico')
      .replace('{{.Title}}', 'Model viewer local resources test')
      .replace('{{.Schema}}', JSON.stringify(catalog.application))
      .replace('<script type="module" src="/src/main.tsx"></script>', '<script type="module" src="/@id/virtual:model-viewer-local-resources-test"></script>');
  },
};

const failures = [];

async function createFixtureServer() {
  const cacheDir = await mkdtemp(path.join(tmpdir(), 'art-ai-model-viewer-vite-'));
  const server = await createServer({
    configFile: path.join(root, 'vite.go-web.config.mjs'),
    cacheDir,
    logLevel: 'error',
    server: { host: '127.0.0.1', port: 0 },
    plugins: [testPlugin],
  });
  try {
    await server.listen();
    return { server, cacheDir };
  } catch (error) {
    await rm(cacheDir, { recursive: true, force: true });
    throw error;
  }
}

async function check(name, test) {
  try { await test(); console.log(`PASS ${name}`); }
  catch (error) { failures.push(name); console.error(`FAIL ${name}: ${error.stack || error.message}`); }
}

async function runBrowser(name, browserType) {
  await check(`${name}: first load, decoder localization, reload and capture`, async () => {
    const { server, cacheDir } = await createFixtureServer();
    const base = server.resolvedUrls.local[0];
    const origin = new URL(base).origin;
    const browser = await browserType.launch({ headless: true });
    const context = await browser.newContext({ serviceWorkers: 'block' });
    const page = await context.newPage();
    const externalAttempts = [];
    const requestFailures = [];
    const consoleErrors = [];
    const requestPaths = [];
    page.on('request', (request) => {
      const url = new URL(request.url());
      if (url.origin !== origin) externalAttempts.push(request.url());
      else requestPaths.push(url.pathname);
    });
    page.on('requestfailed', (request) => requestFailures.push(`${request.url()}: ${request.failure()?.errorText || 'failed'}`));
    page.on('pageerror', (error) => consoleErrors.push(error.message));
    page.on('console', (message) => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    if (name === 'Chromium') {
      const cdp = await context.newCDPSession(page);
      await cdp.send('Network.enable');
      await cdp.send('Network.setCacheDisabled', { cacheDisabled: true });
    }

    await page.route('**/*', async (route) => {
      const url = new URL(route.request().url());
      if (url.origin !== origin) {
        externalAttempts.push(route.request().url());
        await route.abort('blockedbyclient');
        return;
      }
      if (fixtureFiles.has(url.pathname)) {
        const body = fixtureFiles.get(url.pathname);
        if (url.pathname.endsWith('.gltf') && url.searchParams.get('revision') === '1') {
          await new Promise((resolve) => setTimeout(resolve, 1500));
        }
        const contentType = url.pathname.endsWith('.ktx2') ? 'image/ktx2'
          : url.pathname.endsWith('.bin') ? 'application/octet-stream'
            : 'model/gltf+json';
        await route.fulfill({ status: 200, contentType, body });
        return;
      }
      if (decoderContents.has(url.pathname)) {
        const body = decoderContents.get(url.pathname);
        await route.fulfill({ status: 200, contentType: url.pathname.endsWith('.wasm') ? 'application/wasm' : 'text/javascript', body });
        return;
      }
      if (url.pathname === '/api/v1/schema') {
        await route.fulfill({ json: { schema: catalog.application } });
        return;
      }
      if (url.pathname === '/api/version') {
        await route.fulfill({ json: { version: 'offline-fixture' } });
        return;
      }
      await route.continue();
    });

    try {
      await page.goto(base, { waitUntil: 'domcontentloaded' });
      const captureButton = page.getByRole('button', { name: 'Capture model' });
      await captureButton.waitFor();
      assert.equal(await captureButton.isDisabled(), true, 'capture must remain disabled before model load');
      await page.locator('model-viewer').waitFor({ state: 'attached' });
      await page.evaluate(() => {
        window.modelViewerLoadCount = 0;
        document.querySelector('model-viewer').addEventListener('load', () => { window.modelViewerLoadCount += 1; });
      });
      await page.waitForFunction(() => window.modelViewerLoadCount === 1, null, { timeout: 60000 });
      assert.equal(await page.locator('model-viewer').evaluate((element) => element.loaded), true);
      await page.waitForFunction(() => document.querySelector('model-viewer')?.loaded === true && !document.querySelector('button')?.disabled);
      assert.equal(await captureButton.isDisabled(), false, 'capture must enable after first model load');

      const localDecoderPaths = [
        '/model-viewer/draco/draco_decoder.wasm',
        '/model-viewer/draco/draco_wasm_wrapper.js',
        '/model-viewer/basis/basis_transcoder.js',
        '/model-viewer/basis/basis_transcoder.wasm',
      ];
      const captureOnce = async () => {
        await captureButton.click();
        await page.waitForFunction(() => window.modelViewerLocalResources?.captures.length > (window.modelViewerLocalResources?.lastCaptureCount || 0) || window.modelViewerCaptureError, null, { timeout: 45000 });
        if (await page.evaluate(() => window.modelViewerCaptureError)) {
          const directError = await page.evaluate(async () => {
            try { await window.modelViewerLocalResources.inspectCapture(); return ''; }
            catch (error) { return error instanceof Error ? error.message : String(error); }
          });
          throw new Error(`ModelViewer3D capture callback reported failure; direct capture: ${directError || 'succeeded'}`);
        }
        await page.evaluate(() => { window.modelViewerLocalResources.lastCaptureCount = window.modelViewerLocalResources.captures.length; });
        const capture = await page.evaluate(() => window.modelViewerLocalResources.captures.at(-1));
        assert.equal(capture.type, 'image/png');
        assert.ok(capture.size > 0, 'capture must contain PNG data');
      };
      await captureOnce();

      const secondModelRequest = page.waitForRequest((request) => {
        const url = new URL(request.url());
        return url.pathname.endsWith('/CarConcept.gltf') && url.searchParams.get('revision') === '2';
      });
      const secondLoadCount = await page.evaluate(() => window.modelViewerLoadCount);
      await page.evaluate(() => window.modelViewerLocalResources.setSource(2));
      await secondModelRequest;
      await page.waitForFunction((count) => window.modelViewerLoadCount > count, secondLoadCount, { timeout: 60000 });
      await page.waitForFunction(() => document.querySelector('model-viewer')?.loaded === true && !document.querySelector('button')?.disabled);
      await captureOnce();

      assert.deepEqual(externalAttempts, [], 'browser attempted a non-same-origin request');
      assert.deepEqual(requestFailures, [], 'a browser request failed');
      assert.deepEqual(consoleErrors, [], 'browser emitted console errors');
      for (const decoderPath of localDecoderPaths) {
        assert.ok(requestPaths.includes(decoderPath), `missing same-origin decoder request: ${decoderPath}`);
      }
      for (const formatPath of ['/fixtures/model-viewer-carconcept/CarConcept.data.bin', '/fixtures/model-viewer-carconcept/Mechanical_N.ktx2']) {
        assert.ok(requestPaths.includes(formatPath), `missing compressed model resource request: ${formatPath}`);
      }
      assert.ok(requestPaths.some((pathname) => pathname === '/meshopt-decoder-bootstrap.js'));
      console.log(`  ${name} observed ${requestPaths.length} same-origin requests and 2 successful PNG captures`);
    } catch (error) {
      console.error(`  ${name} relevant request paths: ${JSON.stringify(requestPaths.filter((pathname) => /model-viewer|CarConcept|\.ktx2|decoder-bootstrap/.test(pathname)))}`);
      console.error(`  ${name} external attempts: ${JSON.stringify(externalAttempts)}`);
      console.error(`  ${name} failed requests: ${JSON.stringify(requestFailures)}`);
      throw error;
    } finally {
      await context.close();
      await browser.close();
      await server.close();
      await rm(cacheDir, { recursive: true, force: true });
    }
  });
}

const browserNames = process.env.MODEL_VIEWER_BROWSER ? [process.env.MODEL_VIEWER_BROWSER] : ['Chromium', 'WebKit'];
for (const name of browserNames) {
  if (!playwright[name.toLowerCase()]) {
    failures.push(`${name} unavailable`);
    console.error(`FAIL ${name} unavailable in Playwright runtime`);
    continue;
  }
  await runBrowser(name, playwright[name.toLowerCase()]);
}
assert.deepEqual(failures, [], 'model-viewer local-resource browser regressions failed');
