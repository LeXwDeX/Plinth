import type { ProductSchema } from '@/lib/product-schema';
import { parsePriceTotals, type PriceTotal } from '@/lib/pricing';

export interface AccountUsage {
  user_id: string;
  total_tasks: number;
  succeeded_tasks: number;
  failed_tasks: number;
  estimated_credits?: number;
  price_totals?: PriceTotal[];
  by_type?: Record<string, number>;
  last_active_at: string | null;
}

export interface AccountAsset {
  id: string;
  asset_type: string;
  original_filename: string;
  media_type: string;
  size: number;
  sha256: string;
  created_at: string;
  url: string;
}

export interface AccountTaskAsset {
  id: string;
  role: string;
  variant?: string;
  media_type: string;
  size: number;
  url: string;
}

export interface AccountTask {
  id: string;
  capability: string;
  state: string;
  created_at: string;
  assets: AccountTaskAsset[];
}

export interface AccountResources {
  tasks: AccountTask[];
  next_cursor: string;
  uploads: {
    data: AccountAsset[];
    total: number;
    page: number;
    page_size: number;
  };
}

export interface AccountResourceQuery {
  cursor?: string;
  limit?: number;
  page?: number;
  pageSize?: number;
}

export interface AccountUsageQuery {
  days?: number;
  startDate?: string;
  endDate?: string;
}

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export interface AccountResourcesClient {
  usageURL(query: AccountUsageQuery): string;
  resourcesURL(query: AccountResourceQuery): string;
  getUsage(query: AccountUsageQuery, signal?: AbortSignal): Promise<AccountUsage>;
  getResources(query: AccountResourceQuery, signal?: AbortSignal): Promise<AccountResources>;
}

export function createAccountResourcesClient(
  schema: ProductSchema,
  fetcher: Fetcher = fetch,
): AccountResourcesClient {
  const usageRoute = schema.runtime.http.routes.account_usage;
  const resourcesRoute = schema.runtime.http.routes.account_resources;
  const makeURL = (route: string, values: Record<string, string | number | undefined>) => {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(values)) {
      if (value !== undefined && value !== '') query.set(key, String(value));
    }
    return query.size > 0 ? `${route}?${query.toString()}` : route;
  };
  const read = async <T>(url: string, signal?: AbortSignal): Promise<T> => {
    const response = await fetcher(url, { method: 'GET', signal });
    const body: unknown = await response.json().catch(() => null);
    if (!response.ok) {
      const detail = body && typeof body === 'object' && 'detail' in body && typeof body.detail === 'string'
        ? body.detail
        : 'error.account_resource_request_failed';
      throw new Error(detail);
    }
    return body as T;
  };
  return {
    usageURL: (query) => makeURL(usageRoute, {
      days: query.days,
      start_date: query.startDate,
      end_date: query.endDate,
    }),
    resourcesURL: (query) => makeURL(resourcesRoute, {
      cursor: query.cursor,
      limit: query.limit,
      page: query.page,
      page_size: query.pageSize,
    }),
    async getUsage(query, signal) {
      const raw = await read<Record<string, unknown>>(makeURL(usageRoute, {
        days: query.days,
        start_date: query.startDate,
        end_date: query.endDate,
      }), signal);
      const priceTotals = Object.hasOwn(raw, 'price_totals') ? parsePriceTotals(raw.price_totals) : undefined;
      if (priceTotals !== undefined) return { ...raw, estimated_credits: undefined, by_type: undefined, price_totals: priceTotals ?? [] } as unknown as AccountUsage;
      return raw as unknown as AccountUsage;
    },
    getResources: (query, signal) => read(makeURL(resourcesRoute, {
      cursor: query.cursor,
      limit: query.limit,
      page: query.page,
      page_size: query.pageSize,
    }), signal),
  };
}
