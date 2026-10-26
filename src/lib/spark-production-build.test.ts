import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '../..');

function productionBuild(skipTransform = false) {
  // Vitest sets NODE_ENV=test. A fresh production process exercises the same
  // dependency exports and replacement rules as pnpm build, not a dev bundle.
  const script = `
    import { build } from 'vite';
    import config from './vite.go-web.config.mjs';
    config.plugins = config.plugins.filter(p => p.name !== 'go-web-favicon');
    if (${skipTransform}) config.plugins.find(p => p.name === 'go-web-spark-same-origin-worker').transform = () => null;
    try {
      const result = await build({ ...config, configFile: false, logLevel: 'silent',
        build: { ...config.build, write: false, sourcemap: false } });
      const outputs = (Array.isArray(result) ? result : [result]).flatMap(item => item.output);
      const chunks = outputs.filter(item => item.type === 'chunk');
      const source = chunks.map(item => item.code).join('\\n');
      const ids = chunks.flatMap(item => Object.keys(item.modules));
      const worker = outputs.find(item => item.fileName === 'assets/spark-worker.js');
      console.log(JSON.stringify({
        esm: ids.some(id => id.endsWith('/spark/dist/spark.module.js')),
        cjs: ids.some(id => id.endsWith('/spark/dist/spark.cjs.js')),
        sameOrigin: source.includes('/assets/spark-worker.js'),
        dataWrapper: source.includes('data:text/javascript;charset=utf-8,'),
        blobWrapper: source.includes('revokeObjectURL(self.location.href)'),
        worker: worker?.type === 'asset' && String(worker.source).includes('self.addEventListener("message", onMessage)'),
        license: outputs.some(item => item.fileName === 'assets/spark-LICENSE.txt'),
      }));
    } catch (error) { console.log(JSON.stringify({ error: String(error) })); }
  `;
  return JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', script], {
    cwd: root, env: { ...process.env, NODE_ENV: 'production' }, encoding: 'utf8', timeout: 30_000,
  }));
}

describe('Spark production worker contract', () => {
  it('rewrites the ESM actually bundled by Vite and emits its same-origin worker', () => {
    expect(productionBuild()).toEqual({
      esm: true, cjs: false, sameOrigin: true, dataWrapper: false, blobWrapper: false, worker: true, license: true,
    });
  }, 35_000);

  it('fails the production build if the adaptation never transforms the imported module', () => {
    // Reproduce an unmatched module identity, rather than manufacture an error.
    expect(productionBuild(true).error).toMatch(/Spark.*not.*transformed/i);
  }, 35_000);
});
