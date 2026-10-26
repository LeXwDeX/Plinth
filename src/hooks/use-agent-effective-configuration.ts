'use client';
import { useEffect, useMemo, useSyncExternalStore } from 'react';
import { createAgentConfigurationClient } from '@/lib/agent-configuration';
import { AgentConfigurationResource } from '@/lib/agent-configuration-resource';
import type { ProductSchema } from '@/lib/product-schema';
import { useAuth } from '@/hooks/use-auth';
import { useRetainedOnRefresh } from '@/hooks/use-retained-on-refresh';

export function useAgentEffectiveConfiguration(schema: ProductSchema, sessionId: string | null, domain: string, modelId: string | null, refreshVersion = 0) {
  const { user } = useAuth();
  const client = useMemo(() => createAgentConfigurationClient(schema), [schema]);
  const resource = useMemo(() => new AgentConfigurationResource(signal => client.effective(domain, modelId, sessionId, signal)), [client, domain, modelId, sessionId, refreshVersion, user?.id]);
  const live = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  // A refresh (refreshVersion) keeps the current configuration until the new one arrives (#238).
  const snapshot = useRetainedOnRefresh(live, `${user?.id ?? ''}|${sessionId ?? ''}|${domain}|${modelId ?? ''}`);
  useEffect(() => {
    if (!sessionId || !user?.id) return;
    resource.start(); return () => resource.stop();
  }, [resource, sessionId, user?.id]);
  return { ...snapshot, loading: Boolean(sessionId) && snapshot.loading };
}
