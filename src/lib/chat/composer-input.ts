import { isClipboardMarker } from '@/lib/canvas/clipboard';
import { hasCanvasDrag } from '@/lib/workspace/canvas-drag';
import { dataAddressBlob, isDataAddress } from '@/lib/data-address';

/** Image files on the clipboard (a copied screenshot or picture); anything else stays a normal paste. */
export function pastedImageFiles(files: FileList | readonly File[] | null | undefined): File[] {
  return Array.from(files ?? []).filter((file) => file.type.startsWith('image/'));
}

/** What a paste into the composer turns into: words for the prompt and pictures for the attachments. */
export interface ComposerPaste {
  /** Picture files on the clipboard (a screenshot, a copied picture). */
  files: File[];
  /** Picture addresses of copied rich content (chat pictures, a web page), read and attached as uploads. */
  imageUrls: string[];
  /** Words for the prompt; never a canvas marker. */
  text: string;
  /** False: nothing but plain words, so the box pastes them itself. */
  handled: boolean;
}

/**
 * Splits a paste into prompt words and attachments (#281). Plain text is left to the text box. With pictures,
 * the words become the prompt and each picture an attachment, once: picture files win over the `<img>` copies
 * of the same pictures in the HTML. A canvas marker (layers copied on the canvas) is never written as words.
 */
export function planComposerPaste(input: {
  files: FileList | readonly File[] | null | undefined;
  text: string;
  html: string;
  baseURL: string;
}): ComposerPaste {
  const files = pastedImageFiles(input.files);
  const marker = isClipboardMarker(input.text);
  const imageUrls = files.length > 0 ? [] : htmlImageSources(input.html, input.baseURL);
  if (!marker && files.length === 0 && imageUrls.length === 0) return { files, imageUrls, text: input.text, handled: false };
  let text = marker ? '' : normalizePastedText(input.text);
  // A file copied in a file manager brings its own name as the text: that is not a prompt.
  if (files.some((file) => file.name === text)) text = '';
  return { files, imageUrls, text, handled: true };
}

const IMAGE_SOURCE = /<img\b[^>]*?\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))/gi;
const ENTITIES: Record<string, string> = { amp: '&', quot: '"', apos: "'", lt: '<', gt: '>', '#39': "'" };

/**
 * The pictures of copied HTML, in order and once each; only addresses a page can read (http(s), blob and
 * data:image). Clipboard HTML is written by the browser, so the `src` attributes are read as they are.
 */
export function htmlImageSources(html: string, baseURL: string): string[] {
  const sources: string[] = [];
  for (const match of html.matchAll(IMAGE_SOURCE)) {
    const raw = (match[1] ?? match[2] ?? match[3] ?? '').replace(/&(amp|quot|apos|lt|gt|#39);/g, (_, name: string) => ENTITIES[name]).trim();
    if (!raw) continue;
    let url: URL;
    try {
      url = new URL(raw, baseURL);
    } catch {
      continue;
    }
    const readable = url.protocol === 'http:' || url.protocol === 'https:' || url.protocol === 'blob:'
      || (url.protocol === 'data:' && /^data:image\//i.test(url.href));
    if (readable && !sources.includes(url.href)) sources.push(url.href);
  }
  return sources;
}

/** Pasted words with tidy line breaks: CRLF to LF, no trailing spaces, at most one empty line in a row. */
export function normalizePastedText(text: string): string {
  return text
    .replace(/\r\n?/g, '\n')
    .replace(/ /g, ' ')
    .replace(/[ \t]+$/gm, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

/** The value after pasting `text` over the selection [start, end), and where the caret goes. */
export function insertPastedText(value: string, start: number, end: number, text: string): { value: string; caret: number } {
  const from = Math.max(0, Math.min(start, value.length));
  const to = Math.max(from, Math.min(end, value.length));
  return { value: value.slice(0, from) + text + value.slice(to), caret: from + text.length };
}

/**
 * Reads pasted pictures into files for the usual upload path (type, size and count are checked there).
 * A picture that cannot be read (no access, not an image, offline) is counted, not thrown: the words stay.
 */
export async function readPastedImages(
  urls: readonly string[],
  fetcher: (url: string, init: RequestInit) => Promise<Response> = (url, init) => fetch(url, init),
): Promise<{ files: File[]; failed: number }> {
  const results = await Promise.all(urls.map(async (url, index) => {
    try {
      // A data: picture is decoded in the page: the CSP connect-src has no data: (#187 A).
      let blob: Blob;
      if (isDataAddress(url)) {
        blob = dataAddressBlob(url);
      } else {
        const response = await fetcher(url, { credentials: 'same-origin' });
        if (!response.ok) return null;
        blob = await response.blob();
      }
      if (!blob.type.startsWith('image/')) return null;
      return new File([blob], pastedImageName(url, index, blob.type), { type: blob.type });
    } catch {
      return null;
    }
  }));
  const files = results.filter((file): file is File => file !== null);
  return { files, failed: results.length - files.length };
}

function pastedImageName(url: string, index: number, type: string): string {
  const extension = type.split('/')[1]?.split('+')[0] || 'png';
  if (!url.startsWith('data:') && !url.startsWith('blob:')) {
    try {
      const last = new URL(url).pathname.split('/').filter(Boolean).pop();
      if (last) return /\.[a-z0-9]+$/i.test(last) ? last : `${last}.${extension}`;
    } catch {
      // fall through to the numbered name
    }
  }
  return `pasted-${index + 1}.${extension}`;
}

/** Files from a drop; the upload path validates type and size per file. */
export function dropFiles(files: FileList | readonly File[] | null | undefined): File[] {
  return Array.from(files ?? []);
}

/**
 * What is being dragged over the composer: a result card (the canvas item type) or files from the system.
 * Text drags and anything else are left to the box's own behaviour.
 */
export function composerDragKind(types: ReadonlyArray<string> | DOMStringList | null | undefined): 'card' | 'files' | null {
  if (hasCanvasDrag(types)) return 'card';
  if (types && Array.from(types as ArrayLike<string>).includes('Files')) return 'files';
  return null;
}
