import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { afterEach, describe, expect, it, vi } from 'vitest';

/**
 * public/browser-support.js (#387) runs before the application bundle. Below the supported browser minimum it
 * draws the Product Schema's notice and sets `__PLINTH_UNSUPPORTED_BROWSER__`; otherwise it does nothing. It is a
 * plain classic script, so it is executed here the way a browser does, against stand-in globals.
 */
const source = readFileSync(join(__dirname, '..', '..', 'public', 'browser-support.js'), 'utf8');

class FakeElement {
  children: FakeElement[] = [];
  attributes: Record<string, string> = {};
  style: Record<string, string> = {};
  textContent = '';
  constructor(readonly tag: string) {}
  setAttribute(name: string, value: string) { this.attributes[name] = value; }
  appendChild(child: FakeElement) { this.children.push(child); return child; }
}

const messages = {
  'ui.browser_unsupported.title': 'Your browser is out of date',
  'ui.browser_unsupported.description': 'Use Chrome 111 or newer.',
};

type FakeWindow = { __ART_AI_PRODUCT_SCHEMA__?: unknown; __PLINTH_UNSUPPORTED_BROWSER__?: boolean };

/** Hides a built-in member for the caller's lifetime of the returned function; the runtime itself needs it back. */
function withoutMember(target: object, key: string): () => void {
  const descriptor = Object.getOwnPropertyDescriptor(target, key)!;
  Object.defineProperty(target, key, { ...descriptor, value: undefined });
  return () => { Object.defineProperty(target, key, descriptor); };
}

function run(options: { schema?: unknown; documentRoot?: FakeElement | null; hide?: () => () => void } = {}) {
  const root = options.documentRoot === undefined ? new FakeElement('div') : options.documentRoot;
  const body = new FakeElement('body');
  const window: FakeWindow = { __ART_AI_PRODUCT_SCHEMA__: 'schema' in options ? options.schema : { content: { messages } } };
  const document = { getElementById: () => root, createElement: (tag: string) => new FakeElement(tag), body };
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
  const restore = options.hide?.();
  try {
    new Function('window', 'document', source)(window, document);
  } finally {
    restore?.();
  }
  return { window, root, body, warn };
}

/** Every probe passes in this runtime except what the caller removes. */
function capableBrowser() {
  vi.stubGlobal('structuredClone', () => undefined);
  vi.stubGlobal('CSSLayerBlockRule', function CSSLayerBlockRule() { /* interface object */ });
  vi.stubGlobal('CSS', { supports: () => true });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('browser version guard (#387)', () => {
  it('does nothing in a browser that has everything', () => {
    capableBrowser();
    const { window, root } = run();
    expect(window.__PLINTH_UNSUPPORTED_BROWSER__).toBeUndefined();
    expect(root?.children).toHaveLength(0);
  });

  const lacking: Array<[string, () => () => void]> = [
    ['Object.hasOwn', () => withoutMember(Object, 'hasOwn')],
    ['Array.prototype.toSorted', () => withoutMember(Array.prototype, 'toSorted')],
    ['structuredClone', () => { vi.stubGlobal('structuredClone', undefined); return () => undefined; }],
    ['CSS @layer', () => { vi.stubGlobal('CSSLayerBlockRule', undefined); return () => undefined; }],
    ['CSS color-mix()', () => { vi.stubGlobal('CSS', { supports: (_property: string, value: string) => !value.startsWith('color-mix') }); return () => undefined; }],
    ['CSS oklch()', () => { vi.stubGlobal('CSS', { supports: (_property: string, value: string) => !value.startsWith('oklch') }); return () => undefined; }],
  ];
  it.each(lacking)('draws the Schema notice and stops the app start when %s is missing', (probe, hide) => {
    capableBrowser();
    const { window, root, warn } = run({ hide });
    expect(window.__PLINTH_UNSUPPORTED_BROWSER__).toBe(true);
    const [panel] = root!.children;
    expect(panel.tag).toBe('main');
    expect(panel.attributes).toMatchObject({ role: 'alert', 'data-missing': probe });
    expect(panel.children.map((child) => [child.tag, child.textContent])).toEqual([
      ['h1', messages['ui.browser_unsupported.title']],
      ['p', messages['ui.browser_unsupported.description']],
    ]);
    // Inline styles: the stylesheet is what an unsupported browser cannot apply.
    expect(panel.style.background).toBe('#ffffff');
    expect(warn).toHaveBeenCalledWith(expect.stringContaining(probe));
  });

  it('lists everything an old browser lacks', () => {
    capableBrowser();
    vi.stubGlobal('CSSLayerBlockRule', undefined);
    vi.stubGlobal('CSS', undefined);
    vi.stubGlobal('structuredClone', undefined);
    const { root } = run();
    expect(root!.children[0].attributes['data-missing']).toBe('structuredClone,CSS @layer,CSS color-mix(),CSS oklch()');
  });

  it('draws into the page body when there is no root element', () => {
    capableBrowser();
    vi.stubGlobal('CSSLayerBlockRule', undefined);
    const { body } = run({ documentRoot: null });
    expect(body.children).toHaveLength(1);
  });

  it('stays out of the way when the Schema has no notice text, so the app still tries to start', () => {
    capableBrowser();
    vi.stubGlobal('CSSLayerBlockRule', undefined);
    for (const schema of [undefined, {}, { content: { messages: {} } }, { content: { messages: { 'ui.browser_unsupported.title': 'x' } } }]) {
      const { window, root } = run({ schema });
      expect(window.__PLINTH_UNSUPPORTED_BROWSER__).toBeUndefined();
      expect(root?.children).toHaveLength(0);
    }
  });

  it('is ES5 syntax, so the oldest browsers can run it at all', () => {
    const code = source.split('\n').filter((line) => !line.trim().startsWith('//')).join('\n');
    expect(code).not.toMatch(/=>|\b(?:let|const|class|async|await)\b|`|\.\.\.|\?\./);
  });
});
