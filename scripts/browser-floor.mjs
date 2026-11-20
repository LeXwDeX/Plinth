// The minimum supported browser versions are declared once, in package.json's `browserslist` (#387). The build
// target (vite.go-web.config.mjs) and the compatibility checks (src/__tests__/browser-support.test.ts) both read
// them from here instead of repeating the numbers. Only `<browser> >= <version>` is accepted; any other query is
// an error, so a typo cannot be silently ignored.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const QUERY = /^(chrome|edge|firefox|safari) >= (\d+(?:\.\d+)?)$/;

/**
 * The declared floor, e.g. `{ chrome: '111', edge: '111', firefox: '128', safari: '16.4' }`.
 * @param {string} [packageJsonPath]
 * @returns {Record<string, string>}
 */
export function browserFloor(packageJsonPath = path.join(projectRoot, 'package.json')) {
  const { browserslist } = JSON.parse(readFileSync(packageJsonPath, 'utf8'));
  if (!Array.isArray(browserslist) || browserslist.length === 0) {
    throw new Error('package.json has no browserslist: the minimum browser versions are not declared');
  }
  /** @type {Record<string, string>} */
  const floor = {};
  for (const query of browserslist) {
    const match = QUERY.exec(String(query));
    if (!match || match[1] in floor) {
      throw new Error(`browserslist entry not understood or repeated: ${query} (expected "<chrome|edge|firefox|safari> >= <version>")`);
    }
    floor[match[1]] = match[2];
  }
  return floor;
}

/**
 * The floor as Vite `build.target` values: `['chrome111', 'edge111', 'firefox128', 'safari16.4']`.
 * @param {Record<string, string>} [floor]
 * @returns {string[]}
 */
export function buildTargets(floor = browserFloor()) {
  return Object.entries(floor).map(([name, version]) => `${name}${version}`);
}
