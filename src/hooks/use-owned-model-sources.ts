'use client';
import { useMemo, useState } from 'react';
import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import { createAccountResourcesClient } from '@/lib/account-resources';

export function ownedModelSourcesKey(revision: string, owner: string, session: string, page: number, route: string) { return ['owned-model-sources', owner, session, revision, page, route] as const; }
/** Lists owned uploads only; a list entry is not proof that its bytes satisfy an Offering. */
export function useOwnedModelSources(owner: string, session: string, enabled: boolean) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createAccountResourcesClient(schema), [schema]);
  const scope = JSON.stringify([owner, session, schema.revision]);
  const [pagination, setPagination] = useState({ scope, page: 1 });
  const page = pagination.scope === scope ? pagination.page : 1;
  const profile = schema.runtime.input_assets.model_profile;
  const key = enabled && owner && session && profile ? ownedModelSourcesKey(schema.revision, owner, session, page, schema.runtime.http.routes.account_resources) : null;
  const pageSize = schema.administration.user_assets.limits.frontend_page_size;
  const { data, error, isLoading, mutate } = useSWR(key, () => client.getResources({ page, pageSize, limit: 1 }), { keepPreviousData: false, revalidateOnFocus: true });
  return {
    assets: data?.uploads?.data.filter(asset => asset.asset_type === profile?.asset_type) ?? [],
    page, total: data?.uploads?.total ?? 0, hasNext: page * pageSize < (data?.uploads?.total ?? 0),
    setPage: (next: number) => setPagination({ scope, page: Math.max(1, next) }),
    error, isLoading, refresh: mutate,
  };
}
