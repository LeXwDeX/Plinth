import type { ProductSchema } from './product-schema';

type ImageUploadPolicy = ProductSchema['runtime']['frontend']['image_upload'];
type ConversionRule = ImageUploadPolicy['conversions'][number];

type ImageUploadErrorCode =
  | 'unsupported'
  | 'file_too_large'
  | 'too_many_pixels'
  | 'conversion_failed'
  | 'read_failed';

export class ImageUploadError extends Error {
  constructor(public readonly code: ImageUploadErrorCode) {
    super(code);
    this.name = 'ImageUploadError';
  }
}

function matchesAccept(name: string, mediaType: string, accept?: string): boolean {
  if (!accept?.trim()) return true;
  const lowerName = name.toLowerCase();
  const lowerType = mediaType.toLowerCase();
  return accept.split(',').some((entry) => {
    const token = entry.trim().toLowerCase();
    if (token.startsWith('.')) return lowerName.endsWith(token);
    if (token.endsWith('/*')) return lowerType.startsWith(token.slice(0, -1));
    return token !== '' && token === lowerType;
  });
}

function acceptsConversion(rule: ConversionRule, accept?: string): boolean {
  const extension = rule.target_media_type.split('/')[1];
  return matchesAccept(`converted.${extension}`, rule.target_media_type, accept);
}

function conversionFor(file: File, policy: ImageUploadPolicy): ConversionRule | undefined {
  const name = file.name.toLowerCase();
  const mediaType = file.type.toLowerCase();
  return policy.conversions.find((rule) =>
    (rule.source_media_types ?? []).some((type) => type.toLowerCase() === mediaType) ||
    (rule.source_extensions ?? []).some((extension) => name.endsWith(extension.toLowerCase())),
  );
}

/** Advertise convertible inputs only when their output is accepted by this upload. */
export function imageUploadAccept(accept: string | undefined, policy: ImageUploadPolicy): string | undefined {
  if (!accept?.trim()) return accept;
  const tokens = accept.split(',').map((token) => token.trim()).filter(Boolean);
  for (const rule of policy.conversions) {
    if (acceptsConversion(rule, accept)) {
      tokens.push(...(rule.source_media_types ?? []), ...(rule.source_extensions ?? []));
    }
  }
  return [...new Set(tokens)].join(',');
}

function readDataUri(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === 'string'
      ? resolve(reader.result)
      : reject(new ImageUploadError('read_failed'));
    reader.onerror = reader.onabort = () => reject(new ImageUploadError('read_failed'));
    reader.readAsDataURL(blob);
  });
}

async function convertImage(file: File, rule: ConversionRule, maximumPixels: number): Promise<Blob> {
  const image = new Image();
  const url = URL.createObjectURL(file);
  let canvas: HTMLCanvasElement | undefined;
  try {
    await new Promise<void>((resolve, reject) => {
      image.onload = () => resolve();
      image.onerror = () => reject(new ImageUploadError('conversion_failed'));
      image.src = url;
    });
    const width = image.naturalWidth;
    const height = image.naturalHeight;
    if (width <= 0 || height <= 0) throw new ImageUploadError('conversion_failed');
    if (width * height > maximumPixels) throw new ImageUploadError('too_many_pixels');

    canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d');
    if (!context) throw new ImageUploadError('conversion_failed');
    context.drawImage(image, 0, 0);
    const output = await new Promise<Blob | null>((resolve) => {
      canvas!.toBlob(resolve, rule.target_media_type);
    });
    if (!output || output.type.toLowerCase() !== rule.target_media_type.toLowerCase()) {
      throw new ImageUploadError('conversion_failed');
    }
    return output;
  } catch (error) {
    if (error instanceof ImageUploadError) throw error;
    throw new ImageUploadError('conversion_failed');
  } finally {
    image.onload = image.onerror = null;
    image.src = '';
    URL.revokeObjectURL(url);
    if (canvas) {
      canvas.width = 0;
      canvas.height = 0;
    }
  }
}

/** Validate dropped and selected files identically, preserving supported files' bytes. */
export async function prepareImageUploadBlob(
  file: File,
  options: { accept?: string; maximumBytes: number; policy: ImageUploadPolicy },
): Promise<Blob> {
  const { accept, maximumBytes, policy } = options;
  if (file.size > maximumBytes) throw new ImageUploadError('file_too_large');

  const conversion = conversionFor(file, policy);
  if (conversion) {
    if (!acceptsConversion(conversion, accept)) throw new ImageUploadError('unsupported');
    const output = await convertImage(file, conversion, policy.maximum_pixels);
    if (output.size > maximumBytes) throw new ImageUploadError('file_too_large');
    return output;
  }
  if (!matchesAccept(file.name, file.type, accept)) throw new ImageUploadError('unsupported');
  return file;
}

// The names of the files behind recent data addresses, newest last. A form keeps only the data address;
// an upload of it later asks for the name here, so the uploaded input keeps the person's file name (#187 B).
// The key is a digest of the address, never the address itself (#335): an address is the whole picture
// (up to ~70 MB), and keeping it here kept the picture alive after its form had let it go. With at most
// UPLOAD_NAME_LIMIT entries of a fixed-length key and a name of at most UPLOAD_NAME_MAX_LENGTH characters,
// the map holds at most IMAGE_UPLOAD_NAME_CACHE_MAX_BYTES, whatever the pictures weigh.
const uploadNames = new Map<string, string>();
const UPLOAD_NAME_LIMIT = 32;
const UPLOAD_NAME_MAX_LENGTH = 255;
const DIGEST_LENGTH = 24;
/** The most the file-name map holds (UTF-16 code units of keys and names, two bytes each). */
export const IMAGE_UPLOAD_NAME_CACHE_MAX_BYTES = UPLOAD_NAME_LIMIT * (DIGEST_LENGTH + UPLOAD_NAME_MAX_LENGTH) * 2;

/**
 * A short fixed-length digest of every character of a data address: two independent 32-bit lanes
 * (cyrb53-style mixing) and the length. Not cryptographic; a clash could only give an upload the wrong
 * file name, and the bytes uploaded are always the form's own. About 130 ms for a 70 MB picture.
 */
function dataAddressDigest(dataUri: string): string {
  let first = 0xdeadbeef ^ dataUri.length;
  let second = 0x41c6ce57 ^ dataUri.length;
  for (let index = 0; index < dataUri.length; index += 1) {
    const code = dataUri.charCodeAt(index);
    first = Math.imul(first ^ code, 2654435761);
    second = Math.imul(second ^ code, 1597334677);
  }
  first = Math.imul(first ^ (first >>> 16), 2246822507) ^ Math.imul(second ^ (second >>> 13), 3266489909);
  second = Math.imul(second ^ (second >>> 16), 2246822507) ^ Math.imul(first ^ (first >>> 13), 3266489909);
  const hex = (value: number) => (value >>> 0).toString(16).padStart(8, '0');
  return `${hex(first)}${hex(second)}${hex(dataUri.length)}`;
}

/** The name of the file a data address was read from by `prepareImageUpload`, if it is still known. */
export function imageUploadName(dataUri: string): string | undefined {
  return uploadNames.get(dataAddressDigest(dataUri));
}

/** What the file-name map holds now, in bytes (for the memory check of #335). */
export function imageUploadNameCacheBytes(): number {
  let units = 0;
  for (const [key, name] of uploadNames) units += key.length + name.length;
  return units * 2;
}

/**
 * Agent inputs retain their Data URI protocol over the same validation/conversion. `rememberName` keeps
 * the file's name for a later upload of the address (form fields that become input assets); a chat
 * attachment is uploaded under a name of its own and does not ask for it.
 */
export async function prepareImageUpload(
  file: File,
  options: { accept?: string; maximumBytes: number; policy: ImageUploadPolicy; rememberName?: boolean },
): Promise<string> {
  const dataUri = await readDataUri(await prepareImageUploadBlob(file, options));
  if (options.rememberName && file.name) {
    const key = dataAddressDigest(dataUri);
    uploadNames.delete(key);
    uploadNames.set(key, file.name.slice(0, UPLOAD_NAME_MAX_LENGTH));
    while (uploadNames.size > UPLOAD_NAME_LIMIT) uploadNames.delete(uploadNames.keys().next().value!);
  }
  return dataUri;
}
