import { afterEach, describe, expect, it, vi } from 'vitest';
import { createAssetURLRewriter, rememberedAddressCount, safeAssetURL, safePreviewURL } from './asset-url-rewriter';
import type { ProductSchema } from '@/lib/product-schema';

const schema = {} as ProductSchema;

afterEach(() => vi.unstubAllGlobals());

describe('browser asset URL isolation', () => {
  it('keeps native asset paths and same-origin resources', () => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    const rewrite = createAssetURLRewriter(schema);
    expect(rewrite.proxyAssetUrl('/api/v1/tasks/task-a/assets/asset-b')).toBe('/api/v1/tasks/task-a/assets/asset-b');
    expect(safeAssetURL('http://art-ai.local:13333/api/v1/assets/input')).toBe('/api/v1/assets/input');
    expect(safeAssetURL('/assets/logo.png')).toBe('/assets/logo.png');
    expect(safeAssetURL('blob:http://art-ai.local:13333/1b4c99aa-508a-4966-a6d9-2895be4852b0'))
      .toBe('blob:http://art-ai.local:13333/1b4c99aa-508a-4966-a6d9-2895be4852b0');
  });

  it.each([
    'https://cdn.example.invalid/asset.png',
    '//cdn.example.invalid/asset.png',
    'https://unknown.invalid/file.glb',
    '/api/meshy/proxy-asset?url=https%3A%2F%2Fcdn.example.invalid%2Fasset.png',
    '/api/v1/assets/x?url=https%253A%252F%252Fcdn.example.invalid%252Fasset.png',
    '/api/meshy/proxy-asset?taskId=task-a',
    'javascript:alert(1)',
    'data:image/svg+xml,<svg/>',
  ])('rejects remote or legacy browser resources: %s', (url) => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    expect(safeAssetURL(url)).toBe('');
    expect(createAssetURLRewriter(schema).proxyAssetUrl(url)).toBe('');
  });
});

describe('remembered address checks (#290)', () => {
  it('answers a repeated address the same way, and never with an answer made for another origin', () => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    expect(safeAssetURL('http://art-ai.local:13333/api/v1/assets/a')).toBe('/api/v1/assets/a');
    expect(safeAssetURL('http://art-ai.local:13333/api/v1/assets/a')).toBe('/api/v1/assets/a');
    vi.stubGlobal('window', { location: { origin: 'http://other.local:13333' } });
    expect(safeAssetURL('http://art-ai.local:13333/api/v1/assets/a')).toBe('');
    expect(safeAssetURL(' /api/v1/assets/a')).toBe('');
  });

  it('does not keep long values such as a composer data URL as a remembered key', () => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    const picture = `data:image/png;base64,${'A'.repeat(1 << 20)}`;
    const before = rememberedAddressCount();
    expect(safeAssetURL(picture)).toBe('');
    expect(safeAssetURL(picture)).toBe('');
    expect(rememberedAddressCount()).toBe(before);
    const rewriter = createAssetURLRewriter({} as ProductSchema);
    expect(rewriter.previewAssetUrl(picture)).toBe('');
    expect(rememberedAddressCount()).toBe(before);
  });
});

describe('picture previews (#290)', () => {
  const previewSchema = {
    runtime: {
      http: { routes: { task_asset: '/api/v1/tasks/{taskID}/assets/{assetID}', asset_item: '/api/v1/assets/{assetID}' } },
      asset_delivery: { asset_preview: { query: 'size', request_value: 'preview' } },
    },
  } as unknown as ProductSchema;

  it('asks a task output or an input asset for its preview and leaves every other address as it is', () => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    const { previewAssetUrl } = createAssetURLRewriter(previewSchema);
    expect(previewAssetUrl('/api/v1/tasks/task-a/assets/asset-b')).toBe('/api/v1/tasks/task-a/assets/asset-b?size=preview');
    expect(previewAssetUrl('http://art-ai.local:13333/api/v1/tasks/task-a/assets/asset-b?v=2')).toBe('/api/v1/tasks/task-a/assets/asset-b?v=2&size=preview');
    // An explicit size is the caller's choice.
    expect(previewAssetUrl('/api/v1/tasks/task-a/assets/asset-b?size=thumb')).toBe('/api/v1/tasks/task-a/assets/asset-b?size=thumb');
    expect(previewAssetUrl('/api/v1/assets/input-a')).toBe('/api/v1/assets/input-a?size=preview');
    // Other routes and local bitmaps have no preview.
    expect(previewAssetUrl('/api/v1/tasks/task-a/assets/asset-b/extra')).toBe('/api/v1/tasks/task-a/assets/asset-b/extra');
    expect(previewAssetUrl('/api/v1/assets/input-a/model-preview')).toBe('/api/v1/assets/input-a/model-preview');
    expect(previewAssetUrl('/assets/logo.png')).toBe('/assets/logo.png');
    expect(previewAssetUrl('blob:http://art-ai.local:13333/1b4c99aa-508a-4966-a6d9-2895be4852b0'))
      .toBe('blob:http://art-ai.local:13333/1b4c99aa-508a-4966-a6d9-2895be4852b0');
    // Unsafe addresses stay refused.
    expect(previewAssetUrl('https://cdn.example.invalid/asset.png')).toBe('');
  });

  it('serves originals when the Schema declares no preview', () => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    expect(createAssetURLRewriter(schema).previewAssetUrl('/api/v1/tasks/task-a/assets/asset-b')).toBe('/api/v1/tasks/task-a/assets/asset-b');
  });
});

describe('old composer data: pictures (#291 acceptance, #292)', () => {
  const big = `data:image/png;base64,${'A'.repeat(17 << 20)}`;

  it('refuses a data: URL as a resource address without scanning it', () => {
    vi.stubGlobal('window', { location: { origin: 'http://art-ai.local:13333' } });
    const trim = vi.spyOn(String.prototype, 'trim');
    const test = vi.spyOn(RegExp.prototype, 'test');
    const parse = vi.spyOn(globalThis, 'URL');
    const decode = vi.spyOn(globalThis, 'decodeURIComponent');
    const answers: Array<[string, string]> = [];
    const scans = { trim: 0, test: 0, parse: 0, decode: 0 };
    try {
      for (let i = 0; i < 20; i++) {
        answers.push([
          safeAssetURL(big),
          createAssetURLRewriter({} as ProductSchema).previewAssetUrl(big),
        ]);
      }
    } finally {
      // Read the counts before mockRestore clears them.
      scans.trim = trim.mock.calls.length;
      scans.test = test.mock.calls.length;
      scans.parse = parse.mock.calls.length;
      scans.decode = decode.mock.calls.length;
      trim.mockRestore();
      test.mockRestore();
      parse.mockRestore();
      decode.mockRestore();
    }
    expect(answers).toEqual(Array.from({ length: 20 }, () => ['', '']));
    expect(scans).toEqual({ trim: 0, test: 0, parse: 0, decode: 0 });
  });

  it('shows a raster data: picture by its header only, and still refuses other data: kinds', () => {
    const trim = vi.spyOn(String.prototype, 'trim');
    const parse = vi.spyOn(globalThis, 'URL');
    const decode = vi.spyOn(globalThis, 'decodeURIComponent');
    const header = RegExp.prototype.test;
    const tested: string[] = [];
    const test = vi.spyOn(RegExp.prototype, 'test').mockImplementation(function (this: RegExp, value: string) {
      tested.push(value);
      return header.call(this, value);
    });
    const previews: string[] = [];
    const scans = { trim: 0, parse: 0, decode: 0 };
    try {
      for (let i = 0; i < 20; i++) previews.push(safePreviewURL(big));
    } finally {
      // Read the counts before mockRestore clears them.
      scans.trim = trim.mock.calls.length;
      scans.parse = parse.mock.calls.length;
      scans.decode = decode.mock.calls.length;
      test.mockRestore();
      trim.mockRestore();
      parse.mockRestore();
      decode.mockRestore();
    }
    expect(previews).toHaveLength(20);
    expect(previews.every((preview) => preview === big)).toBe(true);
    // The header check must receive the bounded prefix, never the whole picture's base64.
    expect(tested).toHaveLength(20);
    expect(tested.every((value) => value.length <= 64 && big.startsWith(value))).toBe(true);
    expect(scans).toEqual({ trim: 0, parse: 0, decode: 0 });
    expect(safePreviewURL('data:image/svg+xml;base64,PHN2Zy8+')).toBe('');
    expect(safePreviewURL('data:text/html;base64,PGI+')).toBe('');
    expect(safePreviewURL('data:image/png;base64,')).toBe('');
  });
});
