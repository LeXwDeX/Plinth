'use client';

import { useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from 'react';
import { consentAwaitingDecision, projectConsentState, type AgentHarnessClient } from '@/lib/agent-params';
import { SessionConsentController } from '@/lib/session-consent';

/** Each render reads only the active session's controller, including before effect cleanup. */
export function useSessionAgentConsent(
  sessionId: string | null,
  client: AgentHarnessClient,
  pollMilliseconds: number,
) {
  const controller = useMemo(
    () => new SessionConsentController(sessionId, client, pollMilliseconds),
    [sessionId, client, pollMilliseconds],
  );
  const currentController = useRef(controller);
  currentController.current = controller;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  useEffect(() => {
    controller.start();
    return () => controller.stop();
  }, [controller]);
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
