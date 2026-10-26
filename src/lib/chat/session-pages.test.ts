import { mutate as globalMutate, unstable_serialize } from 'swr';
import { cache } from 'swr/_internal';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import {
  forgetSessionPages,
  isAutoTitlePending,
  isSessionPageKey,
  sessionListRefreshSize,
  mergeSessionPages,
  nextSessionCursor,
  pollAutoTitle,
  sessionPageKey,
  clampSessionPage,
  sessionPageStatus,
  sessionPageView,
  type SessionPage,
} from '@/lib/chat/session-pages';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatSession } from '@/store/chat-store';

const session = (id: string, title = id): ChatSession => ({ id, title, createdAt: '', updatedAt: '' });
const page = (sessions: ChatSession[], nextCursor = '', total = sessions.length): SessionPage => ({ sessions, nextCursor, total });

describe('session list paging', () => {
  it('merges pages in order and drops repeats', () => {
    const pages: SessionPage[] = [
      page([session('a'), session('b')], 'c1'),
      page([session('b'), session('c')]),
    ];
    expect(mergeSessionPages(pages).map((item) => item.id)).toEqual(['a', 'b', 'c']);
    expect(mergeSessionPages(undefined)).toEqual([]);
  });

  it('follows the cursor and stops when a page has none', () => {
    expect(sessionPageKey(0, null, 'cat')).toEqual(['studio-session-list', 'cat', '']);
    expect(sessionPageKey(1, page([], 'c1'), 'cat')).toEqual(['studio-session-list', 'cat', 'c1']);
    expect(sessionPageKey(1, page([], ''), 'cat')).toBeNull();
    expect(nextSessionCursor([page([], 'x')])).toBe('x');
    expect(nextSessionCursor([])).toBe('');
  });

  it('shows one page at a time with previous and next', () => {
    const pages: SessionPage[] = [
      page([session('a'), session('b')], 'c1'),
      page([session('c')]),
    ];
    expect(sessionPageView(pages, 0)).toEqual({ sessions: [session('a'), session('b')], loaded: true, hasPrevious: false, hasNext: true });
    expect(sessionPageView(pages, 1)).toEqual({ sessions: [session('c')], loaded: true, hasPrevious: true, hasNext: false });
    expect(sessionPageView(pages.slice(0, 1), 1)).toEqual({ sessions: [], loaded: false, hasPrevious: true, hasNext: false });
    expect(sessionPageView(undefined, 0).loaded).toBe(false);
  });

  it('reports the owner-visible total and never asks for page zero of zero', () => {
    expect(sessionPageStatus(undefined, 0, 15)).toEqual({ total: 0, page: 1, pages: 1 });
    expect(sessionPageStatus([page([], '', 0)], 0, 15)).toEqual({ total: 0, page: 1, pages: 1 });
    expect(sessionPageStatus([page([session('a')], '', 1)], 0, 15)).toEqual({ total: 1, page: 1, pages: 1 });
    const fifteen = Array.from({ length: 15 }, (_, index) => session(`s${index}`));
    expect(sessionPageStatus([page(fifteen, '', 15)], 0, 15)).toEqual({ total: 15, page: 1, pages: 1 });
    const sixteen = [...fifteen, session('s15')];
    const pages = [page(sixteen.slice(0, 15), 'c1', 16), page(sixteen.slice(15), '', 16)];
    expect(sessionPageStatus(pages, 0, 15)).toEqual({ total: 16, page: 1, pages: 2 });
    expect(sessionPageStatus(pages, 1, 15)).toEqual({ total: 16, page: 2, pages: 2 });
  });

  it('formats the search matches wording with the owner-visible match count', () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    schema.content.messages['ui.studio.session.pagination_matches'] = '匹配 {total} 个会话 · 第 {page}/{pages} 页';
    expect(formatSchemaText(schemaText(schema, 'ui.studio.session.pagination_matches'), { page: 1, pages: 1, total: 7 })).toBe('匹配 7 个会话 · 第 1/1 页');
    expect(formatSchemaText(schemaText(schema, 'ui.studio.session.pagination_matches'), { page: 1, pages: 1, total: 0 })).toBe('匹配 0 个会话 · 第 1/1 页');
  });

  it('keeps a large total intact when the pager wraps it', () => {
    expect(sessionPageStatus([page([], '', 123456789012)], 0, 15)).toEqual({ total: 123456789012, page: 1, pages: 8230452601 });
  });

  it('uses the shown page total, falls back to the first page, and clamps a page past the end', () => {
    const firstOnly = [page([session('a')], 'c1', 16)];
    expect(sessionPageStatus(firstOnly, 0, 15)).toEqual({ total: 16, page: 1, pages: 2 });
    expect(sessionPageStatus([page([session('a')], 'c1', 16), page([session('b')], '', 1)], 1, 15)).toEqual({ total: 1, page: 1, pages: 1 });
    expect(sessionPageStatus([page([session('a')], '', 4)], 3, 15)).toEqual({ total: 4, page: 1, pages: 1 });
  });

  it('formats the recommended summary with page, page count and owner-visible total', () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    schema.content.messages['ui.studio.session.pagination_summary'] = '第 {page}/{pages} 页 · 共 {total} 个会话';
    expect(formatSchemaText(schemaText(schema, 'ui.studio.session.pagination_summary'), { page: 1, pages: 2, total: 16 })).toBe('第 1/2 页 · 共 16 个会话');
    expect(formatSchemaText(schemaText(schema, 'ui.studio.session.pagination_summary'), { page: 1, pages: 1, total: 0 })).toBe('第 1/1 页 · 共 0 个会话');
  });

  it('keeps the shown page inside the list after it shrinks', () => {
    const one: SessionPage[] = [page([session('a')])];
    expect(clampSessionPage(one, 1)).toBe(0);
    expect(clampSessionPage([page([session('a')], 'c1')], 1)).toBe(1);
    expect(clampSessionPage([page([session('a')], 'c1'), page([])], 1)).toBe(0);
    expect(clampSessionPage([page([])], 0)).toBe(0);
    expect(clampSessionPage(undefined, 2)).toBe(2);
  });
});

describe('session list freshness', () => {
  it('recognises every page key of every search and nothing else', () => {
    expect(isSessionPageKey(sessionPageKey(0, null, ''))).toBe(true);
    expect(isSessionPageKey(sessionPageKey(1, page([], 'c1'), '细化'))).toBe(true);
    expect(isSessionPageKey(['session-cover', 'a', '', ''])).toBe(false);
    expect(isSessionPageKey('studio-session-list')).toBe(false);
    expect(isSessionPageKey(undefined)).toBe(false);
  });

  it('reads the first page on opening and on a new search, unless SWR is already reading an uncached one', () => {
    const at = (query: string, revision: number) => ({ query, revision });
    expect(sessionListRefreshSize(null, at('', 0), { page: 0, cached: true })).toBe(1);
    expect(sessionListRefreshSize(null, at('', 0), { page: 0, cached: false })).toBe(0);
    expect(sessionListRefreshSize(at('', 0), at('细化', 0), { page: 3, cached: true })).toBe(1);
    expect(sessionListRefreshSize(at('细化', 0), at('', 0), { page: 0, cached: false })).toBe(0);
  });

  it('reads again up to the shown page when the sessions change while the list is open', () => {
    const at = (revision: number) => ({ query: 'cat', revision });
    expect(sessionListRefreshSize(at(1), at(2), { page: 0, cached: true })).toBe(1);
    expect(sessionListRefreshSize(at(1), at(2), { page: 2, cached: true })).toBe(3);
    expect(sessionListRefreshSize(at(1), at(2), { page: 0, cached: false })).toBe(1);
    expect(sessionListRefreshSize(at(1), at(1), { page: 2, cached: true })).toBe(0);
  });

  it('drops cached pages of every search from the SWR cache and keeps other entries', async () => {
    const listed: SessionPage = page([session('a')]);
    const keys = [sessionPageKey(0, null, ''), sessionPageKey(1, page([], 'c1'), ''), sessionPageKey(0, null, '细化')];
    const other = ['session-cover', 'a', '', ''];
    // SWR records the original key as `_k` (not in its public State type); key filters match on it.
    for (const key of [...keys, other]) cache.set(unstable_serialize(key), { data: listed, _k: key } as Parameters<typeof cache.set>[1]);
    await forgetSessionPages(globalMutate);
    for (const key of keys) expect(cache.get(unstable_serialize(key))?.data).toBeUndefined();
    expect(cache.get(unstable_serialize(other))?.data).toEqual(listed);
  });

  it('asks SWR to drop without reading again, so a closed list sends no request', async () => {
    const mutate = vi.fn(async () => undefined);
    await forgetSessionPages(mutate);
    expect(mutate).toHaveBeenCalledWith(isSessionPageKey, undefined, { revalidate: false });
  });
});

describe('session list schema', () => {
  const frontend = defaultCatalog.application.runtime.frontend;

  it('declares the 15-row page and a server cap that allows it', () => {
    expect(frontend.session_menu_page_size).toBe(15);
    expect(frontend.session_menu_search_debounce_milliseconds).toBeGreaterThan(0);
    expect(defaultCatalog.application.chat.api.session_list.maximum_page_size).toBeGreaterThanOrEqual(frontend.session_menu_page_size);
  });

  it('has text for the loading and retry rows and the pager', () => {
    const text = JSON.stringify(defaultCatalog.application);
    for (const key of ['ui.studio.session.loading_more', 'ui.studio.session.load_failed', 'ui.studio.session.previous_page', 'ui.studio.session.next_page', 'ui.studio.session.pagination_summary', 'ui.studio.session.pagination_matches']) {
      expect(text).toContain(`"${key}"`);
    }
  });
});

describe('automatic title refresh', () => {
  it('only waits for sessions that still carry the default title', () => {
    expect(isAutoTitlePending('新对话', '新对话')).toBe(true);
    expect(isAutoTitlePending('我的标题', '新对话')).toBe(false);
  });

  it('applies the new title once it appears', async () => {
    const titles = ['新对话', '新对话', '银色机械猫'];
    const apply = vi.fn();
    const result = await pollAutoTitle({
      currentTitle: '新对话',
      attempts: 5,
      intervalMilliseconds: 1,
      fetchTitle: async () => titles.shift() ?? '',
      isCurrent: () => true,
      apply,
      sleep: async () => {},
    });
    expect(result).toBe(true);
    expect(apply).toHaveBeenCalledWith('银色机械猫');
  });

  it('gives up silently after the attempts, on failure, or when the session is gone', async () => {
    const base = { currentTitle: 'x', attempts: 3, intervalMilliseconds: 1, apply: vi.fn(), sleep: async () => {} };
    const fetchTitle = vi.fn(async () => 'x');
    expect(await pollAutoTitle({ ...base, fetchTitle, isCurrent: () => true })).toBe(false);
    expect(fetchTitle).toHaveBeenCalledTimes(3);
    expect(await pollAutoTitle({ ...base, fetchTitle: async () => { throw new Error('boom'); }, isCurrent: () => true })).toBe(false);
    expect(await pollAutoTitle({ ...base, fetchTitle: async () => 'new', isCurrent: () => false })).toBe(false);
    expect(base.apply).not.toHaveBeenCalled();
  });
});
