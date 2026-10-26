'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useSWRConfig } from 'swr';
import useSWRInfinite from 'swr/infinite';
import { Popover } from '@base-ui/react/popover';
import { Check, ChevronDown, ChevronLeft, ChevronRight, Image as ImageIcon, Loader2, Pencil, Plus, RotateCw, Search, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAnchoredPositioner } from '@/components/ui/overlay';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useSessionCover } from '@/hooks/use-session-cover';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { renameKeyIntent, resolveRename } from '@/lib/chat/session-rename';
import {
  clampSessionPage,
  forgetSessionPages,
  mergeSessionPages,
  sessionListRefreshSize,
  sessionPageKey,
  sessionPageStatus,
  sessionPageView,
  type SessionListRefreshPoint,
  type SessionPage,
} from '@/lib/chat/session-pages';
import { activeLocale } from '@/lib/i18n/locale';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { relativeTime } from '@/lib/workspace/relative-time';
import { cn } from '@/lib/utils';
import { useChatStore, type ChatSession } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';

/** What a key press in the delete confirmation means; IME and repeated keys never delete. */
export function deleteConfirmKeyIntent(event: {
  key: string;
  isComposing: boolean;
  keyCode: number;
  repeat: boolean;
}): 'confirm' | 'cancel' | null {
  if (event.isComposing || event.keyCode === 229 || event.repeat) return null;
  if (event.key === 'Enter') return 'confirm';
  if (event.key === 'Escape') return 'cancel';
  return null;
}

/**
 * The session title as plain text; the pencil button next to it starts an in-place rename (Enter or blur saves,
 * Esc cancels). The name area has one fixed width, so the pencil, the menu arrow and the popover anchor keep
 * their place for short, long, Chinese and English titles and while the field replaces the text.
 */
export function SessionTitle({ session }: { session: ChatSession }) {
  const { schema } = useProductSchema();
  const [draft, setDraft] = useState<string | null>(null);
  const saving = useRef(false);
  const input = useRef<HTMLInputElement>(null);
  const label = schemaText(schema, 'ui.workspace.session.rename');

  const commit = async () => {
    if (draft === null || saving.current) return;
    const resolved = resolveRename(draft, session.title);
    if (resolved.kind === 'cancel') { setDraft(null); return; }
    const title = resolved.title;
    saving.current = true;
    try {
      const renamed = await createChatBrowserClient(schema).renameSession(session.id, title);
      useChatStore.getState().updateSessionTitle(renamed.id, renamed.title);
      setDraft(null);
    } catch {
      toast.error(schemaText(schema, 'ui.workspace.session.rename_failed'));
    } finally {
      saving.current = false;
    }
  };

  if (draft !== null) {
    return (
      <span className="flex w-72 max-w-full min-w-0 items-center gap-1">
        <input
          ref={input}
          autoFocus
          value={draft}
          aria-label={label}
          onChange={(event) => setDraft(event.target.value)}
          onFocus={(event) => event.target.select()}
          onBlur={() => void commit()}
          onKeyDown={(event) => {
            const intent = renameKeyIntent({ key: event.key, isComposing: event.nativeEvent.isComposing, keyCode: event.keyCode });
            if (intent === 'save') { event.preventDefault(); void commit(); }
            if (intent === 'cancel') { event.preventDefault(); setDraft(null); }
          }}
          className="h-7 min-w-0 flex-1 rounded-md border border-ring bg-background px-2 text-[13px] text-foreground outline-none"
        />
        {/* The pencil keeps its slot while the field is open, so the menu arrow does not move. */}
        <button
          type="button"
          disabled
          aria-label={formatSchemaText(schemaText(schema, 'ui.common.label_value'), { label, value: session.title })}
          data-testid="studio-session-rename"
          className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-50"
        >
          <Pencil aria-hidden className="size-3" />
        </button>
      </span>
    );
  }
  return (
    <span className="flex w-72 max-w-full min-w-0 items-center gap-1">
      <HintTooltip name={session.title} className="min-w-0 flex-1">
        <span
          data-testid="studio-session-name"
          className="block h-7 w-full truncate rounded-md px-2 text-[13px] font-medium leading-7 text-foreground"
        >
          {session.title}
        </span>
      </HintTooltip>
      <HintTooltip name={label} className="shrink-0">
        <button
          type="button"
          onClick={() => setDraft(session.title)}
          aria-label={formatSchemaText(schemaText(schema, 'ui.common.label_value'), { label, value: session.title })}
          data-testid="studio-session-rename"
          className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        >
          <Pencil aria-hidden className="size-3" />
        </button>
      </HintTooltip>
    </span>
  );
}

/** The newest output of a session as a small cover; a placeholder until it loads or when there is none. */
function SessionCover({ session }: { session: ChatSession }) {
  const { proxyAssetUrl } = useAssetURLRewriter();
  const cover = useSessionCover(session.id, session.updatedAt);
  const src = cover ? proxyAssetUrl(cover.url, cover.taskId) : '';
  return (
    <span className="flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-md bg-muted" data-testid="studio-session-cover" data-has-cover={src !== ''}>
      {src ? <img src={src} alt={''} loading="lazy" className="size-full object-cover" /> : <ImageIcon aria-hidden className="size-4 text-muted-foreground" />}
    </span>
  );
}

/** Session list: search, switch, new, delete with an in-row confirmation, and one page of sessions at a time. */
function SessionList({ onClose }: { onClose: () => void }) {
  const { schema } = useProductSchema();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const localSessions = useChatStore((state) => state.sessions);
  const revision = useChatStore((state) => state.sessionListRevision);
  const { mutate: mutateCache } = useSWRConfig();
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [confirming, setConfirming] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const deletingRef = useRef(false);
  const deleteTrigger = useRef<HTMLButtonElement | null>(null);
  const confirmButton = useRef<HTMLButtonElement | null>(null);
  const [page, setPage] = useState(0);
  const { session_menu_page_size: pageSize, session_menu_search_debounce_milliseconds: debounce } = schema.runtime.frontend;
  const listRef = useRef<HTMLUListElement>(null);

  useEffect(() => {
    const timer = setTimeout(() => setQuery(search.trim()), debounce);
    return () => clearTimeout(timer);
  }, [search, debounce]);

  // A new search starts again from its first page.
  useEffect(() => setPage(0), [query]);

  // Turning pages must not read the first page again, so SWR's own first-page revalidation is off; the effect
  // below reads the list again when it opens, when the search changes and when the sessions change.
  const { data, error, isValidating, size, setSize, mutate } = useSWRInfinite<SessionPage>(
    (index, previous) => sessionPageKey(index, previous, query),
    ([, term, cursor]) => chat.listSessionPage({ search: String(term), cursor: String(cursor), limit: pageSize }),
    { revalidateOnFocus: false, revalidateFirstPage: false },
  );
  const sessions = useMemo(() => mergeSessionPages(data), [data]);
  const view = sessionPageView(data, page);
  const paging = sessionPageStatus(data, page, pageSize);
  const failed = error !== undefined;
  // A missing or failed read is not an empty result: only a successful read may show the numbers.
  const pagerText = failed
    ? schemaText(schema, 'ui.studio.session.load_failed')
    : data === undefined
      ? schemaText(schema, 'ui.studio.session.loading_more')
      : schemaCountMessage(schema, query === '' ? 'ui.studio.session.pagination_summary' : 'ui.studio.session.pagination_matches', paging.total, { page: paging.page, pages: paging.pages, total: paging.total });

  // Read by the refresh below without making it run again when only these change.
  const shown = useRef({ page, cached: false });
  useEffect(() => {
    shown.current = { page, cached: data !== undefined };
  });

  // Earlier reads are never trusted as current: on opening, on a new search and after a session was created,
  // renamed, deleted, retitled or moved by a turn, every cached page (of every search) is dropped and the pages up
  // to the one shown are read again. Within one opening a page already read is reused when turned back to.
  const refreshedFor = useRef<SessionListRefreshPoint | null>(null);
  useEffect(() => {
    const previous = refreshedFor.current;
    if (previous?.query === query && previous.revision === revision) return;
    const next = { query, revision };
    const size = sessionListRefreshSize(previous, next, shown.current);
    refreshedFor.current = next;
    void forgetSessionPages(mutateCache).then(() => {
      if (size > 0) void setSize(size);
    });
  }, [query, revision, mutateCache, setSize]);

  // A failed delete disables the confirm button while the request runs; once it is available again the
  // confirmation takes focus back, so Escape still belongs to the row.
  useEffect(() => {
    if (confirming === null || deleting !== null) return;
    // Only take focus back when the request left it on the page body (a disabled button cannot keep it).
    // A person who moved to search or elsewhere during the request keeps their focus.
    const active = document.activeElement;
    if (active instanceof HTMLElement && active !== document.body && !active.closest('[data-testid="studio-session-delete-confirm"]')) return;
    confirmButton.current?.focus();
  }, [confirming, deleting]);

  // Stay on a page that still exists after a delete or refresh shortens the list.
  useEffect(() => {
    const clamped = clampSessionPage(data, page);
    if (clamped !== page) setPage(clamped);
  }, [data, page]);

  // Show each page from its top.
  useEffect(() => {
    listRef.current?.scrollTo({ top: 0 });
  }, [page]);

  // Pages load only when the person turns to them; a page already loaded is shown from memory.
  const turn = (target: number) => {
    setPage(target);
    if (target + 1 > size) void setSize(target + 1);
  };

  const open = (session: ChatSession) => {
    if (session.id !== activeSessionId) {
      useWorkspaceStore.getState().clearTrackedTasks();
      if (localSessions.some((item) => item.id === session.id)) useChatStore.getState().setActiveSession(session.id);
      else useChatStore.getState().addSession(session);
    }
    onClose();
  };

  const create = async () => {
    try {
      const session = await chat.createSession();
      useWorkspaceStore.getState().clearTrackedTasks();
      useChatStore.getState().addSession(session);
      onClose();
    } catch {
      toast.error(schemaText(schema, 'ui.workspace.agent.session_failed'));
    }
  };

  // Escape inside the confirmation cancels it and returns focus to the button that opened it. The listener is
  // on the row, not the window, so search, rename and the unconfirmed list keep their own keys.
  const cancelDelete = () => {
    setConfirming(null);
    const trigger = deleteTrigger.current;
    deleteTrigger.current = null;
    trigger?.focus();
  };

  const remove = async (session: ChatSession) => {
    // The button's disabled state lands on the next render; a second click or Enter in the same tick must not
    // send a second delete.
    if (deletingRef.current) return;
    deletingRef.current = true;
    setDeleting(session.id);
    try {
      await chat.deleteSession(session.id);
      const wasActive = session.id === useChatStore.getState().activeSessionId;
      // Removing it from the store bumps the list revision, which reads the list again.
      useChatStore.getState().removeSession(session.id);
      setConfirming(null);
      deleteTrigger.current = null;
      if (wasActive) {
        const next = sessions.find((item) => item.id !== session.id);
        if (next) open(next);
        else await create();
      }
    } catch {
      // Keep the confirmation and its rows so the same button can be tried again.
      toast.error(schemaText(schema, 'ui.studio.session.delete_failed'));
    } finally {
      deletingRef.current = false;
      setDeleting(null);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-1.5 overflow-hidden">
      <label className="flex h-8 shrink-0 items-center gap-2 rounded-md border border-input bg-background px-2 text-muted-foreground focus-within:border-ring">
        <Search aria-hidden className="size-3.5 shrink-0" />
        <span className="sr-only">{schemaText(schema, 'ui.studio.session.search')}</span>
        <input
          type="search"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={schemaText(schema, 'ui.studio.session.search')}
          autoFocus
          className="min-w-0 flex-1 bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground"
        />
      </label>
      <Action type="button" variant="ghost" size="sm" onClick={() => void create()} className="shrink-0 justify-start text-xs" data-testid="studio-session-new">
        <Plus aria-hidden />
        {schemaText(schema, 'ui.studio.session.new')}
      </Action>
      <ul ref={listRef} className="-mx-1 min-h-0 overflow-y-auto" aria-label={schemaText(schema, 'ui.studio.session.menu_label')}>
        {view.sessions.map((session) => {
          const active = session.id === activeSessionId;
          const asking = confirming === session.id;
          return (
            <li key={session.id} className={cn('rounded-md px-2 py-1', active && 'bg-primary/15')} data-testid="studio-session-row">
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  onClick={() => open(session)}
                  aria-current={active || undefined}
                  className="flex min-w-0 flex-1 items-center gap-2 rounded-md py-0.5 text-left text-xs hover:text-primary focus-visible:outline-2 focus-visible:outline-ring"
                >
                  <SessionCover session={session} />
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className={cn('truncate', active && 'font-medium')}>{session.title}</span>
                    <HintTooltip name={new Date(session.updatedAt).toLocaleString(activeLocale(schema))} className="min-w-0">
                      <span className="truncate text-[11px] text-muted-foreground" data-testid="studio-session-time">
                        {relativeTime(schema.canvas, session.updatedAt)}
                      </span>
                    </HintTooltip>
                  </span>
                </button>
                {active ? <Check aria-label={schemaText(schema, 'ui.studio.session.current')} className="size-3.5 shrink-0 text-primary" /> : null}
                <HintTooltip name={schemaText(schema, 'ui.studio.session.delete')} className="shrink-0">
                  <Action
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    aria-label={formatSchemaText(schemaText(schema, 'ui.common.label_value'), { label: schemaText(schema, 'ui.studio.session.delete'), value: session.title })}
                    disabled={deleting !== null}
                    onClick={(event) => {
                      if (deletingRef.current) return;
                      deleteTrigger.current = event.currentTarget;
                      setConfirming(session.id);
                    }}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <Trash2 />
                  </Action>
                </HintTooltip>
              </div>
              {asking ? (
                <div
                  role="alert"
                  data-testid="studio-session-delete-confirm"
                  onKeyDown={(event) => {
                    const intent = deleteConfirmKeyIntent({
                      key: event.key,
                      isComposing: event.nativeEvent.isComposing,
                      keyCode: event.keyCode,
                      repeat: event.repeat,
                    });
                    if (intent !== 'cancel') return;
                    // Escape belongs to the confirmation: it must not also close the popover and steal focus.
                    event.preventDefault();
                    event.stopPropagation();
                    if (deleting !== session.id) cancelDelete();
                  }}
                  className="mt-1 flex flex-col gap-1.5 text-xs"
                >
                  <span className="text-warning">{formatSchemaText(schemaText(schema, 'ui.studio.session.delete_confirm'), { title: session.title })}</span>
                  <span className="flex gap-1.5">
                    <Action
                      ref={confirmButton}
                      type="button"
                      variant="destructive"
                      size="xs"
                      autoFocus
                      loading={deleting === session.id}
                      data-testid="studio-session-delete-confirm-action"
                      onClick={() => void remove(session)}
                      onKeyDown={(event) => {
                        // Enter on this button is the button's own click; composition and key repeat must not
                        // send a second delete.
                        if (event.key !== 'Enter') return;
                        if (deleteConfirmKeyIntent({ key: event.key, isComposing: event.nativeEvent.isComposing, keyCode: event.keyCode, repeat: event.repeat }) !== 'confirm') {
                          event.preventDefault();
                        }
                      }}
                    >
                      {schemaText(schema, 'ui.studio.session.delete_action')}
                    </Action>
                    <Action type="button" variant="ghost" size="xs" disabled={deleting === session.id} onClick={cancelDelete}>{schemaText(schema, 'ui.studio.session.delete_keep')}</Action>
                  </span>
                </div>
              ) : null}
            </li>
          );
        })}
        {view.loaded && !isValidating && view.sessions.length === 0 && !view.hasPrevious ? (
          <li role="status" className="px-2 py-3 text-xs text-muted-foreground">{schemaText(schema, 'ui.studio.session.empty')}</li>
        ) : null}
        {failed ? (
          <li className="px-2 py-1" data-testid="studio-session-retry">
            <Action type="button" variant="ghost" size="xs" onClick={() => void mutate()} className="w-full justify-center text-destructive">
              <RotateCw aria-hidden />
              {schemaText(schema, 'ui.studio.session.load_failed')}
            </Action>
          </li>
        ) : !view.loaded || isValidating ? (
          <li role="status" className="flex items-center justify-center gap-2 px-2 py-3 text-xs text-muted-foreground" data-testid="studio-session-loading"><Loader2 className="size-3.5 animate-spin" aria-hidden />{schemaText(schema, 'ui.studio.session.loading_more')}</li>
        ) : null}
      </ul>
      <nav className="flex shrink-0 flex-col gap-1 border-t border-border pt-1.5" data-testid="studio-session-pager">
        <span className="w-full px-1 text-center text-[11px] leading-snug text-muted-foreground [overflow-wrap:anywhere]" aria-live="polite" data-testid="studio-session-page">
          {pagerText}
        </span>
        <span className="flex items-center justify-between gap-1">
          <Action type="button" variant="ghost" size="xs" disabled={!view.hasPrevious || failed} onClick={() => turn(page - 1)} data-testid="studio-session-previous">
            <ChevronLeft aria-hidden />
            {schemaText(schema, 'ui.studio.session.previous_page')}
          </Action>
          <Action type="button" variant="ghost" size="xs" disabled={!view.hasNext || failed} onClick={() => turn(page + 1)} data-testid="studio-session-next">
            {schemaText(schema, 'ui.studio.session.next_page')}
            <ChevronRight aria-hidden />
          </Action>
        </span>
      </nav>
    </div>
  );
}

export function SessionMenu() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const session = useChatStore((state) => state.sessions.find((item) => item.id === activeSessionId));
  const [open, setOpen] = useState(false);
  const layer = useAnchoredPositioner();

  // The active session may not be among the loaded pages (or the store): read it by id so the title shows.
  const missing = activeSessionId !== null && !session;
  useEffect(() => {
    if (!missing || activeSessionId === null) return;
    const controller = new AbortController();
    createChatBrowserClient(schema)
      .getSession(activeSessionId, controller.signal)
      .then((fetched) => {
        const state = useChatStore.getState();
        if (state.activeSessionId !== fetched.id || state.sessions.some((item) => item.id === fetched.id)) return;
        state.setSessions([fetched, ...state.sessions]);
      })
      .catch(() => {});
    return () => controller.abort();
  }, [missing, activeSessionId, schema]);
  if (!session) return null;
  return (
    <div className="flex min-w-0 items-center">
      <SessionTitle key={session.id} session={session} />
      <Popover.Root open={open} onOpenChange={setOpen}>
        <HintTooltip name={schemaText(schema, 'ui.studio.session.menu_label')} className="shrink-0">
          <Popover.Trigger
            aria-label={schemaText(schema, 'ui.studio.session.menu_label')}
            data-testid="studio-session-menu"
            className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:bg-primary/15 data-popup-open:text-primary"
          >
            <ChevronDown aria-hidden className="size-3.5" />
          </Popover.Trigger>
        </HintTooltip>
        <Popover.Portal>
          <Popover.Positioner side="bottom" align="start" sideOffset={6} collisionPadding={12} className={layer}>
            <Popover.Popup className="flex max-h-[min(32rem,calc(100dvh-6rem))] w-80 max-w-(--available-width) flex-col overflow-hidden rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10">
              <Popover.Title className="sr-only">{schemaText(schema, 'ui.studio.session.menu_label')}</Popover.Title>
              <SessionList onClose={() => setOpen(false)} />
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
    </div>
  );
}
