'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { createHarnessClient, HarnessBrowserError, type HarnessSelectionInput, type HarnessView } from '@/lib/harness';
import { useChatStore } from '@/store/chat-store';

export function sessionHarnessKey(schemaRevision: string, ownerId: string, sessionId: string, route: string | undefined) {
  return ['session-harness', ownerId, sessionId, schemaRevision, route ?? ''] as const;
}

/** Owner-scoped, revision-bound session Harness state with explicit CAS conflict recovery. */
export function useSessionHarness(sessionId: string | null, ownerId: string) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createHarnessClient(schema), [schema]);
  const route = schema.runtime.http.routes.chat_session_harness;
  const key = sessionId && ownerId
    ? sessionHarnessKey(schema.revision, ownerId, sessionId, route)
    : null;
  const scope = JSON.stringify(key);
  const turnBoundary = useChatStore((state) => {
    const buffer = sessionId && ownerId ? state.streamingBuffers[sessionId] : undefined;
    return JSON.stringify([buffer?.assistantDbId ?? null, Boolean(buffer?.isStreaming)]);
  });
  const navigationRevision = useChatStore(state => state.activeSessionRevision);
  const identity = JSON.stringify([scope, navigationRevision]);
  const lifecycle = useRef({ identity, generation: 0, mounted: true });
  if (lifecycle.current.identity !== identity) {
    lifecycle.current = { ...lifecycle.current, identity, generation: lifecycle.current.generation + 1 };
  }
  const { data: view, error, isLoading, mutate } = useSWR<HarnessView>(
    key,
    ([, , id]: readonly [string, string, string]) => client.get(id),
    { revalidateOnFocus: true, keepPreviousData: false },
  );
  const [busyScopes, setBusyScopes] = useState<Set<string>>(() => new Set());
  const savingScopes = useRef(new Set<string>());
  const latestView = useRef<{ scope: string; view?: HarnessView }>({ scope, view });
  latestView.current = { scope, view };
  const busy = busyScopes.has(scope);
  const observedTurn = useRef({ scope, turnBoundary });

  useEffect(() => {
    const previous = observedTurn.current;
    observedTurn.current = { scope, turnBoundary };
    // SWR loads a new owner/session key itself. For an existing key, refresh only
    // at turn boundaries (including msg_created), never on streamed text tokens.
    if (previous.scope !== scope || previous.turnBoundary === turnBoundary || !sessionId || !ownerId) return;
    void mutate().catch(() => undefined);
  }, [mutate, ownerId, scope, sessionId, turnBoundary]);

  useEffect(() => {
    lifecycle.current.mounted = true;
    return () => {
      lifecycle.current.mounted = false;
      lifecycle.current.generation += 1;
    };
  }, []);

  const reload = useCallback(async () => {
    if (!key) return undefined;
    return mutate();
  }, [key, mutate]);

  const save = useCallback(async (selection: HarnessSelectionInput): Promise<HarnessView> => {
    if (!lifecycle.current.mounted || lifecycle.current.identity !== identity || !sessionId || !ownerId || !key || latestView.current.scope !== scope || !latestView.current.view) {
      throw new HarnessBrowserError('Harness session is not loaded', 0);
    }
    if (savingScopes.current.has(scope)) throw new HarnessBrowserError('A Harness save is already in progress', 0);
    const current = latestView.current.view;
    const generation = lifecycle.current.generation;
    const isCurrent = () => lifecycle.current.mounted && lifecycle.current.identity === identity && lifecycle.current.generation === generation;
    const publish = async (updated: HarnessView) => {
      if (!isCurrent() || updated.selection.session_id !== sessionId) return;
      // Compare the cache at mutation time as it may have advanced before React rerenders.
      await mutate(cached => !isCurrent() || (cached && cached.selection.revision > updated.selection.revision) ? cached : updated, { revalidate: false });
    };
    savingScopes.current.add(scope);
    setBusyScopes((currentScopes) => new Set(currentScopes).add(scope));
    try {
      const updated = await client.put(sessionId, current.selection.revision, selection);
      await publish(updated);
      return updated;
    } catch (error) {
      if (error instanceof HarnessBrowserError && error.status === 409) {
        // Keep the server's current selection visible and report the conflict;
        // the caller must let the user reconcile before trying another write.
        try {
          const latest = await client.get(sessionId);
          await publish(latest);
        } catch {
          // Preserve the original conflict; reload remains available for recovery.
        }
      }
      throw error;
    } finally {
      savingScopes.current.delete(scope);
      setBusyScopes((currentScopes) => {
        const next = new Set(currentScopes);
        next.delete(scope);
        return next;
      });
    }
  }, [client, identity, key, mutate, ownerId, scope, sessionId]);

  return { view: view ?? null, busy: busy || isLoading, error, reload, save };
}
