'use client';

import { useMemo } from 'react';

import { handleOfPicture } from '@/lib/workspace/object-handle';
import { useChatStore } from '@/store/chat-store';
import type { CanvasNode } from '@/store/canvas-store';

const NO_MESSAGES: never[] = [];

/** The loaded messages of a session: the only place a picture handle comes from (the server put it on their attachments). */
export function useSessionMessages(sessionId: string | null | undefined) {
  return useChatStore((state) => (sessionId ? state.messagesBySession[sessionId] : undefined) ?? NO_MESSAGES);
}

/**
 * The chat handle of every picture layer that shows a picture of the session's chat, by node id. Layers with
 * no handle (uploads never sent, brush layers, merged pictures, shapes, text) are absent: no handle is invented.
 */
export function useNodeHandles(sessionId: string, nodes: readonly CanvasNode[]): ReadonlyMap<string, string> {
  const messages = useSessionMessages(sessionId);
  return useMemo(() => {
    const result = new Map<string, string>();
    if (messages.length === 0) return result;
    for (const node of nodes) {
      if (node.type !== 'image' || !node.imageUrl) continue;
      const handle = handleOfPicture(messages, { url: node.imageUrl, ...(node.captureId ? { captureId: node.captureId } : {}) });
      if (handle) result.set(node.id, handle);
    }
    return result;
  }, [messages, nodes]);
}
