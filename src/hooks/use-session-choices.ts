'use client';

import { useCallback } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { choicesRoute, fetchChoiceCards, type ChoiceCardModel } from '@/lib/chat/choice-cards';

const NO_CARDS: ChoiceCardModel[] = [];

/**
 * The in-chat choice cards of the session (#321), read from the server, so a
 * card and its answer look the same after a reload, in another session switch
 * or on another device. Read again when a turn ends.
 */
export function useSessionChoices(sessionId: string | null) {
  const { schema } = useProductSchema();
  const enabled = sessionId !== null && choicesRoute(schema, sessionId) !== null;
  const { data, mutate } = useSWR<ChoiceCardModel[]>(
    enabled ? ['chat-choices', sessionId] : null,
    ([, id]: readonly [string, string]) => fetchChoiceCards(schema, id),
    { revalidateOnFocus: true, keepPreviousData: false },
  );
  const refresh = useCallback(async () => { await mutate(); }, [mutate]);
  return { cards: enabled ? data ?? NO_CARDS : NO_CARDS, refresh };
}
