import type { AdministrationProductSchema } from '@/lib/product-schema';
import { parsePriceTotals, type PriceTotal } from '@/lib/pricing';

export interface AdministrationUsageRecord {
  userId: string;
  userName: string;
  userEmail: string;
  totalTasks: number;
  succeededTasks: number;
  failedTasks: number;
  estimatedCredits: number;
  priceTotals?: PriceTotal[];
  lastActiveAt: string;
  byType: Record<string, number>;
}

/** The estimate of every listed user together (#327), stated the way a user's row states its own. */
export interface AdministrationUsageTotal {
  estimatedCredits: number;
  priceTotals?: PriceTotal[];
}

export interface AdministrationUsageOverview {
  users: AdministrationUsageRecord[];
  /** null when the server sent no total. */
  total: AdministrationUsageTotal | null;
}

export interface AdministrationUsageQuery {
  days?: number;
  startDate?: string;
  endDate?: string;
}

export interface AdministrationBalanceSnapshot {
  balance: number;
  capturedAt: string;
}

export interface AdministrationBalanceSummary {
  currentBalance: number | null;
  priceMode?: 'native';
  nativeBalance?: PriceTotal | null;
  history: AdministrationBalanceSnapshot[];
}

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface AdministrationUsageBrowserClientConfig {
  schema: AdministrationProductSchema['usage'];
  routes: {
    usage: string;
    balance: string;
  };
  fetcher?: Fetcher;
}

export interface AdministrationUsageBrowserClient {
  usageURL(query: AdministrationUsageQuery): string;
  listUsage(
    query: AdministrationUsageQuery,
  ): Promise<AdministrationUsageRecord[]>;
  usageOverview(
    query: AdministrationUsageQuery,
  ): Promise<AdministrationUsageOverview>;
  getBalance(): Promise<AdministrationBalanceSummary>;
}

export class AdministrationUsageBrowserError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'AdministrationUsageBrowserError';
  }
}

export function createAdministrationUsageBrowserClient(
  config: AdministrationUsageBrowserClientConfig,
): AdministrationUsageBrowserClient {
  const fetcher = config.fetcher ?? fetch;
  const fields = config.schema.api.fields;
  const balanceFields = config.schema.api.balance_fields;
  const queryFields = config.schema.api.query;

  const request = async (
    route: string,
  ): Promise<Record<string, unknown>> => {
    const response = await fetcher(route, undefined);
    const payload = asRecord(await response.json().catch(() => ({})));
    if (!response.ok) {
      throw new AdministrationUsageBrowserError(
        response.status,
        optionalString(payload[fields.error]) ||
          config.schema.messages.internal_error,
      );
    }
    return payload;
  };

  const usageURL = (query: AdministrationUsageQuery): string => {
    const parameters = new URLSearchParams();
    const startDate = query.startDate?.trim() ?? '';
    const endDate = query.endDate?.trim() ?? '';
    if (startDate !== '' && endDate !== '') {
      parameters.set(queryFields.start_date, startDate);
      parameters.set(queryFields.end_date, endDate);
    } else if (query.days !== undefined) {
      parameters.set(queryFields.days, String(query.days));
    }
    const suffix = parameters.size > 0
      ? `?${parameters.toString()}`
      : '';
    return `${config.routes.usage}${suffix}`;
  };

  const usageRecord = (value: unknown): AdministrationUsageRecord => {
    const wire = asRecord(value);
    const priceTotals = Object.hasOwn(wire, 'price_totals') ? parsePriceTotals(wire.price_totals) : undefined;
    return {
      userId: optionalString(wire[fields.user_id]),
      userName: optionalString(wire[fields.user_name]),
      userEmail: optionalString(wire[fields.user_email]),
      totalTasks: numberValue(wire[fields.total_tasks]),
      succeededTasks: numberValue(wire[fields.succeeded_tasks]),
      failedTasks: numberValue(wire[fields.failed_tasks]),
      ...(priceTotals === undefined ? { estimatedCredits: numberValue(wire[fields.estimated_credits]) } : { estimatedCredits: 0, priceTotals: priceTotals ?? [] }),
      lastActiveAt: optionalString(wire[fields.last_active_at]),
      byType: numberRecord(wire[fields.by_type]),
    };
  };

  const usageTotal = (value: unknown): AdministrationUsageTotal | null => {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
    const wire = asRecord(value);
    if (Object.hasOwn(wire, 'price_totals')) {
      return { estimatedCredits: 0, priceTotals: parsePriceTotals(wire.price_totals) ?? [] };
    }
    return { estimatedCredits: numberValue(wire[fields.estimated_credits]) };
  };

  const usageOverview = async (query: AdministrationUsageQuery): Promise<AdministrationUsageOverview> => {
    const payload = await request(usageURL(query));
    const records = payload[fields.data];
    return {
      users: Array.isArray(records) ? records.map(usageRecord) : [],
      total: usageTotal(payload[fields.total]),
    };
  };

  return {
    usageURL,
    async listUsage(query) {
      return (await usageOverview(query)).users;
    },
    usageOverview,
    async getBalance() {
      const payload = await request(config.routes.balance);
      const history = payload[balanceFields.history];
      const nativeBalance = payload.native_balance && typeof payload.native_balance === 'object' && !Array.isArray(payload.native_balance)
        ? parsePriceTotals([payload.native_balance])?.[0] ?? null
        : null;
      if (payload.price_mode === 'native') {
        return { currentBalance: null, priceMode: 'native' as const, nativeBalance, history: [] };
      }
      return {
        currentBalance: nullableNumber(
          payload[balanceFields.current_balance],
        ),
        history: Array.isArray(history)
          ? history.map((value) => {
            const wire = asRecord(value);
            return {
              balance: numberValue(wire[balanceFields.balance]),
              capturedAt: optionalString(
                wire[balanceFields.captured_at],
              ),
            };
          })
          : [],
      };
    },
  };
}

function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function optionalString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function nullableNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function numberRecord(value: unknown): Record<string, number> {
  const source = asRecord(value);
  const result: Record<string, number> = {};
  for (const [key, item] of Object.entries(source)) {
    result[key] = numberValue(item);
  }
  return result;
}
