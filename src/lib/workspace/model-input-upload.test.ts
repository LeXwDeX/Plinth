import { describe, expect, it, vi } from 'vitest';
import type { ProductSchema } from '@/lib/product-schema';
import { ModelInputUploadError, modelInputAccept, uploadModelInputSource } from './model-input-upload';

const schema = { runtime: { input_assets: { upload_field_name: 'sourceFile', upload_max_bytes: 20, api: { fields: { id: 'nativeID' } }, model_profile: { id: 'declared-profile', revision: '7', label: 'Declared model', description: 'Bounded profile', asset_type: 'model', extensions: ['.CUSTOM'], media_types: ['model/custom'], upload_media_type: 'model/custom', limits: { max_bytes: 12 } } }, http: { routes: { assets: '/configured/assets', asset_item: '/configured/assets/{assetID}' } } } } as unknown as ProductSchema;
const file = () => new File(['valid-bytes'], 'Character.CUSTOM', { type: '' });
const current = () => true;

describe('owned model input upload', () => {
  it('uses the declared profile, original bytes and native upload route, returning an asset-only reference', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ nativeID: 'asset/a b', media_type: 'model/custom', size: 11 }), { status: 201 }));
    const signal = new AbortController().signal;
    const result = await uploadModelInputSource(schema, file(), 'model_bytes', { isCurrent: current, signal }, fetcher);
    expect(result?.source).toEqual({ kind: 'input_asset', parameter: 'model_bytes', asset_id: 'asset/a b' });
    expect(result?.source).not.toHaveProperty('task_id');
    expect(result?.view).toMatchObject({ asset_id: 'asset/a b', original_filename: 'Character.CUSTOM', profile_id: 'declared-profile', state: 'ready' });
    expect(result?.url).toBe('/configured/assets/asset%2Fa%20b');
    const [url, options] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/configured/assets'); expect(options.method).toBe('POST'); expect(options.signal).toBe(signal);
    const uploaded = (options.body as FormData).get('sourceFile') as File;
    expect(uploaded.name).toBe('Character.CUSTOM'); expect(uploaded.type).toBe('model/custom'); expect(await uploaded.text()).toBe('valid-bytes');
    expect(modelInputAccept(schema.runtime.input_assets.model_profile!)).toBe('.CUSTOM,model/custom');
    expect(fetcher).toHaveBeenCalledOnce();
  });
  it('does not invent a GLB whitelist when the declared extension changes', async () => {
    const fetcher = vi.fn();
    await expect(uploadModelInputSource(schema, new File(['x'], 'a.glb'), 'source', { isCurrent: current }, fetcher)).rejects.toMatchObject({ code: 'extension', message: 'ui.harness.upload.format_unsupported' });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each(['profile', 'global'])('checks the tighter %s upload byte limit before any request', async limit => {
    const changed = structuredClone(schema); if (limit === 'global') changed.runtime.input_assets.upload_max_bytes = 8;
    const fetcher = vi.fn(); const bytes = limit === 'profile' ? 13 : 9;
    await expect(uploadModelInputSource(changed, new File([new Uint8Array(bytes)], 'a.custom'), 'source', { isCurrent: current }, fetcher)).rejects.toMatchObject({ code: 'size' });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each([
    [{ error: { code: 'model_unsupported_profile', message: 'error.model_unsupported_profile' } }, 'model_unsupported_profile', 'error.model_unsupported_profile'],
    [{ error: { code: 'model_invalid', message: '文件结构不合法' } }, 'model_invalid', '文件结构不合法'],
    [{ code: 'invalid_input', detail: 'error.legacy_validation' }, 'invalid_input', 'error.legacy_validation'],
  ])('preserves a structured safe server reason instead of replacing it with HTTP status', async (body, code, message) => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify(body), { status: 422 }));
    await expect(uploadModelInputSource(schema, file(), 'source', { isCurrent: current }, fetcher)).rejects.toMatchObject({ name: 'ModelInputUploadError', code, message, status: 422 });
  });
  it('discards a late response after owner/session/selection scope changes without creating a Task', async () => {
    let resolve!: (response: Response) => void; let stillCurrent = true;
    const fetcher = vi.fn((_input: RequestInfo | URL, _options?: RequestInit) => new Promise<Response>(yes => { resolve = yes; }));
    const pending = uploadModelInputSource(schema, file(), 'source', { isCurrent: () => stillCurrent }, fetcher);
    stillCurrent = false; resolve(new Response(JSON.stringify({ nativeID: 'old-owner-asset' }), { status: 201 }));
    await expect(pending).resolves.toBeNull();
    expect(fetcher).toHaveBeenCalledOnce(); expect(fetcher.mock.calls[0][0]).toBe('/configured/assets');
  });
  it('does not upload an already-obsolete or aborted selection', async () => {
    const fetcher = vi.fn(); const controller = new AbortController(); controller.abort();
    await expect(uploadModelInputSource(schema, file(), 'source', { isCurrent: () => false }, fetcher)).resolves.toBeNull();
    await expect(uploadModelInputSource(schema, file(), 'source', { isCurrent: current, signal: controller.signal }, fetcher)).resolves.toBeNull();
    expect(fetcher).not.toHaveBeenCalled();
  });
  it('rejects a response without the native asset identity', async () => {
    await expect(uploadModelInputSource(schema, file(), 'source', { isCurrent: current }, async () => new Response(JSON.stringify({ asset_id: 'legacy-only' }), { status: 201 }))).rejects.toBeInstanceOf(ModelInputUploadError);
  });
});
