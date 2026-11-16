'use client';

import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import type { OfferingWire } from '@/lib/agent-offerings';
import { briefSignature } from '@/lib/chat/production-brief';
import { fetchPurposeParameters, followsPurpose, type PurposeValues } from '@/lib/workspace/purpose-parameters';
import { useChatStore } from '@/store/chat-store';

const NONE: PurposeValues = {};

/**
 * What the session purpose gives the parameters that follow it, for the Offerings given (#306), by Offering ID.
 * The 加工 panel shows it on a parameter the person has not picked, so "按会话用途" says whether that is 2K or 4K
 * before the step is confirmed. The server resolves the values from the stored brief with the rule that fills
 * the plan the panel creates; nothing about a purpose is known here.
 *
 * The values are read again whenever the brief may have changed: when the page's copy of the session's brief
 * changes (the brief panel saved it, or the session was read again), and when a turn ends, since the Agent may
 * have set the purpose with a tool the page's copy is not told about. A value stays on screen until the new
 * answer arrives; it is never shown for another session, and an Offering the server could not answer has none.
 */
export function usePurposeParameters(sessionId: string | null, offerings: readonly OfferingWire[]): PurposeValues {
  const { schema } = useProductSchema();
  const brief = useChatStore((state) => briefSignature(state.sessions.find((session) => session.id === sessionId)?.brief));
  const turns = useChatStore((state) => state.sessionListRevision);
  const offeringIds = [...new Set(offerings.filter(followsPurpose).map((offering) => offering.id))].sort();
  const enabled = Boolean(sessionId && schema.runtime.http.routes.chat_session_purpose_parameters && offeringIds.length > 0);
  const { data } = useSWR(
    enabled ? (['purpose-parameters', schema.revision, sessionId, offeringIds, brief, turns] as const) : null,
    // The answer carries the session it belongs to: while another session's values are kept on screen between a
    // switch and the new answer they must not pass for this session's.
    async ([, , session, ids]) => ({ sessionId: session, values: await fetchPurposeParameters(schema, session as string, ids) }),
    { keepPreviousData: true },
  );
  return data?.sessionId === sessionId && data.values ? data.values : NONE;
}
