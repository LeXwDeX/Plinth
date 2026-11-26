import { pageGate, pollingAllowed } from '@/lib/polling';
import { useChatStore } from '@/store/chat-store';
import type { ChatBrowserClient } from './browser-client';

/** #665: every this many reads of a still pending reply, the stream is tried again instead of only reading. */
const STREAM_RETRY_EVERY = 5;
/** #665: the pause between reads doubles every STREAM_RETRY_EVERY reads, up to this many times the Schema's interval. */
const MAX_POLL_BACKOFF = 8;
/**
 * #665: a reply still pending after this many reads is no longer read on its own; `onGaveUp` lets the page say so and
 * offer to read again (opening the session again does too).
 */
export const MAX_PENDING_POLLS = 40;

interface SessionRecoveryOptions {
  sessionId: string;
  client: Pick<ChatBrowserClient, 'listMessagePage'>;
  /** How many of the newest messages to read; earlier ones are read only when asked for. */
  pageSize: number;
  connect: (sessionId: string, options: {
    fromId: number;
    onDone(): void;
    onError(): void;
  }) => () => void;
  pollMilliseconds: number;
  onLoading(loading: boolean): void;
  onLoadError(): void;
  /** #665: the reads of a reply that stays pending stopped (MAX_PENDING_POLLS); called once per recovery. */
  onGaveUp?(): void;
  /** #665: a read of the newest page was applied (the send queue waits for one after another tab's send). */
  onRead?(): void;
}

/** Owns only the load/subscription for this selected session, until effect cleanup. */
export function recoverChatSession(options: SessionRecoveryOptions): () => void {
  const { sessionId, client, pageSize, connect, pollMilliseconds, onLoading, onLoadError, onGaveUp, onRead } = options;
  const controller = new AbortController();
  let stopped = false;
  let closeStream: (() => void) | undefined;
  // #665: the turn number of the buffer this recovery started; only that buffer is ever finished or settled here.
  let ownedTurn: number | undefined;
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  const current = () => !stopped && useChatStore.getState().activeSessionId === sessionId;
  const streaming = () => !!useChatStore.getState().streamingBuffers[sessionId];
  const cached = sessionId in useChatStore.getState().messagesBySession;

  const finishOwnedStream = () => {
    if (ownedTurn !== undefined) useChatStore.getState().finishStreaming(sessionId, ownedTurn);
    ownedTurn = undefined;
  };

  // #385: a hidden tab or an ended login does not read; the read it owed is made once the page may read again.
  // #665: a reply that stays pending is read with a growing pause, the stream is tried again every few reads, and
  // the reads stop after a bound: a message left pending by a turn that no longer runs is not read for ever.
  let parked = false;
  let pendingPolls = 0;
  let gaveUp = false;
  const schedulePoll = () => {
    if (!current() || pollTimer !== undefined) return;
    if (pendingPolls >= MAX_PENDING_POLLS) {
      if (!gaveUp) {
        gaveUp = true;
        onGaveUp?.();
      }
      return;
    }
    const delay = pollMilliseconds * Math.min(2 ** Math.floor(pendingPolls / STREAM_RETRY_EVERY), MAX_POLL_BACKOFF);
    pendingPolls += 1;
    const retryStream = pendingPolls % STREAM_RETRY_EVERY === 0;
    pollTimer = setTimeout(() => {
      pollTimer = undefined;
      if (!pollingAllowed()) {
        parked = true;
        return;
      }
      void load(retryStream, true);
    }, delay);
  };
  const unsubscribeGate = pageGate.subscribe(() => {
    if (!parked || !pollingAllowed()) return;
    parked = false;
    void load(false);
  });

  // #177: a reconnected stream also ends by swapping the stored page in place of its buffer, so the
  // replayed turn never disappears for a frame.
  const settleOwnedStream = async () => {
    if (!current()) return;
    try {
      const page = await client.listMessagePage(sessionId, { limit: pageSize }, controller.signal);
      if (!current()) return;
      if (ownedTurn !== undefined && useChatStore.getState().streamingBuffers[sessionId]?.turn === ownedTurn) {
        useChatStore.getState().settleStreaming(sessionId, page, ownedTurn);
        ownedTurn = undefined;
      } else {
        useChatStore.getState().applyNewestMessagePage(sessionId, page);
      }
      onRead?.();
      if (page.messages.some((message) => message.isPending)) schedulePoll();
      else pendingPolls = 0;
    } catch {
      if (!current()) return;
      finishOwnedStream();
      schedulePoll();
    } finally {
      if (current()) onLoading(false);
    }
  };

  const reconnect = () => {
    if (!current() || streaming()) return;
    ownedTurn = useChatStore.getState().initStreaming(sessionId, { content: '' });
    try {
      closeStream = connect(sessionId, {
        fromId: 0,
        onDone: () => {
          if (!current()) return;
          void settleOwnedStream();
        },
        onError: () => {
          if (!current()) return;
          closeStream?.();
          finishOwnedStream();
          schedulePoll();
        },
      });
    } catch {
      // #665: a stream that cannot even be opened must not leave its buffer behind as a running reply.
      finishOwnedStream();
      schedulePoll();
    }
  };

  async function load(tryStream: boolean, polled = false) {
    if (!current() || streaming()) return;
    try {
      const page = await client.listMessagePage(sessionId, { limit: pageSize }, controller.signal);
      // AbortSignal alone cannot protect already-resolved responses or custom clients.
      if (!current() || streaming()) return;
      useChatStore.getState().applyNewestMessagePage(sessionId, page);
      onRead?.();
      if (page.messages.some((message) => message.isPending)) {
        if (tryStream) reconnect();
        else schedulePoll();
      } else {
        pendingPolls = 0;
      }
    } catch {
      if (!current()) return;
      if (tryStream && !polled && !cached) onLoadError();
      if (!tryStream || polled) schedulePoll();
    } finally {
      if (current()) onLoading(false);
    }
  }

  onLoading(!cached && !streaming());
  void load(true);
  return () => {
    stopped = true;
    unsubscribeGate();
    controller.abort();
    if (pollTimer !== undefined) clearTimeout(pollTimer);
    closeStream?.();
    finishOwnedStream();
  };
}
