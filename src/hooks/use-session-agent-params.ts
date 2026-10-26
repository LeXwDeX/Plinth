'use client';

import { useMemo, useSyncExternalStore } from 'react';
import useSWR, { mutate as mutateGlobal } from 'swr';
import { toast } from 'sonner';
import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from '@/hooks/use-auth';
import { createAgentHarnessClient, mergeAgentParams, type AgentParamValues, type AgentParameterProjection, type AgentParameterState } from '@/lib/agent-params';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import type { ParamSchema } from '@/lib/providers/types';

export function sessionAgentParamsKey(schema: ProductSchema, ownerId: string, sessionId: string, offeringId = '') {
  return ['workspace-agent-params', ownerId, sessionId, offeringId, schema.revision, schema.runtime.http.routes.agent_params];
}

function client(schema: ProductSchema) {
  return createAgentHarnessClient({
    routes: {
      params: schema.runtime.http.routes.agent_params,
      bind: schema.runtime.http.routes.agent_bind,
      consent: schema.runtime.http.routes.agent_consent,
    },
  });
}

/*
 * Unsaved edits are shared by every component that reads the same owner/session/Offering/Schema scope, and
 * each scope saves its own edits (#246 note 96916). Before, each hook instance kept its edits in React state
 * and dropped them from saving when its Offering changed inside the save delay or during a save (the timer was
 * cleared and the request aborted), while still showing them when it came back to that Offering: the 参数 panel
 * (one instance) could show a size the server never received while the canvas board (another instance) showed
 * the aligned one, so the board saw no mismatch and no notice appeared. Now an edit is saved for the Offering it
 * was made for whatever the components do next, every reader sees the same edits at once, and a failed save
 * drops the edits it carried so no reader keeps showing a value the server does not have.
 */
interface PendingScope {
  sessionId: string;
  offeringId?: string;
  key: ReturnType<typeof sessionAgentParamsKey>;
  schema: ProductSchema;
  values: AgentParamValues;
  timer: ReturnType<typeof setTimeout> | null;
  writes: Promise<unknown>;
}

const EMPTY: AgentParamValues = Object.freeze({}) as AgentParamValues;
const pendingScopes = new Map<string, PendingScope>();
const failedScopes = new Map<string, string>();
const listeners = new Set<() => void>();

function notify() {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/** Saves the scope's edits made so far, after any save already running for it. Resolves once they are saved. */
function savePending(scope: string): Promise<void> {
  const entry = pendingScopes.get(scope);
  if (!entry) return Promise.resolve();
  if (entry.timer) { clearTimeout(entry.timer); entry.timer = null; }
  const snapshot = { values: entry.values };
  const patch = { ...snapshot.values };
  if (Object.keys(patch).length === 0) return entry.writes.catch(() => undefined).then(() => undefined);
  const run = async () => {
    try {
      const changed = Object.fromEntries(Object.entries(patch).map(([name, value]) => [name, value ?? null]));
      const merged = await client(entry.schema).putParamsWithState(entry.sessionId, changed, undefined, entry.offeringId);
      // The answer belongs to this exact owner/session/Offering/revision only, whichever scope is shown now.
      await mutateGlobal(entry.key, merged, { revalidate: false });
      failedScopes.delete(scope);
    } catch (error) {
      failedScopes.set(scope, schemaText(entry.schema, 'ui.workspace.harness.save_failed'));
      toast.error(schemaText(entry.schema, 'ui.workspace.harness.save_failed'));
      throw error;
    } finally {
      // Saved or not, the edits this request carried are no longer local: on success the server has them, on
      // failure showing them would claim a value the server does not have. Newer edits of the same names stay.
      if (pendingScopes.get(scope) === entry) {
        const rest = Object.entries(entry.values).filter(([name, value]) => !(name in patch) || !Object.is(patch[name], value));
        entry.values = rest.length > 0 ? Object.fromEntries(rest) : EMPTY;
        if (rest.length === 0 && !entry.timer) pendingScopes.delete(scope);
      }
      notify();
    }
  };
  const result = entry.writes.catch(() => undefined).then(run);
  entry.writes = result;
  return result;
}

/** Test seam: forget every unsaved edit and cancel scheduled saves. */
export function resetPendingAgentParams() {
  for (const entry of pendingScopes.values()) if (entry.timer) clearTimeout(entry.timer);
  pendingScopes.clear();
  failedScopes.clear();
  notify();
}

/** GET-only on mount. Defaults are presentation values; only explicit edits or submit flush write. */
export function useSessionAgentParams(sessionId: string | null, paramSchemas: ParamSchema[], offeringId?: string) {
  const { schema } = useProductSchema();
  const { user } = useAuth();
  const ownerId = user?.id ?? '';
  const harness = useMemo(() => client(schema), [schema]);
  const key = sessionId && ownerId ? sessionAgentParamsKey(schema, ownerId, sessionId, offeringId) : null;
  const scope = JSON.stringify(key);
  const { data: projection, error, isLoading } = useSWR<AgentParameterProjection>(
    key,
    () => harness.getParamsWithState(sessionId as string, undefined, offeringId),
    {
      refreshInterval: schema.runtime.frontend.harness_refresh_milliseconds || 4000,
      revalidateOnFocus: false,
    },
  );
  const serverParams = projection?.params ?? {};
  const local = useSyncExternalStore(subscribe, () => (key ? pendingScopes.get(scope)?.values ?? EMPTY : EMPTY), () => EMPTY);
  const saveError = useSyncExternalStore(subscribe, () => (key ? failedScopes.get(scope) ?? null : null), () => null);
  const values = useMemo(() => {
    const defaults = Object.fromEntries(paramSchemas
      .filter((param) => param.default !== undefined)
      .map((param) => [param.name, param.default]));
    return { ...defaults, ...mergeAgentParams(serverParams, local) };
  }, [paramSchemas, serverParams, local]);

  const flush = (): Promise<void> => {
    if (!key || isLoading || error) {
      return Promise.reject(new Error('Parameter scope is unavailable'));
    }
    return savePending(scope);
  };
  const setValue = (name: string, value: unknown) => {
    if (!key || !sessionId || !paramSchemas.some((param) => param.name === name)) return;
    const entry = pendingScopes.get(scope) ?? { sessionId, offeringId, key, schema, values: EMPTY, timer: null, writes: Promise.resolve() };
    entry.values = { ...entry.values, [name]: value };
    if (entry.timer) clearTimeout(entry.timer);
    // The delayed save belongs to the scope, not to a component: switching Offering or unmounting does not cancel it.
    entry.timer = setTimeout(() => {
      entry.timer = null;
      void savePending(scope).catch(() => undefined);
    }, schema.runtime.frontend.harness_save_debounce_milliseconds || 500);
    pendingScopes.set(scope, entry);
    failedScopes.delete(scope);
    notify();
  };
  const parameterStates: Record<string, AgentParameterState> = { ...projection?.parameterStates };
  for (const [name, value] of Object.entries(local)) {
    parameterStates[name] = value == null
      ? { source: 'default', value: paramSchemas.find(param => param.name === name)?.default }
      : { source: 'user', value };
  }
  return {
    parameterStates,
    values,
    enteredValues: mergeAgentParams(serverParams ?? {}, local),
    setValue,
    flush,
    loading: isLoading,
    error,
    saveError,
    ownerId,
  };
}

/** Explicit replay/edit write to an exact Offering; never copy the response into another owner's cache. */
export async function writeSessionAgentParams(schema: ProductSchema, sessionId: string, values: AgentParamValues, offeringId?: string, ownerId?: string): Promise<void> {
  const merged = await client(schema).putParamsWithState(sessionId, values, undefined, offeringId);
  if (ownerId) await mutateGlobal(sessionAgentParamsKey(schema, ownerId, sessionId, offeringId), merged, { revalidate: false });
}
