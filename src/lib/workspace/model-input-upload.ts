import type { HarnessSource, HarnessSourceView } from '@/lib/harness';
import type { ProductSchema } from '@/lib/product-schema';

type ModelProfile = NonNullable<ProductSchema['runtime']['input_assets']['model_profile']>;
type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
export class ModelInputUploadError extends Error {
  constructor(message: string, public readonly code: string, public readonly status = 0) { super(message); this.name = 'ModelInputUploadError'; }
}
export function modelInputAccept(profile: ModelProfile) { return [...profile.extensions, ...profile.media_types].join(','); }

/** Uploads unchanged bytes through the native owner asset API; it never submits work. */
export async function uploadModelInputSource(schema: ProductSchema, file: File, parameter: string, scope: { isCurrent: () => boolean; signal?: AbortSignal }, fetcher: Fetcher = fetch): Promise<{ source: HarnessSource; view: HarnessSourceView; url: string } | null> {
  const current = () => scope.isCurrent() && !scope.signal?.aborted;
  if (!current()) return null;
  const input = schema.runtime.input_assets;
  const profile = input.model_profile;
  if (!profile) throw new ModelInputUploadError('ui.harness.upload.unavailable', 'unavailable');
  if (!profile.extensions.some(extension => file.name.toLowerCase().endsWith(extension.toLowerCase()))) throw new ModelInputUploadError('ui.harness.upload.format_unsupported', 'extension');
  if (file.size > Math.min(input.upload_max_bytes, profile.limits.max_bytes)) throw new ModelInputUploadError('ui.harness.upload.file_too_large', 'size');
  const body = new FormData();
  body.append(input.upload_field_name, new Blob([file], { type: profile.upload_media_type }), file.name);
  const response = await fetcher(schema.runtime.http.routes.assets, { method: 'POST', body, signal: scope.signal });
  if (!current()) return null;
  const result = await response.json().catch(() => null) as Record<string, unknown> | null;
  if (!current()) return null;
  if (!response.ok) {
    const error = result?.error && typeof result.error === 'object' ? result.error as Record<string, unknown> : result;
    const message = typeof error?.message === 'string' ? error.message : typeof error?.detail === 'string' ? error.detail : 'ui.harness.upload.failed';
    throw new ModelInputUploadError(message, typeof error?.code === 'string' ? error.code : 'upload_failed', response.status);
  }
  const assetId = result?.[input.api.fields.id];
  if (typeof assetId !== 'string' || !assetId) throw new ModelInputUploadError('ui.harness.upload.failed', 'invalid_response', response.status);
  return {
    source: { kind: 'input_asset', parameter, asset_id: assetId },
    view: { kind: 'input_asset', asset_id: assetId, original_filename: file.name, media_type: profile.upload_media_type, size: file.size, profile_id: profile.id, state: 'ready' },
    url: schema.runtime.http.routes.asset_item.replace('{assetID}', encodeURIComponent(assetId)),
  };
}
