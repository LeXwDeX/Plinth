/** Browser resource URLs must stay on this origin. Legacy upstream URLs are unusable here. */
import type { ProductSchema } from '@/lib/product-schema';

function containsRemoteURL(value: string): boolean {
  let decoded = value;
  for (let depth = 0; depth < 4; depth++) {
    if (/https?:\/\/|https?:\\|\/\//i.test(decoded)) return true;
    try {
      const next = decodeURIComponent(decoded);
      if (next === decoded) break;
      decoded = next;
    } catch {
      break;
    }
  }
  return false;
}

// #290: a chat with twenty picture turns asks for the same addresses on every render (cards, strip, chips);
// the check decodes and parses each one, so its answers are remembered. Bounded: cleared when full, and long
// values (composer data: URLs carry a whole picture as base64) are checked every time instead of being kept as keys.
const MEMO_LIMIT = 4096;
const MEMO_KEY_MAX = 2048;
const checked = new Map<string, string>();

/** Test hook: how many address answers are held. */
export function rememberedAddressCount(): number {
  return checked.size;
}

function remembered(memo: Map<string, string>, key: string, compute: () => string): string {
  if (key.length > MEMO_KEY_MAX) return compute();
  const known = memo.get(key);
  if (known !== undefined) return known;
  const value = compute();
  if (memo.size >= MEMO_LIMIT) memo.clear();
  memo.set(key, value);
  return value;
}

/** Empty result means no browser request or navigation may be made. */
export function safeAssetURL(value: unknown): string {
  if (typeof value !== 'string' || !value) return '';
  // A data: URL is never a resource address. Old composer messages carry whole pictures this way (#292), so it is
  // refused before any per-character work: trimming, parsing or decoding a 17 MB string cost ~0.5 s per send (#291).
  if (value.startsWith('data:')) return '';
  const origin = typeof window !== 'undefined' ? window.location.origin : undefined;
  if (value.length > MEMO_KEY_MAX) return checkAssetURL(value, origin);
  return remembered(checked, `${origin ?? ''}\u0000${value}`, () => checkAssetURL(value, origin));
}

function checkAssetURL(value: string, origin: string | undefined): string {
  if (value !== value.trim() || /[\u0000-\u001f\\]/.test(value)) return '';
  if (value.startsWith('blob:')) {
    if (!origin) return '';
    try {
      const parsed = new URL(value);
      const objectID = parsed.pathname.slice(origin.length + 1);
      return parsed.origin === origin && !parsed.search && !parsed.hash && /^[0-9a-f-]{16,}$/i.test(objectID) ? value : '';
    } catch { return ''; }
  }
  if (!value.startsWith('/') && !origin) return '';
  if (value.startsWith('//')) return '';
  try {
    const parsed = new URL(value, origin ?? 'http://localhost');
    if (!['http:', 'https:'].includes(parsed.protocol)) return '';
    if (origin && parsed.origin !== origin) return '';
    if (parsed.username || parsed.password) return '';
    if (!origin && !value.startsWith('/')) return '';
    if (containsRemoteURL(parsed.pathname + parsed.search + parsed.hash)) return '';
    // This legacy endpoint accepts an upstream URL in the browser query.
    if (parsed.pathname === '/api/meshy/proxy-asset') return '';
    return parsed.pathname + parsed.search + parsed.hash;
  } catch { return ''; }
}

export interface AssetURLRewriter {
  proxyAssetUrl: (url: string, taskId?: string, size?: 'thumb') => string;
  /**
   * #290: the address of a picture's downscaled preview, for views that show it small (chat result cards, the
   * version strip, chips). Only task outputs and input assets have one; any other address is kept. Never use
   * it for what is downloaded, pasted, put on the board or opened large: those need the original.
   */
  previewAssetUrl: (url: string) => string;
  rewriteAssetUrls: <T>(data: T) => T;
}

/** A route template such as `/api/v1/tasks/{taskID}/assets/{assetID}` as a whole-path pattern. */
function routePattern(template: string | undefined): RegExp | null {
  if (!template) return null;
  const parts = template.split(/\{[^}]+\}/).map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  return new RegExp(`^${parts.join('[^/?#]+')}$`);
}

export function createAssetURLRewriter(schema: ProductSchema): AssetURLRewriter {
  const preview = schema.runtime?.asset_delivery?.asset_preview;
  // Task outputs and input assets (uploads, board snapshots) both answer the preview query.
  const routes = [schema.runtime?.http?.routes?.task_asset, schema.runtime?.http?.routes?.asset_item]
    .map(routePattern)
    .filter((pattern): pattern is RegExp => pattern !== null);
  const previews = new Map<string, string>();
  const previewOf = (url: string): string => {
    const safe = safeAssetURL(url);
    if (!safe || !preview?.query || !preview.request_value || safe.startsWith('blob:')) return safe;
    const parsed = new URL(safe, 'http://local.invalid');
    if (!routes.some((pattern) => pattern.test(parsed.pathname)) || parsed.searchParams.has(preview.query)) return safe;
    parsed.searchParams.set(preview.query, preview.request_value);
    return parsed.pathname + parsed.search + parsed.hash;
  };
  return {
    proxyAssetUrl: (url) => safeAssetURL(url),
    previewAssetUrl: (url) => (typeof url === 'string' && url ? remembered(previews, url, () => previewOf(url)) : safeAssetURL(url)),
    rewriteAssetUrls: <T>(data: T) => data,
  };
}

/** Composer attachments preview as local raster data URIs; everything else must pass safeAssetURL. */
export function safePreviewURL(value: unknown): string {
  // Only the header is checked: a data: image cannot make a request, and scanning a whole picture's base64 on every
  // render is what made sends slow in sessions with old composer pictures (#291 acceptance).
  if (typeof value === 'string' && /^data:image\/(png|jpeg|webp|gif);base64,[A-Za-z0-9+/]/.test(value.slice(0, 64))) return value;
  return safeAssetURL(value);
}
