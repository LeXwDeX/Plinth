import type { ChatSession } from '@/store/chat-store';

export interface SessionPage {
  sessions: ChatSession[];
  nextCursor: string;
  /** The owner's sessions matching this page's search, independent of the cursor and page size. */
  total: number;
}

/** Flatten the loaded pages in order, keeping the first copy of a session that appears twice. */
export function mergeSessionPages(pages: readonly SessionPage[] | undefined): ChatSession[] {
  const seen = new Set<string>();
  const merged: ChatSession[] = [];
  for (const page of pages ?? []) {
    for (const session of page.sessions) {
      if (seen.has(session.id)) continue;
      seen.add(session.id);
      merged.push(session);
    }
  }
  return merged;
}

/** The cursor to request after the loaded pages; empty when the last page ended the list. */
export function nextSessionCursor(pages: readonly SessionPage[] | undefined): string {
  return pages && pages.length > 0 ? pages[pages.length - 1].nextCursor : '';
}

const SESSION_LIST_KEY = 'studio-session-list';

/** SWR infinite key for page `index`: null stops loading once a page reports no next cursor. */
export function sessionPageKey(
  index: number,
  previous: SessionPage | null,
  search: string,
): readonly [typeof SESSION_LIST_KEY, string, string] | null {
  if (index > 0 && !previous?.nextCursor) return null;
  return [SESSION_LIST_KEY, search, index > 0 ? previous?.nextCursor ?? '' : ''] as const;
}

/** Whether an SWR key is a page of the session list, for any search and cursor. */
export function isSessionPageKey(key: unknown): boolean {
  return Array.isArray(key) && key[0] === SESSION_LIST_KEY;
}

/** The SWR global mutate signature used to drop cached entries that match a key filter. */
export type SessionPageCacheMutate = (
  filter: (key: unknown) => boolean,
  data: undefined,
  options: { revalidate: false },
) => Promise<unknown>;

/**
 * Drop every cached session-list page, for every search. A page dropped this way is read from the server the
 * next time the list needs it; what is on screen stays until the new read arrives.
 */
export function forgetSessionPages(mutate: SessionPageCacheMutate): Promise<unknown> {
  return mutate(isSessionPageKey, undefined, { revalidate: false });
}

/** What a refresh of the open list was last made for. */
export interface SessionListRefreshPoint {
  query: string;
  /** `useChatStore.sessionListRevision` at the time. */
  revision: number;
}

/**
 * How many pages the open list reads again after its cached pages were dropped, or 0 to leave the reads to SWR.
 * Opening the list or a new search reads only the first page (0 when that search has nothing cached yet: SWR is
 * already reading it). A change to the sessions while the list stays open reads again up to the page shown, so
 * the person stays where they were and later pages follow the new cursors. Nothing changed reads nothing.
 */
export function sessionListRefreshSize(
  previous: SessionListRefreshPoint | null,
  next: SessionListRefreshPoint,
  shown: { page: number; cached: boolean },
): number {
  if (previous === null || previous.query !== next.query) return shown.cached ? 1 : 0;
  if (previous.revision !== next.revision) return shown.page + 1;
  return 0;
}

export interface SessionPageView {
  /** The sessions on the page being shown; empty until that page has loaded. */
  sessions: ChatSession[];
  /** Whether the shown page has arrived. */
  loaded: boolean;
  hasPrevious: boolean;
  /** Whether the shown page reports a page after it. */
  hasNext: boolean;
}

/** What the switcher shows for page `page` (0-based) of the loaded pages. */
export function sessionPageView(pages: readonly SessionPage[] | undefined, page: number): SessionPageView {
  const current = pages?.[page];
  return {
    sessions: current?.sessions ?? [],
    loaded: current !== undefined,
    hasPrevious: page > 0,
    hasNext: current !== undefined && current.nextCursor !== '',
  };
}

export interface SessionPageStatus {
  /** The owner-visible sessions matching the search, not the loaded rows. */
  total: number;
  /** The 1-based page being shown; never 0. */
  page: number;
  /** The total number of pages; never 0, so an empty result reads 1/1 instead of 1/0. */
  pages: number;
}

/**
 * The pager footer's numbers: the owner-visible total from the server (the shown page when it has arrived,
 * otherwise the first page), the page being shown, and how many pages that total makes. A page past the end
 * after a delete is shown as the last page until the list refreshes.
 */
export function sessionPageStatus(
  pages: readonly SessionPage[] | undefined,
  page: number,
  pageSize: number,
): SessionPageStatus {
  const total = Math.max(0, pages?.[page]?.total ?? pages?.[0]?.total ?? 0);
  const pageCount = Math.max(1, Math.ceil(total / Math.max(1, pageSize)));
  return { total, page: Math.min(Math.max(0, page) + 1, pageCount), pages: pageCount };
}

/**
 * The page to stay on after the loaded pages change (a delete or a refresh can shorten the list): never past
 * the last loaded page, and one page back when the shown page became empty.
 */
export function clampSessionPage(pages: readonly SessionPage[] | undefined, page: number): number {
  if (!pages || pages.length === 0 || page <= 0) return page;
  if (page >= pages.length) return pages[pages.length - 1].nextCursor === '' ? pages.length - 1 : page;
  return pages[page].sessions.length === 0 ? page - 1 : page;
}

/** The automatic title only replaces a title that is still the schema default. */
export function isAutoTitlePending(title: string, defaultTitle: string): boolean {
  return title === defaultTitle;
}

/**
 * After a turn completes the server may still be summarising the title in the background. Re-read the
 * session a bounded number of times until its title changes; stop silently on any failure or when the
 * session is no longer current.
 */
export async function pollAutoTitle(options: {
  currentTitle: string;
  attempts: number;
  intervalMilliseconds: number;
  fetchTitle: () => Promise<string>;
  isCurrent: () => boolean;
  apply: (title: string) => void;
  sleep?: (milliseconds: number) => Promise<void>;
}): Promise<boolean> {
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  for (let attempt = 0; attempt < options.attempts; attempt += 1) {
    await sleep(options.intervalMilliseconds);
    if (!options.isCurrent()) return false;
    try {
      const title = await options.fetchTitle();
      if (title && title !== options.currentTitle) {
        options.apply(title);
        return true;
      }
    } catch {
      return false;
    }
  }
  return false;
}
