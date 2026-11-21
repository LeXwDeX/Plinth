'use client';

import { useCallback } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import {
  canExtendSessionSteps,
  extendSessionSteps,
  readSessionSteps,
  sessionStepsRoute,
  type SessionSteps,
} from '@/lib/chat/session-steps';

/**
 * #330 §6: the live 3D step standing of one asset session, for the "give it a
 * few more steps" entry of an external MCP asset. A session that is not a
 * steps asset reads as 404 and yields no standing, so the bar never renders
 * for an ordinary chat.
 */
export function useSessionSteps(sessionID: string | null) {
  const { schema } = useProductSchema();
  const supported = Boolean(sessionID) && canExtendSessionSteps(schema);
  const url = supported && sessionID ? sessionStepsRoute(schema, sessionID) : '';
  const { data, error, isLoading, mutate } = useSWR<SessionSteps | null>(
    url ? ['session-steps', url] : null,
    ([, target]) => readSessionSteps(target as string),
    { keepPreviousData: false, shouldRetryOnError: false, revalidateOnFocus: false },
  );

  const extend = useCallback(async (target: number): Promise<SessionSteps | null> => {
    if (!url) return null;
    const changed = await extendSessionSteps(url, target);
    await mutate(changed, { revalidate: false });
    return changed;
  }, [mutate, url]);

  return {
    steps: supported ? data ?? null : null,
    loading: supported && isLoading,
    error: supported ? error : undefined,
    extend,
  };
}