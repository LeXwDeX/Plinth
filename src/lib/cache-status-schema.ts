import type { ProductSchema } from '@/lib/product-schema';

export interface CacheActiveItem {
  filename: string;
  elapsed: number;
  taskId?: string;
  taskType?: string;
  providerId?: string;
  assetKind?: string;
}

export interface CacheStatusView {
  route: string;
  activeDownloads: number;
  activeItems: CacheActiveItem[];
  totalCachedFiles: number;
  totalCacheSizeMB: number;
}

export function cacheStatusRoute(schema: ProductSchema): string {
  return schema.runtime.http.legacy_routes.cache_status;
}

export function readCacheStatus(
  schema: ProductSchema,
  payload: unknown,
): CacheStatusView {
  const fields = schema.runtime.cache.fields;
  const record = asRecord(payload);
  const rawItems = record?.[fields.active_items];
  return {
    route: cacheStatusRoute(schema),
    activeDownloads: numberValue(record?.[fields.active_downloads]),
    activeItems: Array.isArray(rawItems)
      ? rawItems.flatMap((rawItem) => {
          const item = asRecord(rawItem);
          if (!item) return [];
          return [{
            filename: stringValue(item[fields.filename]),
            elapsed: numberValue(item[fields.elapsed]),
            taskId: optionalString(item[fields.task_id]),
            taskType: optionalString(item[fields.task_type]),
            providerId: optionalString(item[fields.provider_id]),
            assetKind: optionalString(item[fields.asset_kind]),
          }];
        })
      : [],
    totalCachedFiles: numberValue(record?.[fields.total_cached_files]),
    totalCacheSizeMB: numberValue(record?.[fields.total_cache_size_mb]),
  };
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value ? value : undefined;
}
