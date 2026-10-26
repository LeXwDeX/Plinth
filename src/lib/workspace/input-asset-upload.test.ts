import { describe, expect, it, vi } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { RequestFailure } from '@/lib/problem-reference';
import { uploadWorkspaceInputAsset } from './input-asset-upload';

const schema = {
  runtime: {
    input_assets: { upload_field_name: 'configuredFile', api: { fields: { id: 'nativeID', asset_id: 'legacyAssetID' } } },
    http: { routes: { assets: '/api/v1/assets', asset_item: '/api/v1/assets/{assetID}' } },
  },
} as unknown as ProductSchema;

describe('workspace native input-asset upload', () => {
  it('uses the native response ID and returns an owner-scoped asset URL', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({ nativeID: 'asset/a b' }), { status: 201 }));
    const blob = new Blob(['png-bytes'], { type: 'image/png' });

    await expect(uploadWorkspaceInputAsset(schema, blob, 'reference.png', fetcher))
      .resolves.toBe('/api/v1/assets/asset%2Fa%20b');
    const [route, options] = fetcher.mock.calls[0];
    expect(route).toBe('/api/v1/assets');
    expect(options?.method).toBe('POST');
    expect(options?.body).toBeInstanceOf(FormData);
    expect((options?.body as FormData).get('configuredFile')).toBeInstanceOf(File);
  });

  it('rejects a legacy-only response to the native route', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({ legacyAssetID: 'legacy' }), { status: 201 }));
    await expect(uploadWorkspaceInputAsset(schema, new Blob(['x']), 'reference.png', fetcher))
      .rejects.toThrow('asset upload returned no ID');
  });

  it('keeps the request ID of a refused upload (#274)', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({ code: 'invalid_input', request_id: 'req_08d39bb70c2ff5ed' }), { status: 400 }));
    const failure = await uploadWorkspaceInputAsset(schema, new Blob(['x']), 'canvas-inpaint.png', fetcher).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(RequestFailure);
    expect((failure as RequestFailure).message).toBe('asset upload 400');
    expect((failure as RequestFailure).requestId).toBe('req_08d39bb70c2ff5ed');
  });

  it('falls back to the X-Request-ID header when the refusal has no JSON body', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response('bad gateway', { status: 502, headers: { 'X-Request-ID': 'req_0123456789abcdef' } }));
    const failure = await uploadWorkspaceInputAsset(schema, new Blob(['x']), 'canvas-inpaint.png', fetcher).catch((error: unknown) => error);
    expect((failure as RequestFailure).requestId).toBe('req_0123456789abcdef');
  });
});
