'use client';

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { readSessionSkill, writeSessionSkill } from '@/lib/session-skill-storage';

export function useSessionSkillSelection(
  ownerId: string | null,
  sessionId: string | null,
  catalog: ReadonlyArray<{ id: string }> | null,
) {
  const [, update] = useState(0);
  const scope = useRef<{ owner: string | null; selection: Record<string, string | null>; pending: Set<string> }>({ owner: ownerId, selection: {}, pending: new Set() });
  if (scope.current.owner !== ownerId) scope.current = { owner: ownerId, selection: {}, pending: new Set() };
  const selectionRef = useRef(scope.current.selection);
  selectionRef.current = scope.current.selection;

  const select = useCallback((session: string, id: string | null) => {
    if (!ownerId || scope.current.owner !== ownerId) return;
    scope.current.pending.delete(session);
    const selection = { ...scope.current.selection, [session]: id };
    scope.current.selection = selection;
    selectionRef.current = selection;
    try { writeSessionSkill(window.localStorage, ownerId, session, id); } catch { /* Storage may be blocked. */ }
    update((version) => version + 1);
  }, [ownerId]);

  useLayoutEffect(() => {
    if (!ownerId || !sessionId || Object.hasOwn(scope.current.selection, sessionId)) return;
    let saved: string | null = null;
    try { saved = readSessionSkill(window.localStorage, ownerId, sessionId); } catch { /* Storage may be blocked. */ }
    select(sessionId, saved);
    if (saved) scope.current.pending.add(sessionId);
  }, [ownerId, sessionId, select]);

  useEffect(() => {
    if (!sessionId || !catalog || !scope.current.pending.has(sessionId)) return;
    const saved = scope.current.selection[sessionId];
    // An unavailable Skill keeps its selection so it comes back when the model supports it again.
    if (!saved || !catalog.some((skill) => skill.id === saved)) select(sessionId, null);
    else scope.current.pending.delete(sessionId);
  }, [sessionId, catalog, select]);

  return { selection: scope.current.selection, selectionRef, select };
}
