/**
 * The 2D board's picture cache (#287, #290): one decoded picture per address the board shows.
 *
 * It holds exactly the pictures the current layers name. A picture no layer names any more (a deleted layer, a
 * replaced picture, a brush step that was uploaded over) is let go at the next sync: a download still running is
 * stopped and its result ignored. A picture that fails to load is dropped, so the next sync asks again. A hidden
 * layer's picture is not fetched until the layer is shown; one already loaded is kept while it is hidden, so
 * toggling visibility does not download and decode it again.
 *
 * #290: pictures are decoded into an `ImageBitmap` off the main thread (`createImageBitmap` on the downloaded
 * bytes). An `<img>` element drawn on a canvas is decoded on the main thread at draw time, and Chromium drops
 * that decode again once its image cache is full: ten 4K layers then cost about half a second of frozen page on
 * every redraw that follows (after a send, a session switch or a while idle). A bitmap stays decoded until it is
 * collected, so redraws only paint.
 *
 * #291: a zoomed-out board draws each layer from a reduced copy (`display`), decoded at the smaller size from the
 * downloaded bytes with `createImageBitmap` resizing. Drawn from the original, ten 4K layers made the redraw after every send take
 * about 60 ms while the GPU took the whole pictures. The original stays held for zooming in, painting, picking
 * colours, cropping and export.
 */

/** A picture the board can draw: a decoded bitmap, or an element where `createImageBitmap` is missing. */
export type CanvasPicture = HTMLImageElement | ImageBitmap;

/** Pixel size of a picture that is ready to draw; null while an element is still loading or broken. */
export function pictureSize(picture: CanvasPicture | null | undefined): { width: number; height: number } | null {
  if (!picture) return null;
  if ('naturalWidth' in picture) {
    return picture.complete && picture.naturalWidth > 0 ? { width: picture.naturalWidth, height: picture.naturalHeight } : null;
  }
  return picture.width > 0 && picture.height > 0 ? { width: picture.width, height: picture.height } : null;
}

export interface PictureSource {
  get(url: string): CanvasPicture | undefined;
  set(url: string, picture: CanvasPicture): void;
}

/** Fetches and decodes one picture; rejects when it cannot, or when `signal` aborts. */
export type PictureLoader = (source: string, signal: AbortSignal) => Promise<CanvasPicture>;

/** Makes a copy of `picture` reduced to `width` x `height`; rejects when it cannot. */
export type PictureReducer = (picture: CanvasPicture, width: number, height: number) => Promise<CanvasPicture>;

export interface CanvasImageCacheOptions {
  /** The address to fetch for a layer's picture; '' when it may not be fetched. */
  resolve: (url: string) => string;
  /** A picture (or a reduced copy for display) finished loading or failed: the board redraws. */
  onChange: () => void;
  /** Loads a picture; tests pass a fake. */
  load?: PictureLoader;
  /** Makes reduced display copies; tests pass a fake. Without `createImageBitmap` none are made. */
  reduce?: PictureReducer;
}

/**
 * #291: the deepest display copy, as halvings of the original (1/2, 1/4, 1/8). A zoomed-out board draws a 4K
 * layer at a few hundred device pixels; drawing it from the original makes the GPU take the whole picture
 * (33 MiB per 4K layer), which measured as a 60 ms frame after every send with ten 4K layers.
 */
export const MAXIMUM_DISPLAY_REDUCTION = 3;

/**
 * Which halving of the original to draw when `displayed` device pixels show `source` original pixels: the
 * smallest copy that is still at least as large as what is shown (0 = the original).
 */
export function displayReduction(source: number, displayed: number): number {
  if (!(source > 0) || !(displayed > 0) || displayed > source / 2) return 0;
  return Math.min(MAXIMUM_DISPLAY_REDUCTION, Math.floor(Math.log2(source / displayed)));
}

/**
 * The downloaded bytes of each decoded picture. A reduced copy is decoded from them rather than resized from the
 * bitmap: resizing the bitmap made the page hold about 130 MiB more with ten 4K layers (measured as renderer
 * memory), decoding the bytes at the smaller size about 40 MiB.
 */
const encodedSource = new WeakMap<CanvasPicture, Blob>();

function reduceBitmap(picture: CanvasPicture, width: number, height: number): Promise<CanvasPicture> {
  return createImageBitmap(encodedSource.get(picture) ?? picture, { resizeWidth: width, resizeHeight: height, resizeQuality: 'medium' });
}

function loadElement(source: string, signal: AbortSignal): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.crossOrigin = 'anonymous';
    const stop = () => {
      image.onload = null;
      image.onerror = null;
      image.removeAttribute('src');
      reject(signal.reason);
    };
    signal.addEventListener('abort', stop, { once: true });
    image.onload = () => {
      signal.removeEventListener('abort', stop);
      resolve(image);
    };
    image.onerror = () => {
      signal.removeEventListener('abort', stop);
      reject(new Error('picture failed to load'));
    };
    image.src = source;
  });
}

/**
 * Downloads a same-origin picture and decodes it off the main thread. The address must already have passed
 * `safeAssetURL`. Browsers without `createImageBitmap` get an `<img>` element.
 */
export async function decodePicture(source: string, signal: AbortSignal): Promise<CanvasPicture> {
  if (typeof createImageBitmap !== 'function') return loadElement(source, signal);
  const response = await fetch(source, { credentials: 'same-origin', signal });
  if (!response.ok) throw new Error(`picture request failed: ${response.status}`);
  const blob = await response.blob();
  signal.throwIfAborted();
  const bitmap = await createImageBitmap(blob);
  encodedSource.set(bitmap, blob);
  return bitmap;
}

interface Entry {
  /** Set once decoded (or put in by `set`). */
  picture?: CanvasPicture;
  /** Present while the picture is still loading. */
  loading?: AbortController;
  /**
   * The one reduced copy kept for display (#291), by its halving. It serves every view that needs that size
   * or a smaller one; only a view that needs a larger copy replaces it (#313). While it is being made `copy`
   * is unset.
   */
  display?: { reduction: number; copy?: CanvasPicture };
}

export class CanvasImageCache implements PictureSource {
  private readonly entries = new Map<string, Entry>();
  private readonly options: CanvasImageCacheOptions;

  constructor(options: CanvasImageCacheOptions) {
    this.options = options;
  }

  get size(): number {
    return this.entries.size;
  }

  /** The decoded picture; undefined while it is still loading. */
  get(url: string): CanvasPicture | undefined {
    return this.entries.get(url)?.picture;
  }

  /**
   * What to draw for the picture when `ratio` is the shown size over the original's (device pixels per original
   * pixel, below 1 when zoomed out): a reduced copy when the view is small enough, otherwise the original.
   * Asking for a copy that is not there yet starts making it (asynchronously) and answers with the original;
   * `onChange` follows when it is ready. One copy is kept per picture, and it serves every view that needs a
   * copy of that size or a smaller one: two layers of the same picture at different sizes must not replace
   * each other's copy on every redraw (#313). Only a view that needs a larger copy replaces it. The original
   * stays held for zooming in, painting, picking and export.
   */
  display(url: string, ratio: number): CanvasPicture | undefined {
    const entry = this.entries.get(url);
    const picture = entry?.picture;
    const size = pictureSize(picture);
    const reduce = this.options.reduce ?? (typeof createImageBitmap === 'function' ? reduceBitmap : undefined);
    if (!entry || !picture || !size || !reduce) return picture;
    const reduction = displayReduction(size.width, size.width * ratio);
    if (reduction === 0) return picture;
    const held = entry.display;
    if (held && held.reduction <= reduction) return held.copy ?? picture;
    const factor = 2 ** reduction;
    const width = Math.max(1, Math.round(size.width / factor));
    const height = Math.max(1, Math.round(size.height / factor));
    const making: NonNullable<Entry['display']> = { reduction };
    entry.display = making;
    reduce(picture, width, height).then(
      (copy) => {
        if (this.entries.get(url) !== entry || entry.picture !== picture || entry.display !== making) return;
        making.copy = copy;
        this.options.onChange();
      },
      () => {
        // No copy: the original keeps being drawn, and this size is not asked for again.
      },
    );
    return picture;
  }

  /** Held: loaded or loading. */
  has(url: string): boolean {
    return this.entries.has(url);
  }

  /** Puts a picture loaded elsewhere (an uploaded brush result) in the cache, replacing what was there. */
  set(url: string, picture: CanvasPicture): void {
    this.entries.get(url)?.loading?.abort();
    this.entries.set(url, { picture });
  }

  /**
   * Keeps the pictures `referenced` names and lets go of the rest; starts loading those of `wanted` (the visible
   * layers' pictures, a subset of `referenced`) that are not held yet.
   */
  sync(referenced: Iterable<string>, wanted: Iterable<string>): void {
    const keep = new Set(referenced);
    for (const [url, entry] of this.entries) {
      if (keep.has(url)) continue;
      this.entries.delete(url);
      // An unfinished download is abandoned. A decoded picture is left alone (not closed): a brush layer may
      // still draw it as its base, and it is collected once nothing holds it.
      entry.loading?.abort();
    }
    for (const url of wanted) {
      if (this.entries.has(url)) continue;
      const source = this.options.resolve(url);
      if (!source) continue;
      this.load(url, source);
    }
  }

  /** Lets go of everything (the board is unmounted). The cache stays usable. */
  clear(): void {
    for (const entry of this.entries.values()) entry.loading?.abort();
    this.entries.clear();
  }

  private load(url: string, source: string) {
    const loading = new AbortController();
    const entry: Entry = { loading };
    this.entries.set(url, entry);
    const load = this.options.load ?? decodePicture;
    load(source, loading.signal).then(
      (picture) => {
        if (loading.signal.aborted || this.entries.get(url) !== entry) return;
        entry.picture = picture;
        delete entry.loading;
        this.options.onChange();
      },
      () => {
        if (loading.signal.aborted || this.entries.get(url) !== entry) return;
        // A failed picture is not kept: the next sync that wants it asks for it again.
        this.entries.delete(url);
        this.options.onChange();
      },
    );
  }
}

/** The pictures the layers name (`referenced`) and those of visible layers (`wanted`), as stable keys. */
export function pictureKeys(nodes: ReadonlyArray<{ type: string; imageUrl: string; visible?: boolean }>): {
  referenced: string;
  wanted: string;
} {
  const referenced: string[] = [];
  const wanted: string[] = [];
  for (const node of nodes) {
    if (node.type !== 'image' || !node.imageUrl) continue;
    referenced.push(node.imageUrl);
    if (node.visible !== false) wanted.push(node.imageUrl);
  }
  return { referenced: referenced.join('\n'), wanted: wanted.join('\n') };
}

export function splitPictureKey(key: string): string[] {
  return key ? key.split('\n') : [];
}
