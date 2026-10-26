/**
 * Pictures held as base64 data addresses (`data:image/png;base64,…`), turned into bytes in the page.
 *
 * The site CSP's connect-src allows only 'self' and blob:, so `fetch(data:…)` fails in every browser
 * ("Failed to fetch" in Chromium, "Load failed" in WebKit). Code that needs the bytes of a data address
 * decodes it here instead of fetching it (#187 A); the CSP stays as it is.
 */

/** Only the head is matched: a data address can be tens of megabytes. */
const DATA_HEAD = /^data:([a-z0-9.+-]+\/[a-z0-9.+-]+)?((?:;[a-z0-9.+-]+=[^;,]*)*);base64,/i;

export function isDataAddress(value: string): boolean {
  return value.slice(0, 5).toLowerCase() === 'data:';
}

/** The media type the bytes start with, for the picture formats an upload accepts; '' when unknown. */
export function sniffImageType(bytes: Uint8Array): string {
  const at = (offset: number, signature: readonly number[]) => signature.every((byte, index) => bytes[offset + index] === byte);
  if (at(0, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) return 'image/png';
  if (at(0, [0xff, 0xd8, 0xff])) return 'image/jpeg';
  if (at(0, [0x52, 0x49, 0x46, 0x46]) && at(8, [0x57, 0x45, 0x42, 0x50])) return 'image/webp';
  if (at(0, [0x47, 0x49, 0x46, 0x38])) return 'image/gif';
  if (at(0, [0x42, 0x4d])) return 'image/bmp';
  return '';
}

/**
 * The bytes of a base64 data address as a Blob, decoded in the page. Its type is the one the bytes
 * show when they are a known picture format, otherwise the declared one. Throws for anything that is
 * not a base64 data address.
 */
export function dataAddressBlob(value: string): Blob {
  const head = DATA_HEAD.exec(value.slice(0, 256));
  if (!head) throw new Error('not a base64 data address');
  const binary = atob(value.slice(head[0].length));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return new Blob([bytes], { type: sniffImageType(bytes) || (head[1] ?? '').toLowerCase() });
}

const EXTENSIONS: Record<string, string> = {
  'image/png': '.png',
  'image/jpeg': '.jpg',
  'image/webp': '.webp',
  'image/gif': '.gif',
  'image/bmp': '.bmp',
  'image/avif': '.avif',
  'image/tiff': '.tiff',
  'image/svg+xml': '.svg',
};

/** The file extension of a media type, '' when there is no known one. */
export function mediaExtension(type: string): string {
  return EXTENSIONS[type.toLowerCase()] ?? '';
}

const EQUIVALENT: Record<string, string[]> = { '.jpg': ['.jpg', '.jpeg', '.jpe', '.jfif'], '.tiff': ['.tiff', '.tif'] };

/**
 * The file name to upload bytes of `type` under. A name the person's own file had is kept when its
 * extension already matches the type; otherwise its stem gets the type's extension. Without a usable
 * original name, `fallbackStem` is used. The server checks an input's extension, so the name always
 * carries the one of the real type when that type is known.
 */
export function uploadFilename(type: string, fallbackStem: string, original?: string): string {
  const extension = mediaExtension(type);
  const base = (original ?? '').split(/[\\/]/).pop()?.trim() ?? '';
  const dot = base.lastIndexOf('.');
  // A name that is only an extension (".png") has no stem worth keeping.
  const stem = (dot >= 0 ? base.slice(0, dot) : base).trim();
  if (stem) {
    const current = dot > 0 ? base.slice(dot).toLowerCase() : '';
    if (!extension || (EQUIVALENT[extension] ?? [extension]).includes(current)) return base;
    return `${stem}${extension}`;
  }
  return `${fallbackStem}${extension}`;
}
