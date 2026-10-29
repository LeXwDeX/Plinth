import { formatSchemaText } from '@/lib/product-schema';
import type { GLTFParser } from 'three/examples/jsm/loaders/GLTFLoader.js';

/**
 * Why the 3D viewer could not show a model (#264).
 *
 * model-viewer 4.2 does not pass the real load error on: its glTF cache catches it, logs it, caches an empty
 * result, and the "error" event then carries a second, unrelated script error (Chromium "Cannot read properties
 * of undefined (reading 'scene')", WebKit "undefined is not an object…"). The viewer therefore records the
 * original error at the glTF loader (`recordModelLoadErrors`), sorts it into one of these kinds, and shows the
 * Schema sentence for the kind instead of any raw message.
 */
export type ModelLoadFailureKind = 'network' | 'http' | 'parse' | 'aborted' | 'unknown';

export interface ModelLoadFailure {
  kind: ModelLoadFailureKind;
  /** HTTP status, for `http`. */
  status?: number;
  /** The server's X-Request-ID of a failed response, when it sent one. */
  requestId?: string;
  /** A short description for the server log: error name and message with URLs taken out. Never shown. */
  detail: string;
}

/** The Schema message (`content.messages`) shown for each kind. */
export const MODEL_LOAD_FAILURE_TEXT: Record<ModelLoadFailureKind, string> = {
  network: 'ui.viewer.load_failed_network',
  http: 'ui.viewer.load_failed_http',
  parse: 'ui.viewer.load_failed_parse',
  aborted: 'ui.viewer.load_failed_aborted',
  unknown: 'ui.viewer.load_failed_unknown',
};

/**
 * Messages of a request that never got an answer: fetch's TypeError in Chromium ("Failed to fetch"),
 * WebKit ("Load failed", "The network connection was lost.") and Firefox ("NetworkError when attempting…"),
 * a body cut off mid-download ("network error"), and the glTF loader's own word for a lost external buffer.
 */
const NETWORK_MESSAGE = /failed to fetch|load failed|networkerror|network error|network connection|internet connection|failed to load buffer/i;
const DETAIL_LIMIT = 300;

function errorName(error: unknown): string {
  return error !== null && typeof error === 'object' && typeof (error as { name?: unknown }).name === 'string' ? (error as { name: string }).name : '';
}

function errorMessage(error: unknown): string {
  if (typeof error === 'string') return error;
  if (error !== null && typeof error === 'object' && typeof (error as { message?: unknown }).message === 'string') return (error as { message: string }).message;
  return '';
}

/** Name and message for the log, with quoted text and URLs removed (asset URLs may carry tokens). */
function describe(error: unknown): string {
  const message = errorMessage(error)
    .replace(/"[^"]*"/g, '"…"')
    .replace(/\b[a-z][a-z0-9+.-]*:\/\/\S+/gi, '<url>')
    .trim();
  return [errorName(error), message].filter(Boolean).join(': ').slice(0, DETAIL_LIMIT);
}

function responseOf(error: unknown): { status: number; requestId?: string } | null {
  if (error === null || typeof error !== 'object') return null;
  const response = (error as { response?: { status?: unknown; headers?: { get?: (name: string) => string | null } } }).response;
  if (!response || typeof response.status !== 'number') return null;
  let requestId: string | undefined;
  try { requestId = response.headers?.get?.('X-Request-ID') || undefined; } catch { requestId = undefined; }
  return { status: response.status, requestId };
}

/**
 * Sorts the glTF loader's original error. `online` is `navigator.onLine`: a browser that knows it is offline
 * is a network failure whatever the error says. Anything that got bytes back but could not use them is `parse`.
 */
export function classifyModelLoadError(error: unknown, online = true): ModelLoadFailure {
  const detail = describe(error);
  if (error === undefined || error === null) return { kind: 'unknown', detail };
  if (errorName(error) === 'AbortError') return { kind: 'aborted', detail };
  const response = responseOf(error);
  if (response) return { kind: 'http', status: response.status, requestId: response.requestId, detail };
  if (!online || NETWORK_MESSAGE.test(errorMessage(error))) return { kind: 'network', detail };
  return { kind: 'parse', detail };
}

/** A load error the recorder kept for a URL; `found` is false when the loader never reported one. */
export interface RecordedLoadError {
  found: boolean;
  error?: unknown;
}

/**
 * The failure behind a model-viewer "error" event. The recorded loader error decides it; without one (because
 * another viewer already consumed it, or the event is not a load failure, such as a lost WebGL context) the event's
 * own error is not trusted, and only an offline browser is named.
 */
export function modelLoadFailureOf(event: Event, recorded: RecordedLoadError, online = true): ModelLoadFailure {
  const detail = (event as CustomEvent<{ type?: unknown; sourceError?: unknown }>).detail;
  if (recorded.found && detail?.type !== 'webglcontextlost') return classifyModelLoadError(recorded.error, online);
  const fallback = describe(detail?.sourceError);
  return online ? { kind: 'unknown', detail: fallback } : { kind: 'network', detail: fallback };
}

/** The user-facing sentence for a failure, from the Schema. */
export function modelLoadFailureText(failure: ModelLoadFailure, text: (key: string) => string): string {
  const template = text(MODEL_LOAD_FAILURE_TEXT[failure.kind]);
  return failure.kind === 'http' ? formatSchemaText(template, { status: failure.status ?? '' }) : template;
}

/** The part of three.js's GLTFLoader the recorder wraps. */
export interface UrlLoader {
  register(callback: (parser: GLTFParser) => { name: string }): unknown;
  load(url: string, onLoad: (result: unknown) => void, onProgress?: (event: unknown) => void, onError?: (error: unknown) => void): unknown;
}

const RECORDING = Symbol('modelLoadErrorRecording');
const RECORD_LIMIT = 16;
const recordedErrors = new Map<string, unknown>();

/** Where the plugin leaves the texture indices that could not be decoded (in the glTF JSON's root `extras`). */
const FAILED_TEXTURES_KEY = 'plinthFailedTextures';

/**
 * How many textures of a loaded model could not be decoded or fetched (#127). `json` is the glTF JSON the
 * viewer exposes as `originalGltfJson`; the number is 0 for a model whose textures all loaded.
 */
export function incompleteTextureCountOf(json: unknown): number {
  const extras = json !== null && typeof json === 'object' ? (json as { extras?: unknown }).extras : undefined;
  const failed = extras !== null && typeof extras === 'object' ? (extras as Record<string, unknown>)[FAILED_TEXTURES_KEY] : undefined;
  return Array.isArray(failed) ? new Set(failed.filter((index) => typeof index === 'number')).size : 0;
}

function trackTexturePlugin(parser: GLTFParser): { name: string } {
  const original = parser.loadTextureImage;
  if (typeof original !== 'function') throw new Error('Model texture validation unavailable');
  // Three 0.180 swallows image failures as null, which leaves the material slot empty (the default material).
  // The geometry still shows; the indices of the textures that failed are kept on the glTF JSON so the viewer
  // can tell the person the textures are incomplete. A rejection of the texture step itself still fails the load.
  parser.loadTextureImage = function (...args) {
    return original.apply(this, args).then(texture => {
      if (texture === null) {
        const json = this.json as { extras?: Record<string, unknown> };
        const extras = json.extras ?? (json.extras = {});
        const failed = Array.isArray(extras[FAILED_TEXTURES_KEY]) ? (extras[FAILED_TEXTURES_KEY] as unknown[]) : (extras[FAILED_TEXTURES_KEY] = []);
        if (!failed.includes(args[0])) failed.push(args[0]);
      }
      return texture;
    });
  };
  return { name: 'PLINTH_track_textures' };
}

function remember(url: string, error: unknown): void {
  recordedErrors.delete(url);
  recordedErrors.set(url, error);
  // Loads nobody asks about (a viewer that switched away or closed) must not pile up.
  while (recordedErrors.size > RECORD_LIMIT) recordedErrors.delete(recordedErrors.keys().next().value as string);
}

/**
 * Wraps `loader.load` once so every failed load leaves its original error under its URL, then calls
 * `onFailure(url)` (after the loader's own error handling has run). Returns false when the loader does not
 * support both loading and texture tracking registration. The viewer must not mount in that case.
 */
export function recordModelLoadErrors(loader: unknown, onFailure?: (url: string) => void): boolean {
  if (loader === null || typeof loader !== 'object' || typeof (loader as UrlLoader).load !== 'function' || typeof (loader as UrlLoader).register !== 'function') return false;
  const target = loader as UrlLoader & { [RECORDING]?: true };
  if (target[RECORDING]) return true;
  target.register(trackTexturePlugin);
  const original = target.load;
  target.load = function load(this: unknown, url, onLoad, onProgress, onError) {
    return original.call(this, url, (result: unknown) => {
      recordedErrors.delete(url);
      onLoad(result);
    }, onProgress, (error: unknown) => {
      remember(url, error);
      try { onError?.(error); } finally { onFailure?.(url); }
    });
  };
  target[RECORDING] = true;
  return true;
}

/** Hands over (and forgets) the error recorded for this URL. */
export function takeModelLoadError(url: string): RecordedLoadError {
  if (!recordedErrors.has(url)) return { found: false };
  const error = recordedErrors.get(url);
  recordedErrors.delete(url);
  return { found: true, error };
}

/** Test only: forget every recorded error. */
export function resetModelLoadErrorsForTest(): void {
  recordedErrors.clear();
}
