'use client';
import { useRef } from 'react';

export interface ResourceSnapshot<T> { data: T | null; failed: boolean; loading: boolean }
export type Retained<T> = { identity: string; data: T } | null;

/**
 * Keeps the last loaded value while the same resource is only being refreshed (same `identity`, e.g. after
 * deleting a personal Skill), so lists built from it stay mounted instead of flashing empty and losing
 * keyboard focus (#238). A different identity (another user, session, model or revision) starts empty as
 * before, so stale data never stands in for a different question; a failed refresh shows the failure.
 */
export function retainOnRefresh<T>(last: Retained<T>, snapshot: ResourceSnapshot<T>, identity: string): { shown: ResourceSnapshot<T>; last: Retained<T> } {
  if (snapshot.data !== null) return { shown: snapshot, last: { identity, data: snapshot.data } };
  if (snapshot.loading && !snapshot.failed && last?.identity === identity) {
    return { shown: { data: last.data, failed: false, loading: false }, last };
  }
  return { shown: snapshot, last: snapshot.loading ? last : null };
}

export function useRetainedOnRefresh<T>(snapshot: ResourceSnapshot<T>, identity: string): ResourceSnapshot<T> {
  const last = useRef<Retained<T>>(null);
  const next = retainOnRefresh(last.current, snapshot, identity);
  last.current = next.last;
  return next.shown;
}
