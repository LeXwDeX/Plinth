/**
 * X8: a finished inpaint result is also put back on the canvas as a new layer at the dashed frame, with
 * everything outside the selection transparent (the mask baked into the alpha channel, no layer mask).
 * The job (which task, which mask, which frame) is remembered for the browser session so a result that
 * arrives after a reload still lands.
 */
import { isMaskAssetId } from './inpaint-source';

export interface InpaintFrame {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface InpaintJob {
  taskId: string;
  sessionId: string;
  /** The uploaded mask picture (opaque = kept, clear = redrawn). */
  maskUrl: string;
  frame: InpaintFrame;
  /** Pixel size of the mask and of the delivered picture. */
  size: { width: number; height: number };
  /** Platform mask asset from the submission; absent on jobs remembered before it was recorded. */
  maskAssetId?: string;
}

const STORAGE_PREFIX = 'art-ai:inpaint-job:';

function key(taskId: string): string {
  return `${STORAGE_PREFIX}${taskId}`;
}

// A job is needed for this page even when the browser refuses session storage (quota, private mode).
// The map is the page's own copy; storage only carries it across a reload. A removal tombstone keeps a
// failed removeItem from bringing the job back in this page.
const memoryJobs = new Map<string, InpaintJob>();
const removedJobs = new Set<string>();

function validJob(value: unknown): value is InpaintJob {
  if (!value || typeof value !== 'object') return false;
  const job = value as Partial<InpaintJob>;
  const finite = (number: unknown) => typeof number === 'number' && Number.isFinite(number);
  return typeof job.taskId === 'string' && typeof job.sessionId === 'string' && typeof job.maskUrl === 'string'
    && job.maskUrl !== '' && Boolean(job.frame) && finite(job.frame?.x) && finite(job.frame?.y)
    && finite(job.frame?.width) && (job.frame?.width ?? 0) > 0 && finite(job.frame?.height) && (job.frame?.height ?? 0) > 0
    && Boolean(job.size) && (job.size?.width ?? 0) > 0 && (job.size?.height ?? 0) > 0
    && (job.maskAssetId === undefined || isMaskAssetId(job.maskAssetId));
}

/** Returns false when session storage refused the record; this page still remembers it. */
export function saveInpaintJob(job: InpaintJob): boolean {
  if (!validJob(job)) return false;
  memoryJobs.set(job.taskId, job);
  removedJobs.delete(job.taskId);
  try {
    sessionStorage.setItem(key(job.taskId), JSON.stringify(job));
    return true;
  } catch {
    // Private browsing or a full quota: the job lives only until the page reloads.
    return false;
  }
}

export function readInpaintJob(taskId: string): InpaintJob | null {
  if (removedJobs.has(taskId)) return null;
  const remembered = memoryJobs.get(taskId);
  if (remembered) return remembered;
  try {
    const raw = sessionStorage.getItem(key(taskId));
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (!validJob(parsed) || parsed.taskId !== taskId) return null;
    memoryJobs.set(taskId, parsed);
    return parsed;
  } catch {
    return null;
  }
}

export function removeInpaintJob(taskId: string): void {
  memoryJobs.delete(taskId);
  removedJobs.add(taskId);
  try { sessionStorage.removeItem(key(taskId)); } catch { /* The tombstone still hides it on this page. */ }
}

/** Every remembered job of a session, merging this page's copy with stored records once each. */
export function readInpaintJobs(sessionId: string): InpaintJob[] {
  const jobs = new Map<string, InpaintJob>();
  for (const job of memoryJobs.values()) {
    if (job.sessionId === sessionId && !removedJobs.has(job.taskId)) jobs.set(job.taskId, job);
  }
  try {
    for (let index = 0; index < sessionStorage.length; index += 1) {
      const name = sessionStorage.key(index);
      if (!name?.startsWith(STORAGE_PREFIX)) continue;
      const job = readInpaintJob(name.slice(STORAGE_PREFIX.length));
      if (job && job.sessionId === sessionId) jobs.set(job.taskId, job);
    }
  } catch {
    // The in-memory copy above still answers when the storage scan itself fails.
  }
  return [...jobs.values()];
}

type LayerContext = Pick<CanvasRenderingContext2D, 'drawImage' | 'clearRect'> & { globalCompositeOperation: string };

/** The delivered pixels do not match the mask and canvas: the result cannot be put back without stretching it. */
export class InpaintSizeMismatchError extends Error {
  constructor(
    readonly expected: { width: number; height: number },
    readonly actual: { width: number; height: number },
    readonly source: 'result' | 'mask',
  ) {
    super(`inpaint ${source} is ${actual.width}x${actual.height}, expected ${expected.width}x${expected.height}`);
    this.name = 'InpaintSizeMismatchError';
  }
}

function sourceSize(source: CanvasImageSource): { width: number; height: number } | null {
  const candidate = source as unknown as {
    naturalWidth?: unknown; naturalHeight?: unknown;
    videoWidth?: unknown; videoHeight?: unknown;
    width?: unknown; height?: unknown;
  };
  const width = Number(candidate.naturalWidth ?? candidate.videoWidth ?? candidate.width);
  const height = Number(candidate.naturalHeight ?? candidate.videoHeight ?? candidate.height);
  return Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0 ? { width, height } : null;
}

function requireSize(source: CanvasImageSource, expected: { width: number; height: number }, kind: 'result' | 'mask'): void {
  const actual = sourceSize(source) ?? { width: 0, height: 0 };
  if (actual.width !== expected.width || actual.height !== expected.height) {
    throw new InpaintSizeMismatchError(expected, actual, kind);
  }
}

/**
 * Draws the layer: the result at the mask's size, then the mask knocked out of it with destination-out,
 * so only the redrawn (clear) part of the mask keeps any pixels. Both pictures must already be at
 * `size`: drawing a differently sized result here would stretch it and fake a successful patch.
 */
export function drawInpaintLayer(
  context: LayerContext,
  result: CanvasImageSource,
  mask: CanvasImageSource,
  size: { width: number; height: number },
): void {
  requireSize(result, size, 'result');
  requireSize(mask, size, 'mask');
  context.clearRect(0, 0, size.width, size.height);
  context.globalCompositeOperation = 'source-over';
  context.drawImage(result, 0, 0, size.width, size.height);
  context.globalCompositeOperation = 'destination-out';
  context.drawImage(mask, 0, 0, size.width, size.height);
  context.globalCompositeOperation = 'source-over';
}

export function renderInpaintLayer(
  result: CanvasImageSource,
  mask: CanvasImageSource,
  size: { width: number; height: number },
): Promise<Blob> {
  const canvas = document.createElement('canvas');
  canvas.width = size.width;
  canvas.height = size.height;
  const context = canvas.getContext('2d');
  if (!context) return Promise.reject(new Error('canvas unavailable'));
  drawInpaintLayer(context, result, mask, size);
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error('inpaint layer encoding failed'))), 'image/png');
  });
}
