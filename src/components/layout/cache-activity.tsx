'use client';

import { HardDrive } from 'lucide-react';
import useSWR from 'swr';
import { Surface } from '@/components/ui/surface';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useProductSchema } from '@/components/product-schema-provider';
import {
  schemaCountMessage,
  schemaMessage,
  schemaText,
  type ProductSchema,
  valueLabel,
} from '@/lib/product-schema';
import {
  cacheStatusRoute,
  readCacheStatus,
  type CacheActiveItem,
} from '@/lib/cache-status-schema';

const cacheFetcher = (url: string) => fetch(url).then(r => r.ok ? r.json() : null);

function formatAssetKind(
  schema: ProductSchema,
  assetKind: string | undefined,
): string {
  if (!assetKind) return schemaText(schema, 'ui.header.unknown_asset');
  const exact = valueLabel(schema.content.asset_kinds, assetKind, '');
  if (exact) return exact;
  const separator = assetKind.indexOf(':');
  if (separator === -1) return assetKind;
  const prefix = assetKind.slice(0, separator);
  const pattern = schema.content.asset_kind_patterns.find(
    (item) => item.prefix === prefix,
  );
  if (!pattern) return assetKind;
  const rawSuffix = assetKind.slice(separator + 1);
  const suffix = pattern.uppercase_suffix
    ? rawSuffix.toUpperCase()
    : rawSuffix;
  return schemaMessage(schema, pattern.message_key, { suffix });
}

function formatCacheItem(schema: ProductSchema, item: CacheActiveItem): string {
  const capability = item.taskType
    ? schema.capabilities.find((entry) => entry.id === item.taskType)?.label_cn ??
      item.taskType
    : schemaText(schema, 'ui.header.unknown_task');
  const task = item.taskId
    ? schemaMessage(schema, 'ui.header.cache_task_id', {
        id: item.taskId.slice(0, 8),
      })
    : schemaText(schema, 'ui.header.unknown_task_id');
  return schemaMessage(schema, 'ui.header.cache_item', {
    capability,
    task,
    asset: formatAssetKind(schema, item.assetKind),
  });
}

function buildCacheTooltip(
  schema: ProductSchema,
  items: CacheActiveItem[] | undefined,
  total: number,
): string {
  if (!items || items.length === 0) {
    return schemaCountMessage(schema, 'ui.header.cache_tooltip', total, { count: total });
  }
  return schemaCountMessage(schema, 'ui.header.cache_tooltip_details', total, {
    count: total,
    items: items.map((item) => formatCacheItem(schema, item)).join('\n'),
  });
}

/** 管理区和我的资源页在顶栏显示服务端正在缓存的产物数量；没有缓存任务时不占位。 */
export function CacheActivityIndicator() {
  const { schema } = useProductSchema();
  const { data: cachePayload } = useSWR(cacheStatusRoute(schema), cacheFetcher, {
    refreshInterval: schema.runtime.frontend.cache_status_refresh_milliseconds,
    revalidateOnFocus: false,
  });
  const cacheData = readCacheStatus(schema, cachePayload);
  if (!(cacheData.activeDownloads > 0)) return null;
  return (
    <HintTooltip
      name={buildCacheTooltip(
        schema,
        cacheData.activeItems as CacheActiveItem[] | undefined,
        cacheData.activeDownloads,
      )}
    >
      <Surface variant="info" padding="sm" radius="xl" className="flex items-center gap-1.5 text-xs">
        <HardDrive className="h-3.5 w-3.5 animate-pulse" />
        <span>
          {schemaCountMessage(schema, 'ui.header.cache_count', cacheData.activeDownloads, {
            count: cacheData.activeDownloads,
          })}
        </span>
      </Surface>
    </HintTooltip>
  );
}
