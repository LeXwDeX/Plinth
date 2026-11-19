'use client';

import { useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from 'react';
import { useProductSchema } from '@/components/product-schema-provider';
import { useSessionBusy } from '@/hooks/use-polling';
import { consentAwaitingDecision, projectConsentState, type AgentHarnessClient } from '@/lib/agent-params';
import { SessionConsentController } from '@/lib/session-consent';

/**
 * Each render reads only the active session's controller, including before effect cleanup.
 *
 * The controller reads the consent state every `pollMilliseconds` while a turn or a task of the session runs, and
 * slows down (Schema `idle_polling`) while it is idle; it does not read at all in a hidden tab or after the login
 * ended (#385).
 */
export function useSessionAgentConsent(
  sessionId: string | null,
  client: AgentHarnessClient,
  pollMilliseconds: number,
) {
  const { schema } = useProductSchema();
  const factor = schema.runtime.frontend.idle_polling?.backoff_factor;
  const ceiling = schema.runtime.frontend.idle_polling?.max_interval_milliseconds;
  const busy = useSessionBusy(sessionId);
  const busyRef = useRef(busy);
  busyRef.current = busy;
  const controller = useMemo(
    () => new SessionConsentController(sessionId, client, pollMilliseconds, {
      idle: factor === undefined || ceiling === undefined ? undefined : { backoff_factor: factor, max_interval_milliseconds: ceiling },
      busy: () => busyRef.current,
    }),
    [sessionId, client, pollMilliseconds, factor, ceiling],
  );
  const currentController = useRef(controller);
  currentController.current = controller;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  useEffect(() => {
    controller.start();
    return () => controller.stop();
  }, [controller]);
  // A turn or task that starts now may bring a decision within moments: do not let a long idle wait run out first.
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (busy && !wasBusy.current) controller.refresh();
    wasBusy.current = busy;
  }, [busy, controller]);
  const decide = useCallback(async (requestId: string, approved: boolean) => {
    if (currentController.current !== controller) return;
    try {
      await controller.decide(requestId, approved);
    } catch (error) {
      if (currentController.current === controller) throw error;
    }
  }, [controller]);
  return {
    consent: projectConsentState(snapshot.state),
    awaiting: consentAwaitingDecision(snapshot.state),
    busy: snapshot.busy,
    decide,
  };
}
