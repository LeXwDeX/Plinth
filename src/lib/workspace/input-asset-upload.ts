import { problemReason, RequestFailure, responseRequestId } from '@/lib/problem-reference';
import type { ProductSchema } from '@/lib/product-schema';

type UploadFetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

/** Upload a workspace image through the native input-asset API. */
export async function uploadWorkspaceInputAsset(
  schema: ProductSchema,
  blob: Blob,
  filename: string,
  fetcher: UploadFetcher = fetch,
): Promise<string> {
	return (await uploadWorkspaceInputAssetResult(schema, blob, filename, fetcher)).url;
}

export async function uploadWorkspaceInputAssetResult(
	schema: ProductSchema,
	blob: Blob,
	filename: string,
	fetcher: UploadFetcher = fetch,
): Promise<{ id: string; url: string }> {
  const form = new FormData();
  form.append(schema.runtime.input_assets.upload_field_name, blob, filename);
  const response = await fetcher(schema.runtime.http.routes.assets, { method: 'POST', body: form });
  if (!response.ok) {
    // #274: the failed request's ID travels with the error, so the toast and the client report can name it.
    const body: unknown = await response.json().catch(() => null);
    // #292: a user-facing reason (#288) is kept, so the composer can say why its picture was refused.
    throw new RequestFailure(`asset upload ${response.status}`, responseRequestId(response, body), problemReason(body));
  }

  const payload: unknown = await response.json();
  const identifierField = schema.runtime.input_assets.api.fields.id;
  const assetID = payload && typeof payload === 'object' && identifierField in payload
    ? (payload as Record<string, unknown>)[identifierField] : undefined;
  if (typeof assetID !== 'string' || assetID === '') {
    throw new Error('asset upload returned no ID');
  }
	return { id: assetID, url: schema.runtime.http.routes.asset_item.replace('{assetID}', encodeURIComponent(assetID)) };
}
