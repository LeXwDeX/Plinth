import { slotAction, type ThreeViewGroup } from '@/lib/workspace/three-view';

/**
 * #636: a view this tab asked the server to start or retry, before the server answered. Starting a view can take
 * the server a while (it shrinks a large picture first), and it goes on when the page is reloaded in between:
 * the reloaded card then reads the view as still startable, and nothing else on the page reads it again. The
 * request is remembered in sessionStorage (it survives a reload in the tab, not shared with other tabs) until the
 * server answers, or the view moves, or the page's own limit for trying to start a view runs out.
 */
export interface PendingAdvance {
  sessionId: string;
  runId: string;
  slotKey: string;
  action: 'start' | 'retry';
  attempt: number;
  /** Epoch milliseconds the request was sent. */
  at: number;
}

const STORAGE_KEY = 'plinth:three-view-pending';

const keyOf = (item: Pick<PendingAdvance, 'sessionId' | 'runId' | 'slotKey'>) => `${item.sessionId}|${item.runId}|${item.slotKey}`;

function isPending(value: unknown): value is PendingAdvance {
  if (!value || typeof value !== 'object') return false;
  const item = value as Partial<PendingAdvance>;
  return typeof item.sessionId === 'string' && typeof item.runId === 'string' && typeof item.slotKey === 'string'
    && (item.action === 'start' || item.action === 'retry')
    && typeof item.attempt === 'number' && typeof item.at === 'number';
}

export function readPendingAdvances(): PendingAdvance[] {
  try {
    const value: unknown = JSON.parse(window.sessionStorage.getItem(STORAGE_KEY) ?? '[]');
    return Array.isArray(value) ? value.filter(isPending) : [];
  } catch {
    return [];
  }
}

function writePendingAdvances(items: readonly PendingAdvance[]): void {
  try {
    if (items.length === 0) window.sessionStorage.removeItem(STORAGE_KEY);
    else window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(items));
  } catch { /* Blocked storage: a reload then reads the card once, as before #636. */ }
}

export function rememberPendingAdvance(item: PendingAdvance): void {
  writePendingAdvances([...readPendingAdvances().filter((other) => keyOf(other) !== keyOf(item)), item]);
}

export function forgetPendingAdvance(item: Pick<PendingAdvance, 'sessionId' | 'runId' | 'slotKey'>): void {
  const items = readPendingAdvances();
  const rest = items.filter((other) => keyOf(other) !== keyOf(item));
  if (rest.length !== items.length) writePendingAdvances(rest);
}

/**
 * Whether the server may still be taking a request of `sessionId`: one is younger than `limitMs` and its view still
 * offers the same action at the same attempt. A view that moved, a group no longer listed, or an expired request
 * no longer counts.
 */
export function pendingAdvanceOpen(
  item: PendingAdvance,
  groups: readonly ThreeViewGroup[],
  now: number,
  limitMs: number | null,
): boolean {
  if (limitMs === null || now - item.at > limitMs) return false;
  const slot = groups.find((group) => group.runId === item.runId)?.slots.find((other) => other.key === item.slotKey);
  return Boolean(slot && slotAction(slot) === item.action && slot.attempt === item.attempt);
}

/**
 * The remembered requests of `sessionId` still open on `groups`; the others of that session are forgotten. Requests
 * of other sessions are kept for when those are open.
 */
export function settlePendingAdvances(
  sessionId: string,
  groups: readonly ThreeViewGroup[],
  now: number,
  limitMs: number | null,
): PendingAdvance[] {
  const items = readPendingAdvances();
  const open = items.filter((item) => item.sessionId === sessionId && pendingAdvanceOpen(item, groups, now, limitMs));
  const kept = items.filter((item) => item.sessionId !== sessionId || open.includes(item));
  if (kept.length !== items.length) writePendingAdvances(kept);
  return open;
}
