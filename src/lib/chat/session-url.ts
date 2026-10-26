import type { ChatSession } from '@/store/chat-store';
import type { ChatBrowserClient } from './browser-client';

/**
 * The open conversation lives in the page address (`?session=<id>`), so a refresh
 * reopens the same conversation instead of the most recent one. The address is
 * only rewritten in place: switching conversations adds no history entries.
 */
export const SESSION_QUERY_FIELD = 'session';

type AddressHistory = Pick<History, 'replaceState' | 'state'>;
type AddressLocation = Pick<Location, 'href'>;

/** The conversation id named by the page address, or null. */
export function sessionFromAddress(location: AddressLocation = window.location): string | null {
  const value = new URL(location.href).searchParams.get(SESSION_QUERY_FIELD);
  return value && value.trim() !== '' ? value : null;
}

/** Writes (or, with null, removes) the open conversation id in the page address. */
export function writeSessionToAddress(
  sessionId: string | null,
  location: AddressLocation = window.location,
  history: AddressHistory = window.history,
): void {
  const url = new URL(location.href);
  if ((url.searchParams.get(SESSION_QUERY_FIELD) ?? null) === sessionId) return;
  if (sessionId === null) url.searchParams.delete(SESSION_QUERY_FIELD);
  else url.searchParams.set(SESSION_QUERY_FIELD, sessionId);
  history.replaceState(history.state, '', url.pathname + url.search + url.hash);
}

/**
 * The conversation to open on page load: the one the address names when the
 * server still returns it for this user, otherwise the most recent one.
 * `sessions` is the list to seed the store with; null `session` means none exists yet.
 */
export async function resolveInitialSession(
  client: Pick<ChatBrowserClient, 'getSession' | 'listSessionPage'>,
  requestedId: string | null,
  signal?: AbortSignal,
): Promise<{ session: ChatSession | null; sessions: ChatSession[] }> {
  if (requestedId) {
    try {
      const session = await client.getSession(requestedId, signal);
      return { session, sessions: [session] };
    } catch (error) {
      if (signal?.aborted) throw error;
      // Deleted, foreign or unreachable: fall back to the latest conversation.
    }
  }
  const { sessions } = await client.listSessionPage({ search: '', limit: 1 }, signal);
  return { session: sessions[0] ?? null, sessions };
}
