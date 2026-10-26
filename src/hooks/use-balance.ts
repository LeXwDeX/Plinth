'use client';

import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from './use-auth';
import type { PriceTotal } from '@/lib/pricing';
import { parsePriceTotals } from '@/lib/pricing';

const fetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    return r.json();
  });

export interface ProviderBalanceEstimateView {
  capability: string;
  /** 能力显示名（来自 Product Schema capabilities） */
  label: string;
  credits: number;
  /** 当前余额按该消耗可支撑的大致次数 */
  times: number;
}

export interface ProviderBalanceAccountView {
  provider: string;
  label: string;
  balance?: number;
  priceMode?: 'native';
  nativeBalance?: PriceTotal | null;
  error?: string;
  estimates: ProviderBalanceEstimateView[];
}

interface WireAccount {
  provider?: unknown;
  error?: unknown;
  [key: string]: unknown;
}

/**
 * Schema 驱动的多 Provider 账户余额：
 * 契约（字段名/账户清单/消耗估算）全部来自 runtime.http.legacy_provider_balance。
 */
export function projectProviderBalances(
  response: unknown,
  contract: {
    accounts_field: string;
    provider_field: string;
    balance_field: string;
    error_field: string;
    accounts: Array<{
      account_id: string;
      label?: string;
      consumption_estimates?: Array<{ capability: string; credits: number }>;
    }>;
  },
  capabilityLabels: Record<string, string>,
): ProviderBalanceAccountView[] {
  if (
    response === null ||
    typeof response !== 'object' ||
    Array.isArray(response)
  ) return [];
  const wireAccounts = (response as Record<string, unknown>)[
    contract.accounts_field
  ];
  if (!Array.isArray(wireAccounts)) return [];
  const byProvider = new Map<string, WireAccount>();
  for (const raw of wireAccounts) {
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) continue;
    const record = raw as WireAccount;
    const provider =
      typeof record[contract.provider_field] === 'string'
        ? (record[contract.provider_field] as string)
        : '';
    if (provider) byProvider.set(provider, record);
  }

  return contract.accounts.map((configured) => {
    const wire = byProvider.get(configured.account_id);
    const rawBalance = wire?.[contract.balance_field];
    const balance =
      typeof rawBalance === 'number' && Number.isFinite(rawBalance)
        ? rawBalance
        : undefined;
    const rawError = wire?.[contract.error_field];
    const native = wire?.price_mode === 'native';
    const nativeBalanceRaw = wire?.native_balance;
    const nativeBalance = parsePriceTotals([nativeBalanceRaw])?.[0] ?? null;
    const estimates: ProviderBalanceEstimateView[] = (
      native ? [] : configured.consumption_estimates ?? []
    ).map((estimate) => ({
      capability: estimate.capability,
      label:
        capabilityLabels[estimate.capability] ?? estimate.capability,
      credits: estimate.credits,
      times:
        balance !== undefined && estimate.credits > 0
          ? Math.floor(balance / estimate.credits)
          : 0,
    }));
    return {
      provider: configured.account_id,
      label: configured.label || configured.account_id,
      ...(!native && balance !== undefined ? { balance } : {}),
      ...(native ? { priceMode: 'native' as const, nativeBalance } : {}),
      ...(typeof rawError === 'string' ? { error: rawError } : {}),
      estimates,
    };
  });
}

export type ProviderBalanceContract = {
  accounts_field: string;
  provider_field: string;
  balance_field: string;
  error_field: string;
  accounts: Array<{
    account_id: string;
    label?: string;
    consumption_estimates?: Array<{ capability: string; credits: number }>;
  }>;
};

export function useBalance() {
  const { schema } = useProductSchema();
  const { user } = useAuth();
  const balanceRoute = schema.runtime.http.legacy_routes.provider_balance;
  // 未认证时不轮询，避免持续产生 401 请求
  const { data, error, isLoading, mutate } = useSWR<unknown>(
    user?.id ? [balanceRoute, user.id] as const : null,
    ([route]) => fetcher(route),
    {
      refreshInterval:
        schema.runtime.frontend.balance_refresh_milliseconds,
    },
  );
  const capabilityLabels: Record<string, string> = {};
  for (const capability of schema.capabilities) {
    capabilityLabels[capability.id] = capability.label;
  }
  return {
    accounts: projectProviderBalances(
      data,
      schema.runtime.http.legacy_provider_balance,
      capabilityLabels,
    ),
    error,
    isLoading,
    refresh: mutate,
  };
}
