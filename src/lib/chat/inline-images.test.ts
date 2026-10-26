import { afterEach, describe, expect, it, vi } from 'vitest';

const reported = vi.hoisted(() => vi.fn());
vi.mock('@/lib/client-error-report', () => ({ reportClientError: reported }));

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { configureErrorReference, RequestFailure } from '@/lib/problem-reference';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatAttachmentClaim } from './attachments';
import {
  InlineImageTooLarge,
  InlineImageUploadFailure,
  inlineImageBlob,
  inlineImageFailureText,
  inlineImageFilename,
  isInlineImage,
  reportInlineImageFailure,
  uploadInlineImages,
} from './inline-images';

const application = defaultCatalog.application as unknown as {
  content: { messages: Record<string, string> };
  chat: { context: { reference_filename: string } };
};
const messages = application.content.messages;
const schema = {
  content: { messages },
  chat: { context: { reference_filename: application.chat.context.reference_filename } },
  runtime: {},
} as unknown as ProductSchema;

// "PNG!" as the picture's bytes.
const png = 'data:image/png;base64,UE5HIQ==';
const jpeg = 'data:image/jpeg;base64,SlBFRw==';
const asset = '/api/v1/assets/asset_existing';

async function bytesOf(blob: Blob): Promise<string> {
  return new TextDecoder().decode(await blob.arrayBuffer());
}

function uploader() {
  let next = 0;
  const uploaded: Blob[] = [];
  const upload = vi.fn(async (picture: Blob) => {
    uploaded.push(picture);
    next += 1;
    return { id: `asset_${next}`, url: `/api/v1/assets/asset_${next}` };
  });
  return { upload, uploaded };
}

async function failureOf(promise: Promise<unknown>): Promise<InlineImageUploadFailure> {
  const failure = await promise.then(() => null, (error: unknown) => error);
  expect(failure).toBeInstanceOf(InlineImageUploadFailure);
  return failure as InlineImageUploadFailure;
}

afterEach(() => {
  reported.mockReset();
  configureErrorReference(undefined);
});

describe('inline composer pictures (#292)', () => {
  it('tells a data address from a platform address', () => {
    expect(isInlineImage(png)).toBe(true);
    expect(isInlineImage(asset)).toBe(false);
    expect(isInlineImage('/api/v1/tasks/t1/assets/a1')).toBe(false);
  });

  it('decodes the exact bytes and media type of a base64 image data address', async () => {
    const blob = inlineImageBlob(png);
    expect(blob.type).toBe('image/png');
    expect(await bytesOf(blob)).toBe('PNG!');
    expect(() => inlineImageBlob('data:text/plain;base64,QQ==')).toThrow();
    expect(() => inlineImageBlob('data:image/png,raw')).toThrow();
  });

  it('names the upload after the Schema reference filename and the media type', () => {
    expect(inlineImageFilename(schema, new Blob([], { type: 'image/png' }))).toBe(`${application.chat.context.reference_filename}.png`);
    expect(inlineImageFilename(schema, new Blob([], { type: 'image/jpeg' }))).toBe(`${application.chat.context.reference_filename}.jpg`);
    expect(inlineImageFilename(schema, new Blob([]))).toBe(application.chat.context.reference_filename);
  });

  it('leaves a turn without data addresses as it is and uploads nothing', async () => {
    const { upload } = uploader();
    const claims: ChatAttachmentClaim[] = [{ kind: 'image', imageSlot: 1, assetId: 'asset_existing' }];
    const result = await uploadInlineImages([asset], claims, { upload, maximumBytes: 1024 });
    expect(result).toEqual({ images: [asset], claims });
    expect(upload).not.toHaveBeenCalled();
  });

  it('puts the uploaded asset in place of each data address and claims it for that slot', async () => {
    const { upload, uploaded } = uploader();
    const claims: ChatAttachmentClaim[] = [{ kind: 'image', imageSlot: 2, assetId: 'asset_existing' }];
    const result = await uploadInlineImages([png, asset, jpeg], claims, { upload, maximumBytes: 1024 });
    expect(result.images).toEqual(['/api/v1/assets/asset_1', asset, '/api/v1/assets/asset_2']);
    expect(result.claims).toEqual([
      { kind: 'image', imageSlot: 2, assetId: 'asset_existing' },
      { kind: 'image', imageSlot: 1, assetId: 'asset_1' },
      { kind: 'image', imageSlot: 3, assetId: 'asset_2' },
    ]);
    // The asset holds the very bytes the data address carried, so the Agent reads the same picture.
    expect(await Promise.all(uploaded.map(bytesOf))).toEqual(['PNG!', 'JPEG']);
    expect(uploaded.map((blob) => blob.type)).toEqual(['image/png', 'image/jpeg']);
    expect(result.images.some(isInlineImage)).toBe(false);
  });

  it('replaces a claim that a replayed turn kept for a data address slot', async () => {
    const { upload } = uploader();
    const result = await uploadInlineImages([png], [{ kind: 'image', imageSlot: 1 }], { upload, maximumBytes: 1024 });
    expect(result.claims).toEqual([{ kind: 'image', imageSlot: 1, assetId: 'asset_1' }]);
  });

  it('uploads the same data address once', async () => {
    const { upload } = uploader();
    const result = await uploadInlineImages([png, png], [], { upload, maximumBytes: 1024 });
    expect(upload).toHaveBeenCalledTimes(1);
    expect(result.images).toEqual(['/api/v1/assets/asset_1', '/api/v1/assets/asset_1']);
    expect(result.claims.map((claim) => [claim.imageSlot, claim.assetId])).toEqual([[1, 'asset_1'], [2, 'asset_1']]);
  });

  it('refuses a picture over the upload limit before any request', async () => {
    const { upload } = uploader();
    const failure = await failureOf(uploadInlineImages([png], [], { upload, maximumBytes: 2 }));
    expect(failure.step).toBe('upload');
    expect(failure.slot).toBe(1);
    expect(failure.cause).toBeInstanceOf(InlineImageTooLarge);
    expect(upload).not.toHaveBeenCalled();
    expect(inlineImageFailureText(schema, failure)).toBe(
      messages['ui.chat.input.attachment_too_large'].replace('{index}', '1').replace('{size}', '0.01 KB').replace('{limit}', '0 KB'),
    );
  });

  it('stops at the first failing picture and keeps the request ID for the text and the report', async () => {
    configureErrorReference('（错误 ID：{id}）');
    const upload = vi.fn(async () => { throw new RequestFailure('asset upload 500', 'req_08d39bb70c2ff5ed'); });
    const failure = await failureOf(uploadInlineImages([asset, png, jpeg], [], { upload, maximumBytes: 1024 }));
    expect(failure.step).toBe('upload');
    expect(failure.slot).toBe(2);
    expect(failure.bytes).toBe(4);
    expect(upload).toHaveBeenCalledTimes(1);
    const text = reportInlineImageFailure(schema, failure);
    expect(text).toBe(`${messages['ui.chat.input.attachment_upload_failed']}（错误 ID：req_08d39bb70c2ff5ed）`);
    expect(reported).toHaveBeenCalledWith('chat-attachment-upload', failure, { requestId: 'req_08d39bb70c2ff5ed' });
  });

  it('shows the server reason when the upload was refused with one', async () => {
    const upload = vi.fn(async () => { throw new RequestFailure('asset upload 400', '', '图片格式不受支持'); });
    const failure = await failureOf(uploadInlineImages([png], [], { upload, maximumBytes: 1024 }));
    expect(inlineImageFailureText(schema, failure)).toBe('图片格式不受支持');
  });

  it('reports an unreadable data address as the decode step', async () => {
    const { upload } = uploader();
    const failure = await failureOf(uploadInlineImages(['data:image/png;base64,***'], [], { upload, maximumBytes: 1024 }));
    expect(failure.step).toBe('decode');
    expect(upload).not.toHaveBeenCalled();
    expect(reportInlineImageFailure(schema, failure)).toBe(messages['ui.chat.input.attachment_upload_failed']);
    expect(reported).toHaveBeenCalledWith('chat-attachment-decode', failure, { requestId: '' });
  });
});
