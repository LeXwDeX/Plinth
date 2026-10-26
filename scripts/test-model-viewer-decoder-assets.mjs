import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import viteConfig from '../vite.go-web.config.mjs';

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const plugin = viteConfig.plugins.find((entry) => entry.name === 'go-web-model-viewer-decoders');
assert.ok(plugin, 'Vite must package model-viewer decoder assets');

// Markers differ: the last four bytes of every WebAssembly binary are the version header, not a magic number.
const requiredAssets = [
  'model-viewer/draco/draco_decoder.js',
  'model-viewer/draco/draco_decoder.wasm',
  'model-viewer/draco/draco_wasm_wrapper.js',
  'model-viewer/draco/LICENSE.draco',
  'model-viewer/basis/basis_transcoder.js',
  'model-viewer/basis/basis_transcoder.wasm',
  'model-viewer/basis/LICENSE.basis',
];

export function validateDecoderAssets() {
  const assets = new Map();
  plugin.generateBundle.call({
    emitFile(asset) {
      assets.set(asset.fileName, asset.source);
    },
  }, {}, {});

  for (const name of requiredAssets) {
    const content = assets.get(name);
    assert.ok(content, `missing build asset ${name}`);
    assert.ok(content.byteLength > 0, `empty build asset ${name}`);
    if (name.endsWith('.wasm')) {
      assert.deepEqual(
        [...content.subarray(0, 4)],
        [0x00, 0x61, 0x73, 0x6d],
        `${name} must be a WebAssembly module`,
      );
    }
  }

  // three's package is MIT; the bundled Draco and Basis decoders are Apache-2.0. Their shipped
  // texts must be the tracked copies, unchanged.
  assert.deepEqual(
    assets.get('model-viewer/draco/LICENSE.draco'),
    readFileSync(path.join(root, 'internal/assets/local/draco/LICENSE.draco')),
    'the Draco licence must match internal/assets/local/draco/LICENSE.draco byte for byte',
  );
  assert.deepEqual(
    assets.get('model-viewer/basis/LICENSE.basis'),
    readFileSync(path.join(root, 'scripts/vendor/basis/LICENSE')),
    'the Basis licence must match scripts/vendor/basis/LICENSE byte for byte',
  );

  assert.equal(assets.size, requiredAssets.length, 'emit only the decoder payloads and licences required by model-viewer');
  return assets.size;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  console.log(`Validated ${validateDecoderAssets()} local model-viewer decoder assets.`);
}
