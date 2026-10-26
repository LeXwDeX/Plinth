/**
 * #292: a picture attached from disk or pasted lives in the composer as a data address until its turn is sent.
 * Before #292 the data address itself was sent, so the whole picture was stored as base64 inside the chat
 * message: every read of the session carried it, the transcript could not show it (a data address does not pass
 * `safeAssetURL`), and the browser held several copies. Now every such picture is uploaded as the person's input
 * asset right before the turn starts; the message keeps the asset address, the transcript shows its preview, and
 * the claim names the asset. The Agent gets the same bytes: a generation reference resolves to that asset (the
 * data address used to be uploaded at that point instead), and vision reads the asset back as a data address.
 *
 * Like the board snapshot (#286) each failure keeps its step, so the person is told why nothing was sent and the
 * client error report names the step, the picture's slot and size, and the upload's request ID.
 */
import { reportClientError } from '@/lib/client-error-report';
import { reasonOf, requestIdOf, withErrorReference } from '@/lib/problem-reference';
import { schemaMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatAttachmentClaim } from './attachments';
import { sizeText } from '@/lib/size-text';

/** Only the head is read: a data address can be tens of megabytes. */
const HEAD = /^data:(image\/[a-z0-9.+-]+);base64,/i;
const HEAD_MAX = 96;

/** A picture that is still a data address, not yet a platform asset. */
export function isInlineImage(url: string): boolean {
  return url.startsWith('data:');
}

/** The bytes of a base64 image data address; throws when it is not one. */
export function inlineImageBlob(url: string): Blob {
  const head = HEAD.exec(url.slice(0, HEAD_MAX));
  if (!head) throw new Error('not a base64 image data address');
  const binary = atob(url.slice(head[0].length));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return new Blob([bytes], { type: head[1].toLowerCase() });
}

/**
 * The filename an uploaded composer picture carries: the Schema's `chat.context.reference_filename` (the name the
 * server gives a data URI it uploads as a reference) with the extension of the picture's media type.
 */
export function inlineImageFilename(schema: ProductSchema, picture: Blob): string {
  const subtype = /^image\/([a-z0-9]+)$/.exec(picture.type.toLowerCase())?.[1] ?? '';
  const extension = subtype === 'jpeg' ? '.jpg' : subtype ? `.${subtype}` : '';
  return `${schema.chat.context.reference_filename}${extension}`;
}

export type InlineImageStep = 'decode' | 'upload';

/** The picture is larger than the Schema's input-asset upload limit; nothing was sent. */
export class InlineImageTooLarge extends Error {
  constructor(readonly bytes: number, readonly limit: number) {
    super(`attached picture is ${bytes} bytes, upload limit ${limit} bytes`);
    this.name = 'InlineImageTooLarge';
  }
}

function describe(error: unknown): string {
  if (error instanceof Error) return `${error.name}: ${error.message}`;
  return String(error);
}

export class InlineImageUploadFailure extends Error {
  constructor(readonly step: InlineImageStep, readonly slot: number, readonly cause: unknown, readonly bytes = 0) {
    super(`attached picture ${slot} ${step} failed: ${describe(cause)}${bytes > 0 ? ` bytes=${bytes}` : ''}`);
    this.name = 'InlineImageUploadFailure';
  }
}

export interface InlineImageSteps {
  /** Uploads one picture as the person's input asset (`uploadWorkspaceInputAssetResult`). */
  upload: (picture: Blob) => Promise<{ id: string; url: string }>;
  /** The Schema's `runtime.input_assets.upload_max_bytes`: a larger picture is refused before the POST. */
  maximumBytes: number;
}

/**
 * Uploads every data-address picture of a turn and returns the turn's pictures and claims with the asset in its
 * place. Pictures that already are platform addresses, and their claims, are kept as they are. The same data
 * address twice is uploaded once. The first failure stops the turn: nothing after it is uploaded.
 */
export async function uploadInlineImages(
  images: readonly string[],
  claims: readonly ChatAttachmentClaim[],
  steps: InlineImageSteps,
): Promise<{ images: string[]; claims: ChatAttachmentClaim[] }> {
  if (!images.some(isInlineImage)) return { images: [...images], claims: [...claims] };
  const uploaded = new Map<string, { id: string; url: string }>();
  const nextImages: string[] = [];
  const nextClaims = [...claims];
  for (const [index, url] of images.entries()) {
    if (!isInlineImage(url)) {
      nextImages.push(url);
      continue;
    }
    const slot = index + 1;
    let asset = uploaded.get(url);
    if (!asset) {
      let picture: Blob;
      try {
        picture = inlineImageBlob(url);
      } catch (error) {
        throw new InlineImageUploadFailure('decode', slot, error);
      }
      if (steps.maximumBytes > 0 && picture.size > steps.maximumBytes) {
        throw new InlineImageUploadFailure('upload', slot, new InlineImageTooLarge(picture.size, steps.maximumBytes), picture.size);
      }
      try {
        asset = await steps.upload(picture);
      } catch (error) {
        throw new InlineImageUploadFailure('upload', slot, error, picture.size);
      }
      uploaded.set(url, asset);
    }
    nextImages.push(asset.url);
    // A data address has no platform identity (the server refuses IDs claimed for one); the asset now has one.
    const claim: ChatAttachmentClaim = { kind: 'image', imageSlot: slot, assetId: asset.id };
    const existing = nextClaims.findIndex((item) => item.imageSlot === slot);
    if (existing >= 0) nextClaims[existing] = claim;
    else nextClaims.push(claim);
  }
  return { images: nextImages, claims: nextClaims };
}

/** What the person is told: the Schema text for the failure; an upload failure keeps its request ID. */
export function inlineImageFailureText(schema: ProductSchema, failure: unknown): string {
  const cause = failure instanceof InlineImageUploadFailure ? failure.cause : failure;
  const slot = failure instanceof InlineImageUploadFailure ? failure.slot : 0;
  if (cause instanceof InlineImageTooLarge) {
    return schemaMessage(schema, 'ui.chat.input.attachment_too_large', {
      index: slot,
      size: sizeText(cause.bytes, true),
      limit: sizeText(cause.limit, false),
    });
  }
  return withErrorReference(reasonOf(cause) || schemaText(schema, 'ui.chat.input.attachment_upload_failed'), cause);
}

/** Reports the failure to the server log (#233) and returns the text to show. */
export function reportInlineImageFailure(schema: ProductSchema, failure: unknown): string {
  const step = failure instanceof InlineImageUploadFailure ? failure.step : 'upload';
  const cause = failure instanceof InlineImageUploadFailure ? failure.cause : failure;
  reportClientError(`chat-attachment-${step}`, failure, { requestId: requestIdOf(cause) });
  return inlineImageFailureText(schema, failure);
}
