import { readFileSync } from 'node:fs';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Mesh, MeshStandardMaterial, NearestFilter, ClampToEdgeWrapping, Texture } from 'three';
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';
import { CachingGLTFLoader, $loader } from '@google/model-viewer/lib/three-components/CachingGLTFLoader.js';
import { GLTFInstance } from '@google/model-viewer/lib/three-components/GLTFInstance.js';

import {
  MODEL_LOAD_FAILURE_TEXT,
  classifyModelLoadError,
  modelLoadFailureOf,
  modelLoadFailureText,
  recordModelLoadErrors,
  resetModelLoadErrorsForTest,
  takeModelLoadError,
  type UrlLoader,
} from './model-load-failure';

const catalog = JSON.parse(readFileSync(new URL('../../../internal/catalog/default_catalog.json', import.meta.url), 'utf8'));
const messages: Record<string, string> = catalog.application.content.messages;
const text = (key: string) => messages[key] ?? key;

/** three.js FileLoader's HttpError: an Error carrying the failed Response. */
function httpError(status: number, requestId?: string) {
  const error = new Error(`fetch for "https://example.invalid/api/v1/assets/a/content?token=secret" responded with ${status}: Service Unavailable`);
  return Object.assign(error, { response: { status, headers: new Headers(requestId ? { 'X-Request-ID': requestId } : {}) } });
}

/** The script error model-viewer 4.2 puts in its "error" event after it swallowed the real one. */
const secondaryError = new TypeError("Cannot read properties of undefined (reading 'scene')");
const loadFailureEvent = new CustomEvent('error', { detail: { type: 'loadfailure', sourceError: secondaryError } });

afterEach(() => { resetModelLoadErrorsForTest(); vi.restoreAllMocks(); });

describe('classifyModelLoadError', () => {
  it('names a lost connection in Chromium, WebKit and Firefox words', () => {
    for (const message of ['Failed to fetch', 'Load failed', 'The network connection was lost.', 'NetworkError when attempting to fetch resource.', 'network error']) {
      expect(classifyModelLoadError(new TypeError(message)).kind).toBe('network');
    }
    expect(classifyModelLoadError(new Error('THREE.GLTFLoader: Failed to load buffer "model.bin".')).kind).toBe('network');
  });

  it('treats any error from a browser that knows it is offline as a network failure', () => {
    expect(classifyModelLoadError(new SyntaxError('Unexpected end of JSON input'), false).kind).toBe('network');
  });

  it('keeps the status and request ID of an HTTP failure, but no URL', () => {
    const failure = classifyModelLoadError(httpError(503, 'req_1234'));
    expect(failure).toMatchObject({ kind: 'http', status: 503, requestId: 'req_1234' });
    expect(failure.detail).not.toMatch(/example\.invalid|token|secret/);
    expect(failure.detail).toContain('503');
  });

  it('calls bytes that arrived but cannot be read a parse failure', () => {
    expect(classifyModelLoadError(new SyntaxError('Unexpected token \'h\', "this is not"... is not valid JSON')).kind).toBe('parse');
    expect(classifyModelLoadError(new Error('THREE.GLTFLoader: Unsupported asset. glTF versions >=2.0 are supported.')).kind).toBe('parse');
    expect(classifyModelLoadError('THREE.GLTFLoader: Unsupported asset.').kind).toBe('parse');
  });

  it('names an aborted download and an empty error', () => {
    expect(classifyModelLoadError(new DOMException('The user aborted a request.', 'AbortError')).kind).toBe('aborted');
    expect(classifyModelLoadError(undefined).kind).toBe('unknown');
    expect(classifyModelLoadError(null).kind).toBe('unknown');
  });

  it('removes URLs from the logged detail', () => {
    expect(classifyModelLoadError(new TypeError('Failed to fetch https://example.invalid/m.glb?sig=abc')).detail).toBe('TypeError: Failed to fetch <url>');
  });
});

describe('modelLoadFailureOf', () => {
  it('uses the recorded loader error, never the secondary script error', () => {
    const failure = modelLoadFailureOf(loadFailureEvent, { found: true, error: new TypeError('Failed to fetch') });
    expect(failure.kind).toBe('network');
    expect(failure.detail).not.toContain('scene');
  });

  it('falls back to the general sentence when nothing was recorded, or to network when offline', () => {
    expect(modelLoadFailureOf(loadFailureEvent, { found: false }).kind).toBe('unknown');
    expect(modelLoadFailureOf(loadFailureEvent, { found: false }, false).kind).toBe('network');
  });

  it('does not blame the model file for a lost WebGL context', () => {
    const contextLost = new CustomEvent('error', { detail: { type: 'webglcontextlost', sourceError: new Event('webglcontextlost') } });
    expect(modelLoadFailureOf(contextLost, { found: true, error: new SyntaxError('bad') }).kind).toBe('unknown');
  });
});

describe('modelLoadFailureText', () => {
  it('has a Schema sentence for every kind, and none of them is a script error', () => {
    for (const key of Object.values(MODEL_LOAD_FAILURE_TEXT)) {
      expect(messages[key], key).toBeTruthy();
      expect(messages[key]).not.toMatch(/undefined|Cannot read|TypeError|\{reason\}/);
    }
  });

  it('shows the status of an HTTP failure and the plain sentence otherwise', () => {
    expect(modelLoadFailureText({ kind: 'http', status: 404, detail: '' }, text)).toBe(messages['ui.viewer.load_failed_http'].replace('{status}', '404'));
    expect(modelLoadFailureText({ kind: 'network', detail: '' }, text)).toBe(messages['ui.viewer.load_failed_network']);
    expect(modelLoadFailureText({ kind: 'parse', detail: '' }, text)).toBe(messages['ui.viewer.load_failed_parse']);
    expect(modelLoadFailureText({ kind: 'aborted', detail: '' }, text)).toBe(messages['ui.viewer.load_failed_aborted']);
    expect(modelLoadFailureText({ kind: 'unknown', detail: '' }, text)).toBe(messages['ui.viewer.load_failed_unknown']);
  });
});

describe('recordModelLoadErrors', () => {
  /** A loader whose next load fails with `error`, or succeeds when it is undefined. */
  function fakeLoader() {
    const calls: { url: string; onLoad: (v: unknown) => void; onError?: (e: unknown) => void }[] = [];
    const loader: UrlLoader = {
      register: vi.fn(),
      load(url, onLoad, _onProgress, onError) { calls.push({ url, onLoad, onError }); return undefined; },
    };
    return { loader, calls };
  }

  it('keeps the original error under its URL, then calls the loader error handler and onFailure in that order', () => {
    const { loader, calls } = fakeLoader();
    const order: string[] = [];
    expect(recordModelLoadErrors(loader, (url) => order.push(`failure ${url}`))).toBe(true);
    loader.load('/a.glb', () => undefined, undefined, () => order.push('loader onError'));
    const original = new TypeError('Failed to fetch');
    calls[0].onError?.(original);
    expect(order).toEqual(['loader onError', 'failure /a.glb']);
    expect(takeModelLoadError('/a.glb')).toEqual({ found: true, error: original });
    // Taken once: a later failure without a new record is not blamed on the old error.
    expect(takeModelLoadError('/a.glb')).toEqual({ found: false });
  });

  it('still calls onFailure when the loader error handler throws', () => {
    const { loader, calls } = fakeLoader();
    const onFailure = vi.fn();
    recordModelLoadErrors(loader, onFailure);
    loader.load('/b.glb', () => undefined, undefined, () => { throw new Error('handler broke'); });
    expect(() => calls[0].onError?.(new Error('x'))).toThrow('handler broke');
    expect(onFailure).toHaveBeenCalledWith('/b.glb');
  });

  it('passes through this, progress and the return value', () => {
    const progress = vi.fn();
    const original = vi.fn(function (this: unknown, _url, _onLoad, onProgress) {
      expect(this).toBe(loader);
      onProgress({ loaded: 3, total: 4 });
      return 'request';
    });
    const loader = { load: original, register: vi.fn() };
    recordModelLoadErrors(loader);
    expect(loader.load('/progress.glb', vi.fn(), progress)).toBe('request');
    expect(progress).toHaveBeenCalledWith({ loaded: 3, total: 4 });
  });

  it('forgets an old failure once the same URL loads', () => {
    const { loader, calls } = fakeLoader();
    recordModelLoadErrors(loader);
    loader.load('/c.glb', () => undefined, undefined, () => undefined);
    calls[0].onError?.(new TypeError('Failed to fetch'));
    const onLoad = vi.fn();
    loader.load('/c.glb', onLoad, undefined, () => undefined);
    calls[1].onLoad('gltf');
    expect(onLoad).toHaveBeenCalledWith('gltf');
    expect(takeModelLoadError('/c.glb').found).toBe(false);
  });

  it('wraps a loader only once and refuses something that is not a loader', () => {
    const { loader, calls } = fakeLoader();
    const onFailure = vi.fn();
    recordModelLoadErrors(loader, onFailure);
    recordModelLoadErrors(loader, onFailure);
    loader.load('/d.glb', () => undefined, undefined, () => undefined);
    calls[0].onError?.(new Error('x'));
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(recordModelLoadErrors(undefined)).toBe(false);
    expect(recordModelLoadErrors({})).toBe(false);
    expect(recordModelLoadErrors({ load: vi.fn() })).toBe(false);
  });

  it('keeps only the most recent failures, so abandoned loads do not pile up', () => {
    const { loader, calls } = fakeLoader();
    recordModelLoadErrors(loader);
    for (let index = 0; index < 40; index += 1) {
      loader.load(`/m${index}.glb`, () => undefined, undefined, () => undefined);
      calls[index].onError?.(new Error(String(index)));
    }
    expect(takeModelLoadError('/m0.glb').found).toBe(false);
    expect(takeModelLoadError('/m39.glb').found).toBe(true);
  });
});

describe('strict requested textures (real three 0.180 parser)', () => {
  const model = (textured = true) => JSON.stringify({
    asset: { version: '2.0' }, scenes: [{ nodes: [0] }], nodes: [{ mesh: 0 }],
    meshes: [{ primitives: [{ attributes: {}, material: 0 }] }],
    materials: [textured ? { pbrMetallicRoughness: { baseColorTexture: { index: 0 } } } : {}],
    textures: [{ source: 0, sampler: 0 }, { source: 1 }],
    images: [{ uri: 'requested.png' }, { uri: 'unused-broken.png' }],
    samplers: [{ magFilter: 9728, minFilter: 9728, wrapS: 33071, wrapT: 33071 }],
  });

  it('rejects the whole parse when a requested image rejection becomes null', async () => {
    const loader = new GLTFLoader();
    const image = vi.fn().mockRejectedValue(new Error('private image contents'));
    loader.register(parser => { parser.loadImageSource = image; return { name: 'test-image' }; });
    recordModelLoadErrors(loader);
    await expect(loader.parseAsync(model(), '')).rejects.toThrow('Model texture could not be decoded');
    expect(image).toHaveBeenCalledTimes(1);
  });

  it('preserves an original texture rejection instead of replacing it', async () => {
    const original = new Error('original failure');
    const loader = new GLTFLoader().register(parser => {
      parser.loadTextureImage = () => Promise.reject(original);
      return { name: 'test-rejection' };
    });
    recordModelLoadErrors(loader);
    await expect(loader.parseAsync(model(), '')).rejects.toBe(original);
  });

  it('fails if a second requested texture is broken even when the first succeeds', async () => {
    const fixture = JSON.parse(model());
    fixture.materials[0].normalTexture = { index: 1 };
    const image = vi.fn().mockImplementation((index: number) => index === 0 ? Promise.resolve(new Texture()) : Promise.reject(new Error('broken normal map')));
    const loader = new GLTFLoader().register(parser => { parser.loadImageSource = image; return { name: 'test-image' }; });
    recordModelLoadErrors(loader);
    await expect(loader.parseAsync(JSON.stringify(fixture), '')).rejects.toThrow('Model texture could not be decoded');
    expect(image).toHaveBeenCalledTimes(2);
  });

  it('keeps texture identity, this, sampler handling and unused images untouched', async () => {
    const texture = new Texture();
    const loader = new GLTFLoader();
    const image = vi.fn().mockResolvedValue(texture);
    loader.register(parser => { parser.loadImageSource = image; return { name: 'test-image' }; });
    const register = vi.spyOn(loader, 'register');
    recordModelLoadErrors(loader);
    const load = loader.load;
    recordModelLoadErrors(loader);
    expect(loader.load).toBe(load);
    expect(register).toHaveBeenCalledTimes(1);
    const result = await loader.parseAsync(model(), '');
    const material = (result.scene.children[0] as Mesh).material as MeshStandardMaterial;
    expect(material.map).toBe(texture);
    expect(texture).toMatchObject({ flipY: false, magFilter: NearestFilter, minFilter: NearestFilter, wrapS: ClampToEdgeWrapping, wrapT: ClampToEdgeWrapping, generateMipmaps: false });
    expect(image).toHaveBeenCalledTimes(1);
    expect(image.mock.contexts[0]).toBe(result.parser);
  });

  it('accepts an untextured model without loading unused broken definitions', async () => {
    const image = vi.fn().mockRejectedValue(new Error('unused'));
    const loader = new GLTFLoader().register(parser => { parser.loadImageSource = image; return { name: 'test-image' }; });
    recordModelLoadErrors(loader);
    await expect(loader.parseAsync(model(false), '')).resolves.toHaveProperty('scene');
    expect(image).not.toHaveBeenCalled();
    await expect(loader.parseAsync('{"asset":{"version":"2.0"},"scenes":[{}]}', '')).resolves.toHaveProperty('scene');
  });

  it.each(['KHR_texture_basisu', 'EXT_texture_webp', 'EXT_texture_avif'])('rejects a requested %s texture through the same seam', async extension => {
    const fixture = JSON.parse(model());
    fixture.extensionsUsed = [extension];
    fixture.textures[0].extensions = { [extension]: { source: 0 } };
    const loader = new GLTFLoader().register(parser => {
      parser.loadImageSource = vi.fn().mockRejectedValue(new Error('decode failed'));
      return { name: 'test-image' };
    });
    // Select Basis' actual extension path without invoking its GPU transcoder in Node.
    if (extension === 'KHR_texture_basisu') loader.setKTX2Loader({} as Parameters<GLTFLoader['setKTX2Loader']>[0]);
    recordModelLoadErrors(loader);
    await expect(loader.parseAsync(JSON.stringify(fixture), '')).rejects.toThrow('Model texture could not be decoded');
  });

  it('evicts a failed shared cache entry so concurrent viewers fail and a same-asset retry parses again', async () => {
    const caching = new CachingGLTFLoader(GLTFInstance);
    const loader = caching[$loader];
    const image = vi.fn().mockRejectedValueOnce(new Error('decode failed')).mockResolvedValue(new Texture());
    loader.register(parser => { parser.loadImageSource = image; return { name: 'test-image' }; });
    // Replace transport only: real parser, recorder, cache preparation and cloning still run.
    const transport = vi.spyOn(loader, 'load').mockImplementation(function (this: GLTFLoader, _url, onLoad, _progress, onError) {
      this.parse(model(), '', onLoad, onError);
    });
    const evictions: Promise<unknown>[] = [];
    recordModelLoadErrors(loader, url => {
      evictions.push(new Promise(resolve => setTimeout(resolve, 0)).then(() => CachingGLTFLoader.delete(url)).catch(() => undefined));
    });
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const element = { withCredentials: false } as Parameters<typeof caching.load>[1];
    const url = '/shared-texture.glb';
    try {
      const results = await Promise.allSettled([caching.load(url, element), caching.load(url, element)]);
      expect(results.map(result => result.status)).toEqual(['rejected', 'rejected']);
      expect(transport).toHaveBeenCalledTimes(1);
      expect(classifyModelLoadError(takeModelLoadError(url).error).kind).toBe('parse');
      await Promise.all(evictions);
      expect(CachingGLTFLoader.has(url)).toBe(false);
      const retried = await caching.load(url, element);
      expect(retried.scene).toBeTruthy();
      expect(transport).toHaveBeenCalledTimes(2);
      expect(image).toHaveBeenCalledTimes(2);
      expect(takeModelLoadError(url).found).toBe(false);
      retried.dispose();
    } finally {
      await Promise.all(evictions);
      await CachingGLTFLoader.delete(url).catch(() => undefined);
    }
  });
});

describe('model-viewer internals the viewer relies on', () => {
  // The recorder and the cache eviction reach into model-viewer 4.2's glTF cache. If an upgrade moves these,
  // setup must fail closed rather than silently lose texture validation or cache eviction.
  it('still exposes the shared loader and the cache delete', () => {
    const lib = (file: string) => readFileSync(new URL(`../../../node_modules/@google/model-viewer/lib/three-components/${file}`, import.meta.url), 'utf8');
    const caching = lib('CachingGLTFLoader.js');
    expect(caching).toContain("export const $loader = Symbol('loader');");
    expect(caching).toMatch(/static async delete\(url\)/);
    expect(caching).toMatch(/\.catch\(\(?reason/);
    expect(lib('Renderer.js')).toMatch(/this\.loader = new CachingGLTFLoader\(/);
    const three = readFileSync(new URL('../../../node_modules/three/examples/jsm/loaders/GLTFLoader.js', import.meta.url), 'utf8');
    expect(three).toContain('loadTextureImage( textureIndex, sourceIndex, loader )');
    expect(three.match(/return parser\.loadTextureImage\( textureIndex, extension\.source, loader \)/g)).toHaveLength(3);
    expect(three).toContain('return this.loadTextureImage( textureIndex, sourceIndex, loader )');
  });
});
