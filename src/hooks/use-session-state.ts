import { useCallback, useRef, useState, type Dispatch, type SetStateAction } from 'react';

import { captureSessionScope, isCurrentSessionScope, sameSessionScope } from '@/lib/workspace/session-scope';
import { useChatStore } from '@/store/chat-store';

/** Draft state becomes empty immediately on navigation. An old async setter
 * cannot mutate the new draft, including a switch away and back to the same ID. */
export function useSessionState<T>(initialValue: T): [T, Dispatch<SetStateAction<T>>] {
  const sessionId = useChatStore((state) => state.activeSessionId);
  const revision = useChatStore((state) => state.activeSessionRevision);
  const initial = useRef(initialValue);
  const [stored, setStored] = useState(() => ({ scope: captureSessionScope(), value: initial.current }));
  const scope = { sessionId, revision };
  const value = sameSessionScope(stored.scope, scope) ? stored.value : initial.current;
  const update = useCallback((action: SetStateAction<T>) => {
    const origin = { sessionId, revision };
    if (!isCurrentSessionScope(origin)) return;
    setStored((previous) => {
      if (!isCurrentSessionScope(origin)) return previous;
      const current = sameSessionScope(previous.scope, origin) ? previous.value : initial.current;
      return { scope: origin, value: typeof action === 'function'
        ? (action as (current: T) => T)(current) : action };
    });
  }, [sessionId, revision]);
  return [value, update];
}
