'use client';

import { useMemo } from 'react';

import { handleReferences, type ComposerReference } from '@/lib/chat/composer-reference';
import { useChatStore } from '@/store/chat-store';

const NO_MESSAGES: never[] = [];

/** The pictures of the open session by handle ("@img3"), for handle tags that attach them to the next message. */
export function useHandleReferences(): ReadonlyMap<string, ComposerReference> {
  const sessionId = useChatStore((state) => state.activeSessionId);
  const messages = useChatStore((state) => state.messagesBySession[sessionId ?? ''] ?? NO_MESSAGES);
  return useMemo(() => handleReferences(messages), [messages]);
}
