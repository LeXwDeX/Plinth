/**
 * When a session's version chain (lineage) is read again.
 *
 * Every `useSessionVersions` consumer of a session (a dozen on the studio page) shares one SWR cache entry.
 * Opening a session needs no explicit refresh: SWR revalidates a newly mounted or changed key once, deduplicated
 * across all consumers. An explicit refresh (`mutate()`, which SWR never deduplicates) is only for a change seen
 * after the session is open that can alter the chain — a task of it settling, a view finishing, a bumped
 * revision — and exactly one consumer asks for it, however many see the change in the same commit.
 */
export interface LineageWatch {
  sessionId: string;
  /** What the consumer last saw of the things that change the chain. */
  signature: string;
}

export function lineageWatchSignature(settled: string, viewsDone: string, revision: number): string {
  return `${revision}|${settled}|${viewsDone}`;
}

/** How long one change, claimed by the first consumer that sees it, stays claimed for the others. */
const CLAIM_MILLISECONDS = 1_000;

/**
 * True when this consumer should refresh: it already watched this session (not an open) and saw a change no
 * other consumer has just claimed.
 */
export function createLineageRefreshGate(now: () => number = Date.now) {
  const claimed = new Map<string, number>();
  return (previous: LineageWatch | null, next: LineageWatch): boolean => {
    if (!previous || previous.sessionId !== next.sessionId || previous.signature === next.signature) return false;
    const time = now();
    for (const [key, until] of claimed) if (until <= time) claimed.delete(key);
    const key = `${next.sessionId}\u0000${next.signature}`;
    if (claimed.has(key)) return false;
    claimed.set(key, time + CLAIM_MILLISECONDS);
    return true;
  };
}

/** The gate the studio's consumers share. */
export const claimLineageRefresh = createLineageRefreshGate();

/**
 * The settled tasks that can still change the chain: those the loaded chain does not already show as finished.
 * Opening a session loads its finished tasks into the tracker; they are already in the chain the open read
 * returned, so they must not count as a change and ask for a second read (#263). Until the chain has loaded,
 * nothing counts: the read in flight answers it.
 */
export function settledOutsideChain(
  settledTaskIds: readonly string[],
  chainFinished: ReadonlySet<string> | null,
): string {
  if (!chainFinished) return '';
  return settledTaskIds.filter((taskId) => !chainFinished.has(taskId)).sort().join(',');
}

/**
 * The settled tasks that have counted as a chain change, per session, shared by every consumer. A set kept by
 * each consumer would differ for one mounted later (a details panel, the other stage), so two consumers would
 * see different signatures for the same change and both refresh. The set only grows: a task the refresh then
 * brings into the chain must not flip the signature back and ask for another read.
 */
export function createSettledCounter() {
  const bySession = new Map<string, Set<string>>();
  return (sessionId: string, outsideChain: string): string => {
    let ids = bySession.get(sessionId);
    if (!ids) {
      ids = new Set();
      bySession.set(sessionId, ids);
    }
    for (const taskId of outsideChain ? outsideChain.split(',') : []) ids.add(taskId);
    return [...ids].sort().join(',');
  };
}

/** The counter the studio's consumers share. */
export const countSettledChanges = createSettledCounter();
