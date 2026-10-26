import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { readCacheStatus } from '@/lib/cache-status-schema';

describe('Schema-driven cache status response', () => {
  it('reads route and wire fields from a mutated Product Schema', () => {
    const schema = structuredClone(
      defaultCatalog.application,
    ) as ProductSchema;
    schema.runtime.http.legacy_routes.cache_status = '/api/schema/cache';
    schema.runtime.cache.fields.active_downloads = 'running';
    schema.runtime.cache.fields.active_items = 'entries';
    schema.runtime.cache.fields.filename = 'file';
    schema.runtime.cache.fields.elapsed = 'age';
    schema.runtime.cache.fields.task_id = 'job';
    schema.runtime.cache.fields.task_type = 'kind';
    schema.runtime.cache.fields.provider_id = 'source';
    schema.runtime.cache.fields.asset_kind = 'asset';
    schema.runtime.cache.fields.total_cached_files = 'files';
    schema.runtime.cache.fields.total_cache_size_mb = 'size';

    expect(readCacheStatus(schema, {
      running: 1,
      entries: [{
        file: 'preview.png',
        age: 2,
        job: 'task-a',
        kind: 'text-to-image',
        source: 'openai',
        asset: 'image',
      }],
      files: 3,
      size: 1.5,
    })).toEqual({
      route: '/api/schema/cache',
      activeDownloads: 1,
      activeItems: [{
        filename: 'preview.png',
        elapsed: 2,
        taskId: 'task-a',
        taskType: 'text-to-image',
        providerId: 'openai',
        assetKind: 'image',
      }],
      totalCachedFiles: 3,
      totalCacheSizeMB: 1.5,
    });
  });

  it('returns a safe empty view for malformed payloads', () => {
    const schema = defaultCatalog.application as ProductSchema;
    expect(readCacheStatus(schema, null)).toEqual({
      route: '/api/cache-status',
      activeDownloads: 0,
      activeItems: [],
      totalCachedFiles: 0,
      totalCacheSizeMB: 0,
    });
  });
});
