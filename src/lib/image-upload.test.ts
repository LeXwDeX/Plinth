import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import {
  IMAGE_UPLOAD_NAME_CACHE_MAX_BYTES,
  imageUploadAccept,
  imageUploadName,
  imageUploadNameCacheBytes,
  prepareImageUpload,
  prepareImageUploadBlob,
} from './image-upload';

const policy = defaultCatalog.application.runtime.frontend.image_upload;
const accept = 'image/jpeg,image/png,image/webp,image/gif';
const options = { accept, maximumBytes: 1024, policy };
const pngBytes = Uint8Array.from(Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a5WQAAAAASUVORK5CYII=',
  'base64',
));
let output: Blob | null;
let decodeFailure: boolean;
let imageWidth: number;
let imageHeight: number;
let readFailure: boolean;
const drawImage = vi.fn();
const createCanvas = vi.fn();
const createObjectURL = vi.fn(() => 'blob:test-image');
const revokeObjectURL = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  output = new Blob([pngBytes], { type: 'image/png' });
  decodeFailure = false;
  readFailure = false;
  imageWidth = 2;
  imageHeight = 1;
  createCanvas.mockImplementation(() => ({
    width: 0,
    height: 0,
    getContext: () => ({ drawImage }),
    toBlob: (callback: BlobCallback) => callback(output),
  }));
  vi.stubGlobal('document', { createElement: createCanvas });
  vi.spyOn(URL, 'createObjectURL').mockImplementation(createObjectURL);
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(revokeObjectURL);
  vi.stubGlobal('Image', class {
    naturalWidth = imageWidth;
    naturalHeight = imageHeight;
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    set src(value: string) {
      if (value) queueMicrotask(() => decodeFailure ? this.onerror?.() : this.onload?.());
    }
  });
  vi.stubGlobal('FileReader', class {
    result: string | null = null;
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    async readAsDataURL(blob: Blob) {
      if (readFailure) {
        this.onerror?.();
        return;
      }
      this.result = `data:${blob.type};base64,${Buffer.from(await blob.arrayBuffer()).toString('base64')}`;
      this.onload?.();
    }
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('image upload conversion', () => {
  it.each(['image/png', 'image/jpeg', 'image/webp', 'image/gif'])(
    'returns original %s upload bytes without Data URI serialization or fetch', async (type) => {
      const fetch = vi.fn(() => { throw new Error('Data URI fetch is blocked by CSP'); });
      vi.stubGlobal('fetch', fetch);
      readFailure = true;
      const file = new File([pngBytes], 'reference', { type });
      const blob = await prepareImageUploadBlob(file, options);
      expect(blob).toBe(file);
      expect(new Uint8Array(await blob.arrayBuffer())).toEqual(pngBytes);
      expect(fetch).not.toHaveBeenCalled();
      expect(createObjectURL).not.toHaveBeenCalled();
      expect(createCanvas).not.toHaveBeenCalled();
    },
  );

  it('returns converted BMP as a PNG Blob without Data URI serialization or fetch', async () => {
    const fetch = vi.fn(() => { throw new Error('Data URI fetch is blocked by CSP'); });
    vi.stubGlobal('fetch', fetch);
    readFailure = true;
    const file = new File(['BM-original-bitmap'], 'reference.bmp', { type: 'image/bmp' });
    const blob = await prepareImageUploadBlob(file, options);
    expect(blob).toBe(output);
    expect(blob.type).toBe('image/png');
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(pngBytes);
    expect(drawImage).toHaveBeenCalledOnce();
    expect(createObjectURL).toHaveBeenCalledWith(file);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:test-image');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('advertises configured BMP inputs only when the converted output is accepted', () => {
    expect(imageUploadAccept(accept, policy)?.split(',')).toEqual(expect.arrayContaining([
      'image/bmp', 'image/x-bmp', 'image/x-ms-bmp', '.bmp', '.dib',
    ]));
    expect(imageUploadAccept('image/*', policy)).toContain('.bmp');
    expect(imageUploadAccept('.png', policy)).toContain('.bmp');
    expect(imageUploadAccept('image/jpeg', policy)).toBe('image/jpeg');
    expect(imageUploadAccept(accept, { ...policy, conversions: [] })).toBe(accept);
  });

  it.each([null, undefined])('accepts a MIME-only conversion with extensions %s', async (extensions) => {
    const mimePolicy = {
      ...policy,
      conversions: [{
        source_media_types: ['image/bmp'],
        source_extensions: extensions,
        target_media_type: 'image/png',
      }],
    };
    expect(imageUploadAccept(accept, mimePolicy)).toBe(`${accept},image/bmp`);
    const uri = await prepareImageUpload(new File(['BM'], 'reference', { type: 'image/bmp' }), {
      ...options,
      policy: mimePolicy,
    });
    expect(uri).toBe(`data:image/png;base64,${Buffer.from(pngBytes).toString('base64')}`);
  });

  it.each([null, undefined])('accepts an extension-only conversion with MIME types %s', async (mediaTypes) => {
    const extensionPolicy = {
      ...policy,
      conversions: [{
        source_media_types: mediaTypes,
        source_extensions: ['.bmp'],
        target_media_type: 'image/png',
      }],
    };
    expect(imageUploadAccept(accept, extensionPolicy)).toBe(`${accept},.bmp`);
    const uri = await prepareImageUpload(new File(['BM'], 'reference.BMP'), {
      ...options,
      policy: extensionPolicy,
    });
    expect(uri).toBe(`data:image/png;base64,${Buffer.from(pngBytes).toString('base64')}`);
  });

  it.each([
    ['reference.bmp', 'image/bmp'],
    ['reference.bmp', 'image/x-bmp'],
    ['reference.bmp', 'image/x-ms-bmp'],
    ['reference.BMP', ''],
    ['reference.dib', 'application/octet-stream'],
  ])('converts %s (%s) into encoded PNG bytes before returning the data URI', async (name, type) => {
    const file = new File(['BM-original-bitmap'], name, { type });
    const uri = await prepareImageUpload(file, options);
    expect(uri).toBe(`data:image/png;base64,${Buffer.from(pngBytes).toString('base64')}`);
    expect(drawImage).toHaveBeenCalledOnce();
    expect(createObjectURL).toHaveBeenCalledWith(file);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:test-image');
  });

  it.each(['image/png', 'image/jpeg', 'image/webp', 'image/gif'])(
    'preserves existing %s bytes without decoding or encoding them', async (type) => {
      const file = new File(['existing-bytes'], 'reference', { type });
      expect(await prepareImageUpload(file, options)).toBe(
        `data:${type};base64,${Buffer.from('existing-bytes').toString('base64')}`,
      );
      expect(createObjectURL).not.toHaveBeenCalled();
      expect(createCanvas).not.toHaveBeenCalled();
    },
  );

  it('rejects unsupported drop inputs before reading or decoding', async () => {
    await expect(prepareImageUpload(new File(['text'], 'file.txt', { type: 'text/plain' }), options))
      .rejects.toMatchObject({ code: 'unsupported' });
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it('does not allow conversion to bypass the caller acceptance restriction', async () => {
    await expect(prepareImageUpload(new File(['BM'], 'file.bmp', { type: 'image/bmp' }), {
      ...options,
      accept: 'image/jpeg',
    })).rejects.toMatchObject({ code: 'unsupported' });
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it('rejects oversized source files before decoding', async () => {
    await expect(prepareImageUpload(new File(['BM'.repeat(1024)], 'file.bmp'), options))
      .rejects.toMatchObject({ code: 'file_too_large' });
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it('rejects conversion output that exceeds the upload byte limit', async () => {
    output = new Blob(['converted'.repeat(1024)], { type: 'image/png' });
    await expect(prepareImageUpload(new File(['BM'], 'file.bmp'), options))
      .rejects.toMatchObject({ code: 'file_too_large' });
    expect(revokeObjectURL).toHaveBeenCalledOnce();
  });

  it('rejects images over the pixel budget before allocating a canvas', async () => {
    imageWidth = policy.maximum_pixels;
    imageHeight = 2;
    await expect(prepareImageUpload(new File(['BM'], 'file.bmp'), options))
      .rejects.toMatchObject({ code: 'too_many_pixels' });
    expect(createCanvas).not.toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledOnce();
  });

  it('reports corrupt BMP decoding and releases its object URL', async () => {
    decodeFailure = true;
    await expect(prepareImageUpload(new File(['bad-bitmap'], 'file.bmp'), options))
      .rejects.toMatchObject({ code: 'conversion_failed' });
    expect(createCanvas).not.toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledOnce();
  });

  it('reports failed encoding instead of returning the original BMP', async () => {
    output = null;
    await expect(prepareImageUpload(new File(['BM'], 'file.bmp'), options))
      .rejects.toMatchObject({ code: 'conversion_failed' });
    expect(revokeObjectURL).toHaveBeenCalledOnce();
  });

  it('rejects encoders that return a different media type', async () => {
    output = new Blob(['wrong-output'], { type: 'image/jpeg' });
    await expect(prepareImageUpload(new File(['BM'], 'file.bmp'), options))
      .rejects.toMatchObject({ code: 'conversion_failed' });
  });

  it('preserves read errors for the schema message renderer', async () => {
    readFailure = true;
    await expect(prepareImageUpload(new File(['png'], 'file.png', { type: 'image/png' }), options))
      .rejects.toMatchObject({ code: 'read_failed' });
  });
});

// #335: the file names behind data addresses used to be keyed by the whole address, so up to 32 pictures of
// up to ~70 MB each stayed reachable after their form or chat message had let them go.
describe('upload file names (#187 B, #335)', () => {
  const large = { accept, maximumBytes: 8 * 1024 * 1024, policy };
  const picture = (round: number) => {
    const bytes = new Uint8Array(2 * 1024 * 1024);
    bytes.fill(round & 0xff);
    bytes[0] = round >> 8;
    return new File([bytes], `round-${round}-${'n'.repeat(300)}.png`, { type: 'image/png' });
  };

  it('holds a bounded number of bytes however many large pictures are prepared', async () => {
    let last = '';
    let first = '';
    for (let round = 0; round < 40; round += 1) {
      const uri = await prepareImageUpload(picture(round), { ...large, rememberName: true });
      if (round === 0) first = uri;
      last = uri;
      expect(imageUploadNameCacheBytes()).toBeLessThanOrEqual(IMAGE_UPLOAD_NAME_CACHE_MAX_BYTES);
    }
    // The cap is far below one picture: the addresses themselves are never kept.
    expect(IMAGE_UPLOAD_NAME_CACHE_MAX_BYTES).toBeLessThan(64 * 1024);
    expect(imageUploadNameCacheBytes()).toBeLessThan(last.length / 100);
    expect(imageUploadName(last)).toMatch(/^round-39-n+$/);
    expect(imageUploadName(last)!.length).toBeLessThanOrEqual(255);
    // The oldest names make room for newer ones.
    expect(imageUploadName(first)).toBeUndefined();
  });

  it('tells pictures apart by their bytes, not by their size or opening bytes', async () => {
    const a = new Uint8Array(4096);
    const b = new Uint8Array(4096);
    b[2048] = 1;
    const first = await prepareImageUpload(new File([a], 'first.png', { type: 'image/png' }), { ...large, rememberName: true });
    const second = await prepareImageUpload(new File([b], 'second.png', { type: 'image/png' }), { ...large, rememberName: true });
    expect(imageUploadName(first)).toBe('first.png');
    expect(imageUploadName(second)).toBe('second.png');
  });

  it('remembers nothing for a picture whose caller does not ask for its name (chat attachments)', async () => {
    const before = imageUploadNameCacheBytes();
    const uri = await prepareImageUpload(new File([Uint8Array.from([9, 9, 9])], 'chat.png', { type: 'image/png' }), large);
    expect(imageUploadName(uri)).toBeUndefined();
    expect(imageUploadNameCacheBytes()).toBe(before);
  });
});
