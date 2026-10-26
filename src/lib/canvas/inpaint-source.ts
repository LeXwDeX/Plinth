import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import type { ProductSchema } from '@/lib/product-schema';

/** The exact mask a finished inpaint result was made with: a platform input asset and its pixel size. */
export interface InpaintSource {
  maskAssetId: string;
  width: number;
  height: number;
}

/** Platform input-asset IDs are `asset_` plus 32 lowercase hex characters (generation.randomIDs.AssetID). */
const MASK_ASSET_ID = /^asset_[0-9a-f]{32}$/;

export function isMaskAssetId(value: unknown): value is string {
  return typeof value === 'string' && MASK_ASSET_ID.test(value);
}

export function isInpaintSource(value: unknown): value is InpaintSource {
  if (!value || typeof value !== 'object') return false;
  const source = value as Partial<InpaintSource>;
  return isMaskAssetId(source.maskAssetId)
    && Number.isSafeInteger(source.width) && (source.width ?? 0) > 0
    && Number.isSafeInteger(source.height) && (source.height ?? 0) > 0;
}

/** The same-origin asset route declared by the Product Schema; empty when the source or route is invalid. */
export function inpaintMaskURL(schema: ProductSchema, source: InpaintSource): string {
  if (!isInpaintSource(source)) return '';
  const route = schema.runtime?.http?.routes?.asset_item;
  if (!route || !route.includes('{assetID}')) return '';
  return route.replace('{assetID}', encodeURIComponent(source.maskAssetId));
}

/** The asset ID from this Schema's asset route; anything else (external URL, data URI, other route) is null. */
export function maskAssetIdFromURL(schema: ProductSchema, url: string): string | null {
  const safe = safeAssetURL(url);
  const route = schema.runtime?.http?.routes?.asset_item;
  if (!safe || !route) return null;
  const marker = route.indexOf('{assetID}');
  if (marker < 0) return null;
  const prefix = route.slice(0, marker);
  const suffix = route.slice(marker + '{assetID}'.length);
  const path = safe.split(/[?#]/, 1)[0];
  if (!path.startsWith(prefix) || !path.endsWith(suffix)) return null;
  const encoded = path.slice(prefix.length, path.length - suffix.length);
  if (!encoded || encoded.includes('/')) return null;
  try {
    const id = decodeURIComponent(encoded);
    return isMaskAssetId(id) ? id : null;
  } catch {
    return null;
  }
}
