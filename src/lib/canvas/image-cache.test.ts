import { afterEach, describe, expect, it, vi } from 'vitest';

import { CanvasImageCache, decodePicture, pictureKeys, pictureSize, splitPictureKey, type CanvasPicture } from './image-cache';

interface PendingLoad {
  source: string;
  signal: AbortSignal;
  finish: (width?: number) => CanvasPicture;
  fail: () => void;
}

function setup() {
  const loads: PendingLoad[] = [];
  const onChange = vi.fn();
  const cache = new CanvasImageCache({
    resolve: (url) => (url.startsWith('/api/') ? url : ''),
    onChange,
    load: (source, signal) => new Promise<CanvasPicture>((resolve, reject) => {
      loads.push({
        source,
        signal,
        finish: (width = 2048) => {
          const picture = { width, height: width, close: () => {} } as unknown as ImageBitmap;
          resolve(picture);
          return picture;
        },
        fail: () => reject(new Error('broken')),
      });
    }),
  });
  const pending = (url: string) => loads.filter((load) => load.source === url).at(-1)!;
  // Promise callbacks run on the microtask queue.
  const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
  return { cache, onChange, loads, pending, settle };
}

const turn = (prefix: string, count: number) => Array.from({ length: count }, (_, index) => `/api/v1/assets/${prefix}-${index}`);

describe('CanvasImageCache (#287, #290)', () => {
  it('loads each wanted picture once, however often the same layers sync', () => {
    const { cache, loads } = setup();
    const a = turn('a', 9);
    cache.sync(a, a);
    cache.sync(a, a);
    expect(loads).toHaveLength(9);
    expect(cache.size).toBe(9);
  });

  it('hands out a picture only once it is decoded, and asks the board to redraw then', async () => {
    const { cache, onChange, pending, settle } = setup();
    const url = '/api/v1/assets/x';
    cache.sync([url], [url]);
    expect(cache.has(url)).toBe(true);
    expect(cache.get(url)).toBeUndefined();
    const picture = pending(url).finish(3840);
    await settle();
    expect(cache.get(url)).toBe(picture);
    expect(pictureSize(cache.get(url))).toEqual({ width: 3840, height: 3840 });
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it('lets go of pictures no layer names any more instead of growing with every replacement', async () => {
    const { cache, loads, pending, settle } = setup();
    const a = turn('a', 9);
    const b = turn('b', 12);
    cache.sync(a, a);
    for (const url of a) pending(url).finish();
    await settle();
    cache.sync(b, b);
    expect(cache.size).toBe(12);
    expect(a.some((url) => cache.has(url))).toBe(false);
    cache.sync([], []);
    expect(cache.size).toBe(0);
    // Coming back loads A again: 9 + 12 + 9 loads over time, but only the current 9 are held.
    cache.sync(a, a);
    expect(loads).toHaveLength(30);
    expect(cache.size).toBe(9);
  });

  it('stops a download that is no longer needed and ignores its late result', async () => {
    const { cache, onChange, pending, settle } = setup();
    cache.sync(['/api/v1/assets/x'], ['/api/v1/assets/x']);
    const load = pending('/api/v1/assets/x');
    cache.sync([], []);
    expect(load.signal.aborted).toBe(true);
    load.finish();
    await settle();
    expect(cache.has('/api/v1/assets/x')).toBe(false);
    expect(onChange).not.toHaveBeenCalled();
  });

  it('leaves a decoded picture open when letting go of it (a brush layer may still draw it)', async () => {
    const { cache, pending, settle } = setup();
    cache.sync(['/api/v1/assets/x'], ['/api/v1/assets/x']);
    const picture = pending('/api/v1/assets/x').finish();
    const close = vi.spyOn(picture as ImageBitmap, 'close');
    await settle();
    cache.sync([], []);
    expect(close).not.toHaveBeenCalled();
  });

  it('drops a failed picture so the next sync asks for it again', async () => {
    const { cache, onChange, loads, pending, settle } = setup();
    const url = '/api/v1/assets/flaky';
    cache.sync([url], [url]);
    pending(url).fail();
    await settle();
    expect(cache.has(url)).toBe(false);
    expect(onChange).toHaveBeenCalledTimes(1);
    cache.sync([url], [url]);
    expect(loads).toHaveLength(2);
    pending(url).finish();
    await settle();
    expect(pictureSize(cache.get(url))?.width).toBe(2048);
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it('does not fetch a hidden layer\'s picture, but keeps one already loaded while it is hidden', async () => {
    const { cache, loads, pending, settle } = setup();
    const shown = '/api/v1/assets/shown';
    const hidden = '/api/v1/assets/hidden';
    cache.sync([shown, hidden], [shown]);
    expect(cache.has(hidden)).toBe(false);
    expect(loads).toHaveLength(1);
    pending(shown).finish();
    await settle();
    cache.sync([shown, hidden], [hidden]);
    expect(cache.get(shown)).toBeDefined();
    expect(loads).toHaveLength(2);
  });

  it('skips addresses that may not be fetched', () => {
    const { cache, loads } = setup();
    cache.sync(['javascript:alert(1)', ''], ['javascript:alert(1)', '']);
    expect(loads).toHaveLength(0);
    expect(cache.size).toBe(0);
  });

  it('keeps a picture put in by the brush once its layer names it, and stops loading the one it replaces', async () => {
    const { cache, pending, settle } = setup();
    const url = '/api/v1/assets/painted';
    cache.sync([url], [url]);
    const loading = pending(url);
    const uploaded = { width: 512, height: 512 } as unknown as ImageBitmap;
    cache.set(url, uploaded);
    expect(loading.signal.aborted).toBe(true);
    loading.finish();
    await settle();
    cache.sync([url], [url]);
    expect(cache.get(url)).toBe(uploaded);
  });

  it('clear lets go of everything and the cache can load again', async () => {
    const { cache, onChange, pending, settle } = setup();
    const url = '/api/v1/assets/x';
    cache.sync([url], [url]);
    const load = pending(url);
    cache.clear();
    expect(cache.size).toBe(0);
    expect(load.signal.aborted).toBe(true);
    load.finish();
    await settle();
    expect(onChange).not.toHaveBeenCalled();
    cache.sync([url], [url]);
    expect(cache.size).toBe(1);
  });
});

describe('pictureSize', () => {
  it('reads bitmaps and finished elements, and nothing from a loading or broken element', () => {
    expect(pictureSize({ width: 30, height: 20 } as ImageBitmap)).toEqual({ width: 30, height: 20 });
    expect(pictureSize({ width: 0, height: 0 } as ImageBitmap)).toBeNull();
    const element = (complete: boolean, naturalWidth: number) => ({ complete, naturalWidth, naturalHeight: naturalWidth }) as HTMLImageElement;
    expect(pictureSize(element(true, 64))).toEqual({ width: 64, height: 64 });
    expect(pictureSize(element(false, 0))).toBeNull();
    expect(pictureSize(element(true, 0))).toBeNull();
    expect(pictureSize(undefined)).toBeNull();
  });
});

describe('decodePicture', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('downloads the bytes and decodes them with createImageBitmap', async () => {
    const blob = new Blob(['png']);
    const fetcher = vi.fn().mockResolvedValue({ ok: true, blob: () => Promise.resolve(blob) });
    const bitmap = { width: 4, height: 4 };
    const decode = vi.fn().mockResolvedValue(bitmap);
    vi.stubGlobal('fetch', fetcher);
    vi.stubGlobal('createImageBitmap', decode);
    const signal = new AbortController().signal;
    await expect(decodePicture('/api/v1/assets/x', signal)).resolves.toBe(bitmap);
    expect(fetcher).toHaveBeenCalledWith('/api/v1/assets/x', { credentials: 'same-origin', signal });
    expect(decode).toHaveBeenCalledWith(blob);
  });

  it('rejects a failed response without decoding, and does not decode once aborted', async () => {
    const decode = vi.fn();
    vi.stubGlobal('createImageBitmap', decode);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404 }));
    await expect(decodePicture('/api/v1/assets/x', new AbortController().signal)).rejects.toThrow('404');
    const controller = new AbortController();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, blob: () => { controller.abort(); return Promise.resolve(new Blob(['png'])); } }));
    await expect(decodePicture('/api/v1/assets/x', controller.signal)).rejects.toBeDefined();
    expect(decode).not.toHaveBeenCalled();
  });
});

describe('pictureKeys', () => {
  it('names image layers only, with hidden ones referenced but not wanted, and ignores moves', () => {
    const nodes = [
      { type: 'image', imageUrl: '/api/v1/assets/a', x: 0 },
      { type: 'image', imageUrl: '/api/v1/assets/b', visible: false, x: 0 },
      { type: 'image', imageUrl: '', blank: true, x: 0 },
      { type: 'text', imageUrl: '', x: 0 },
    ];
    const keys = pictureKeys(nodes);
    expect(splitPictureKey(keys.referenced)).toEqual(['/api/v1/assets/a', '/api/v1/assets/b']);
    expect(splitPictureKey(keys.wanted)).toEqual(['/api/v1/assets/a']);
    expect(pictureKeys(nodes.map((node) => ({ ...node, x: 40 })))).toEqual(keys);
    expect(splitPictureKey(pictureKeys([]).referenced)).toEqual([]);
  });
});
