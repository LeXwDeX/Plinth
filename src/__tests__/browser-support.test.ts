import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import ts from 'typescript';
import { describe, expect, it } from 'vitest';

import { browserFloor, buildTargets } from '../../scripts/browser-floor.mjs';
import viteConfig from '../../vite.go-web.config.mjs';

/**
 * Supported-browser guard (#387). The minimum versions are declared once, in package.json's `browserslist`; this
 * file keeps everything else that depends on them in step:
 * - the Vite build target is that floor;
 * - tsconfig's ES `lib` is the newest ECMAScript library the floor can run, so `tsc --noEmit` (a gate) rejects
 *   `Object.hasOwn`-style built-ins that the floor lacks, on the real receiver types, with no list to maintain;
 * - Web APIs newer than the floor sit outside `lib` (lib.dom.d.ts is always current), so a short table of known
 *   ones is scanned for in the browser sources;
 * - the Schema notice for a browser below the floor names the same versions.
 * Dependencies are not covered: only this repository's own sources are checked.
 */

type Floor = Record<string, string>;
const floor = browserFloor() as Floor;
const projectRoot = join(__dirname, '..', '..');

/**
 * The first version of each browser from which the headline additions of an ECMAScript year all work (MDN
 * browser-compat-data; Edge follows Chrome). Used to find the newest `lib` a floor allows.
 * es2021: replaceAll, Promise.any, WeakRef. es2022: Array/String.at, Object.hasOwn, Error.cause, static blocks.
 * es2023: toSorted/toReversed/with, findLast, Symbols as WeakMap keys. es2024: Object.groupBy, Promise.withResolvers.
 */
const ES_LIBS = [
  { lib: 'es2020', chrome: 80, firefox: 74, safari: 14 },
  { lib: 'es2021', chrome: 85, firefox: 79, safari: 14.1 },
  { lib: 'es2022', chrome: 94, firefox: 93, safari: 16.4 },
  { lib: 'es2023', chrome: 110, firefox: 115, safari: 16.4 },
  { lib: 'es2024', chrome: 119, firefox: 122, safari: 17.4 },
];

function newestEsLib(minimum: Floor): string {
  const usable = ES_LIBS.filter((row) => (
    row.chrome <= Number(minimum.chrome) && row.chrome <= Number(minimum.edge)
    && row.firefox <= Number(minimum.firefox) && row.safari <= Number(minimum.safari)
  ));
  return usable[usable.length - 1].lib;
}

/** TypeScript diagnostics for `source` compiled against the given ES lib plus the DOM lib. */
function diagnose(source: string, esLib: string): string[] {
  const fileName = '/virtual/snippet.ts';
  const options: ts.CompilerOptions = {
    lib: [`lib.${esLib}.d.ts`, 'lib.dom.d.ts'], strict: true, noEmit: true, skipLibCheck: true, types: [], target: ts.ScriptTarget.ES2022,
  };
  const host = ts.createCompilerHost(options);
  const getSourceFile = host.getSourceFile.bind(host);
  host.getSourceFile = (name, languageVersion, ...rest) => (
    name === fileName ? ts.createSourceFile(name, source, languageVersion) : getSourceFile(name, languageVersion, ...rest)
  );
  const fileExists = host.fileExists.bind(host);
  host.fileExists = (name) => name === fileName || fileExists(name);
  const program = ts.createProgram([fileName], options, host);
  return ts.getPreEmitDiagnostics(program).map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n'));
}

/**
 * Web APIs that a browser in the supported range may lack. `object` narrows a static member (`AbortSignal.any`);
 * without it any receiver counts. First release with the API, per MDN browser-compat-data (Edge follows Chrome).
 * Feature-detected use is legitimate: say so with a `browser-support: allow` comment on that line or the one above.
 */
interface WebApi { object?: string; property: string; chrome: number; firefox: number; safari: number }
const NEWER_WEB_APIS: WebApi[] = [
  { object: 'AbortSignal', property: 'any', chrome: 116, firefox: 124, safari: 17.4 },
  { object: 'URL', property: 'canParse', chrome: 120, firefox: 115, safari: 17 },
  { property: 'showPopover', chrome: 114, firefox: 125, safari: 17 },
  { property: 'hidePopover', chrome: 114, firefox: 125, safari: 17 },
  { property: 'togglePopover', chrome: 114, firefox: 125, safari: 17 },
  { property: 'setHTMLUnsafe', chrome: 124, firefox: 131, safari: 17.4 },
  { property: 'checkVisibility', chrome: 105, firefox: 106, safari: 17.4 },
  { property: 'startViewTransition', chrome: 111, firefox: 144, safari: 18 },
];

function beyondFloor(api: WebApi, minimum: Floor): boolean {
  return api.chrome > Number(minimum.chrome) || api.chrome > Number(minimum.edge)
    || api.firefox > Number(minimum.firefox) || api.safari > Number(minimum.safari);
}

function newerWebApiUses(fileName: string, source: string, minimum: Floor): string[] {
  const file = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, fileName.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const lines = source.split('\n');
  const found: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isPropertyAccessExpression(node)) {
      const api = NEWER_WEB_APIS.find((candidate) => candidate.property === node.name.text
        && (!candidate.object || (ts.isIdentifier(node.expression) && node.expression.text === candidate.object)));
      if (api && beyondFloor(api, minimum)) {
        const { line } = file.getLineAndCharacterOfPosition(node.name.getStart(file));
        const allowed = /browser-support: allow/.test(lines[line]) || (line > 0 && /browser-support: allow/.test(lines[line - 1]));
        if (!allowed) found.push(`${fileName}:${line + 1} ${api.object ? `${api.object}.` : '.'}${api.property}`);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
}

/** Browser sources: everything under src/ and web/src/ except tests, which run in Node. */
function browserSources(dir: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      if (entry !== 'node_modules' && entry !== '__tests__') files.push(...browserSources(path));
    } else if (/\.tsx?$/.test(entry) && !/\.test\./.test(entry)) {
      files.push(path);
    }
  }
  return files;
}

describe('the declared browser floor (#387)', () => {
  it('is Tailwind v4\'s: Chrome and Edge 111, Firefox 128, Safari 16.4', () => {
    // Tailwind v4 compiles its CSS (cascade layers, color-mix(), oklch(), @property) for exactly these versions, so
    // a lower floor could not be honoured however the scripts were written. Chrome 92 reports (#387) are below it.
    expect(floor).toEqual({ chrome: '111', edge: '111', firefox: '128', safari: '16.4' });
  });

  it('is the build target', () => {
    expect(viteConfig.build.target).toEqual(buildTargets(floor));
    expect(buildTargets(floor)).toEqual(['chrome111', 'edge111', 'firefox128', 'safari16.4']);
  });

  it('is what the ES lib in tsconfig.json allows, so tsc rejects built-ins the floor lacks', () => {
    const tsconfig = JSON.parse(readFileSync(join(projectRoot, 'tsconfig.json'), 'utf8'));
    expect(tsconfig.compilerOptions.lib).toEqual(['dom', 'dom.iterable', newestEsLib(floor)]);
  });

  it('lets the type checker reject Object.hasOwn below Chrome 94, and groupBy / withResolvers at this floor', () => {
    const snippet = 'Object.hasOwn({}, "a"); [3, 1].toSorted(); Object.groupBy([1], (n) => String(n)); Promise.withResolvers<number>();';
    const reject = (esLib: string) => diagnose(snippet, esLib).filter((message) => message.includes('does not exist on type'));
    // A floor of Chrome 92 / Firefox 91 / Safari 15 (the reported Edge 92) can only use the ES2021 library.
    expect(newestEsLib({ chrome: '92', edge: '92', firefox: '91', safari: '15' })).toBe('es2021');
    const atChrome92 = reject('es2021');
    expect(atChrome92.some((message) => message.includes("'hasOwn'"))).toBe(true);
    expect(atChrome92.some((message) => message.includes("'toSorted'"))).toBe(true);
    // At this floor Object.hasOwn and toSorted are fine, the ES2024 additions are not.
    const atFloor = reject(newestEsLib(floor));
    expect(atFloor.some((message) => message.includes("'hasOwn'") || message.includes("'toSorted'"))).toBe(false);
    expect(atFloor.some((message) => message.includes("'groupBy'"))).toBe(true);
    expect(atFloor.some((message) => message.includes("'withResolvers'"))).toBe(true);
  }, 60_000);

  it('is not undercut by a Web API newer than it in the browser sources', () => {
    const files = [...browserSources(join(projectRoot, 'src')), ...browserSources(join(projectRoot, 'web', 'src'))];
    expect(files.length).toBeGreaterThan(100);
    const uses = files.flatMap((file) => newerWebApiUses(relative(projectRoot, file), readFileSync(file, 'utf8'), floor));
    expect(uses).toEqual([]);
  });

  it('finds a newer Web API, follows the floor, and honours the allow comment', () => {
    const snippet = 'const signal = AbortSignal.any([a, b]);\nel.showPopover();\nconst timeout = AbortSignal.timeout(5);\nel?.checkVisibility?.();';
    expect(newerWebApiUses('a.ts', snippet, floor)).toEqual(['a.ts:1 AbortSignal.any', 'a.ts:2 .showPopover', 'a.ts:4 .checkVisibility']);
    expect(newerWebApiUses('a.ts', `// browser-support: allow (feature-detected)\n${snippet}`, floor)).toEqual(['a.ts:3 .showPopover', 'a.ts:5 .checkVisibility']);
    expect(newerWebApiUses('a.ts', 'AbortSignal.any([]); el.showPopover();', { chrome: '125', edge: '125', firefox: '131', safari: '18' })).toEqual([]);
    expect(newerWebApiUses('b.ts', 'other.any([]); x.canParse();', floor)).toEqual([]);
  });

  it('is what the Schema notice for an older browser tells people', () => {
    const catalog = JSON.parse(readFileSync(join(projectRoot, 'internal', 'catalog', 'default_catalog.json'), 'utf8'));
    const texts: Record<string, string[]> = { title: [], description: [] };
    const walk = (value: unknown) => {
      if (!value || typeof value !== 'object') return;
      for (const [key, child] of Object.entries(value)) {
        if (key === 'ui.browser_unsupported.title' && typeof child === 'string') texts.title.push(child);
        else if (key === 'ui.browser_unsupported.description' && typeof child === 'string') texts.description.push(child);
        else walk(child);
      }
    };
    walk(catalog);
    // The default language and the English bundle.
    expect(texts.title).toHaveLength(2);
    expect(texts.description).toHaveLength(2);
    for (const description of texts.description) {
      for (const version of new Set(Object.values(floor))) expect(description).toContain(version);
    }
  });
});
