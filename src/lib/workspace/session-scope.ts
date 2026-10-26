import { useChatStore } from '@/store/chat-store';
import type { CurrentObject } from '@/store/workspace-store';

export interface SessionScope {
  sessionId: string | null;
  revision: number;
}

export function captureSessionScope(): SessionScope {
  const state = useChatStore.getState();
  return { sessionId: state.activeSessionId, revision: state.activeSessionRevision };
}

export function sameSessionScope(left: SessionScope, right: SessionScope): boolean {
  return left.sessionId === right.sessionId && left.revision === right.revision;
}

export function isCurrentSessionScope(scope: SessionScope): boolean {
  return sameSessionScope(scope, captureSessionScope());
}

export function selectionForSession(selection: CurrentObject | null, sessionId: string | null): CurrentObject | null {
  return sessionId && selection?.sessionId === sessionId ? selection : null;
}
