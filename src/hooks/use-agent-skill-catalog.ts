'use client';
import { useEffect, useMemo, useSyncExternalStore } from 'react';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { AgentConfigurationResource } from '@/lib/agent-configuration-resource';
import type { ProductSchema } from '@/lib/product-schema';
import { useAuth } from '@/hooks/use-auth';
import { useRetainedOnRefresh } from '@/hooks/use-retained-on-refresh';

export function useAgentSkillCatalog(schema: ProductSchema, configurationRevision?: string, refreshVersion = 0) {
  const { user } = useAuth();
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const resource = useMemo(() => new AgentConfigurationResource(signal => client.listSkills(signal)), [client, configurationRevision, refreshVersion, user?.id]);
  const live = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  // A refresh keeps the current list on screen until the new one arrives (#238). Deleting a Skill also
  // changes the configuration revision, so only a different user starts from an empty list.
  const snapshot = useRetainedOnRefresh(live, user?.id ?? '');
  useEffect(() => { if (!user?.id) return; resource.start(); return () => resource.stop(); }, [resource, user?.id]);
  return { catalog: snapshot.data, failed: snapshot.failed, loading: snapshot.loading };
}
