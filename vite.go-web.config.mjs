import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import path from 'node:path';
import { copyFileSync, readFileSync } from 'node:fs';

const projectRoot = path.dirname(fileURLToPath(import.meta.url));
const goWebOutputRoot = path.join(projectRoot, 'build', 'go-web');
const projectRequire = createRequire(import.meta.url);
const modelViewerRequire = createRequire(projectRequire.resolve('@google/model-viewer'));
const sparkRequire = createRequire(projectRequire.resolve('@sparkjsdev/spark'));
const sparkPackagePath = path.resolve(path.dirname(sparkRequire.resolve('@sparkjsdev/spark')), '..', 'package.json');
const sparkPackage = JSON.parse(readFileSync(sparkPackagePath, 'utf8'));
if (sparkPackage.version !== '2.3.1') throw new Error(`Unsupported Spark version: ${sparkPackage.version}`);
// Spark publishes separate require/import entries. Vite bundles the browser ESM export.
const sparkModulePath = path.resolve(path.dirname(sparkPackagePath), sparkPackage.exports['.'].import);
const sparkModuleSource = readFileSync(sparkModulePath, 'utf8');
const sparkWorkerMatch = sparkModuleSource.match(/^var jsContent = ("(?:\\.|[^"\\])*");$/m);
if (!sparkWorkerMatch || !sparkModuleSource.includes('var blob = typeof self !== "undefined"')
  || !sparkModuleSource.includes('return new Worker("data:text/javascript;charset=utf-8," + encodeURIComponent(jsContent)')) {
  throw new Error('Unsupported Spark worker wrapper; review the same-origin worker adaptation before upgrading');
}
const sparkWorkerSource = JSON.parse(sparkWorkerMatch[1].replace(/\t/g, '\\t'));
let sparkWorkerTransformed = false;
const sparkWorkerWrapper = /var jsContent = "(?:\\.|[^"\\])*";\nvar blob = typeof self !== "undefined" && self\.Blob && new Blob\(\["\(self\.URL \|\| self\.webkitURL\)\.revokeObjectURL\(self\.location\.href\);", jsContent\], \{ type: "text\/javascript;charset=utf-8" \}\);\nfunction WorkerWrapper\(options\) \{[\s\S]*?\n\}\n\/\/#endregion/;

const modelViewerDecoderAssets = [
  ['three/examples/jsm/libs/draco/draco_decoder.js', 'model-viewer/draco/draco_decoder.js'],
  ['three/examples/jsm/libs/draco/draco_decoder.wasm', 'model-viewer/draco/draco_decoder.wasm'],
  ['three/examples/jsm/libs/draco/draco_wasm_wrapper.js', 'model-viewer/draco/draco_wasm_wrapper.js'],
  ['three/examples/jsm/libs/basis/basis_transcoder.js', 'model-viewer/basis/basis_transcoder.js'],
  ['three/examples/jsm/libs/basis/basis_transcoder.wasm', 'model-viewer/basis/basis_transcoder.wasm'],
];
// three is MIT but its decoders are Google Draco and BinomialLLC Basis Universal, both Apache-2.0.
// Ship the tracked licence texts beside them (#258).
const decoderLicenses = [
  ['internal/assets/local/draco/LICENSE.draco', 'model-viewer/draco/LICENSE.draco'],
  ['scripts/vendor/basis/LICENSE', 'model-viewer/basis/LICENSE.basis'],
];

// @google/model-viewer ships CDN defaults for its decoders. The app overrides them at runtime, but the literals
// must not survive in the bundle: the intranet has no internet and scripts/check-build-external-links.mjs fails on them.
const modelViewerCdnDefaults = [
  [/https:\/\/www\.gstatic\.com\/draco\/versioned\/decoders\/[0-9.]+\//g, '/model-viewer/draco/'],
  [/https:\/\/www\.gstatic\.com\/basis-universal\/versioned\/[0-9a-z-]+\//g, '/model-viewer/basis/'],
  [/https:\/\/cdn\.jsdelivr\.net\/npm\/three@[0-9.]+\/examples\/jsm\/loaders\/LottieLoader\.js/g, '/model-viewer/lottie/LottieLoader.js'],
];

export default {
  root: path.join(projectRoot, 'web'),
  publicDir: path.join(projectRoot, 'public'),
  plugins: [
    {
      name: 'go-web-spark-same-origin-worker',
      buildStart() { sparkWorkerTransformed = false; },
      transform(code, id) {
        if (path.resolve(id.split('?')[0]) !== path.resolve(sparkModulePath)) return null;
        if (!sparkWorkerWrapper.test(code)) throw new Error('Spark worker wrapper changed; review the same-origin adaptation before building');
        sparkWorkerTransformed = true;
        const adapted = code.replace(sparkWorkerWrapper, `function WorkerWrapper(options) {\n\treturn new Worker("/assets/spark-worker.js", { name: options?.name });\n}\n//#endregion`);
        return { code: adapted, map: null };
      },
      generateBundle() {
        if (!sparkWorkerTransformed) throw new Error('Spark ESM worker was not transformed; refusing a CSP-incompatible build');
        this.emitFile({ type: 'asset', fileName: 'assets/spark-worker.js', source: sparkWorkerSource });
        this.emitFile({ type: 'asset', fileName: 'assets/spark-LICENSE.txt', source: readFileSync(path.join(path.dirname(sparkPackagePath), 'LICENSE')) });
      },
    },
    {
      name: 'go-web-model-viewer-decoders',
      generateBundle() {
        for (const [dependencyPath, outputPath] of modelViewerDecoderAssets) {
          const sourcePath = modelViewerRequire.resolve(dependencyPath);
          this.emitFile({
            type: 'asset',
            fileName: outputPath,
            source: readFileSync(sourcePath),
          });
        }
        for (const [sourcePath, outputPath] of decoderLicenses) {
          this.emitFile({
            type: 'asset',
            fileName: outputPath,
            source: readFileSync(path.join(projectRoot, sourcePath)),
          });
        }
      },
    },
    {
      name: 'go-web-model-viewer-no-cdn-defaults',
      renderChunk(code) {
        let next = code;
        for (const [pattern, replacement] of modelViewerCdnDefaults) next = next.replace(pattern, replacement);
        return next === code ? null : { code: next, map: null };
      },
    },
    {
      name: 'go-web-favicon',
      closeBundle() {
        copyFileSync(
          path.join(projectRoot, 'src', 'app', 'favicon.ico'),
          path.join(goWebOutputRoot, 'favicon.ico'),
        );
      },
    },
  ],
  resolve: {
    alias: {
      '@': path.join(projectRoot, 'src'),
    },
  },
  build: {
    outDir: goWebOutputRoot,
    emptyOutDir: true,
    assetsDir: 'assets',
    sourcemap: true,
    rolldownOptions: {
      output: {
        codeSplitting: {
          // Keep the complete 3D runtime behind its existing dynamic import.
          // Separate stable renderer code for caching without external/CDN scripts.
          groups: [
            { name: 'three-core', test: /node_modules\/three\/build\/three\.core\.js$/ },
            { name: 'three-renderer', test: /node_modules\/three\/build\/three\.module\.js$/ },
          ],
        },
      },
    },
  },
};
