import { useChatStore, type StreamingBuffer } from '@/store/chat-store';
import type { ChatBrowserClient } from './browser-client';

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
}

/** Owns only the load/subscription for this selected session, until effect cleanup. */
export function recoverChatSession(options: SessionRecoveryOptions): () => void {
  const { sessionId, client, pageSize, connect, pollMilliseconds, onLoading, onLoadError } = options;
  const controller = new AbortController();
  let stopped = false;
  let closeStream: (() => void) | undefined;
  let ownedBuffer: StreamingBuffer | undefined;
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  const current = () => !stopped && useChatStore.getState().activeSessionId === sessionId;
  const streaming = () => !!useChatStore.getState().streamingBuffers[sessionId];
  const cached = sessionId in useChatStore.getState().messagesBySession;

  const finishOwnedStream = () => {
    if (ownedBuffer && useChatStore.getState().streamingBuffers[sessionId]) {
      useChatStore.getState().finishStreaming(sessionId);
    }
    ownedBuffer = undefined;
  };

  const schedulePoll = () => {
    if (!current() || pollTimer !== undefined) return;
    pollTimer = setTimeout(() => {
      pollTimer = undefined;
      void load(false);
    }, pollMilliseconds);
  };

  const reconnect = () => {
    if (!current() || streaming()) return;
    useChatStore.getState().initStreaming(sessionId, { content: '' });
    ownedBuffer = useChatStore.getState().streamingBuffers[sessionId];
    closeStream = connect(sessionId, {
      fromId: 0,
      onDone: () => {
        if (!current()) return;
        finishOwnedStream();
        void load(false);
      },
      onError: () => {
        if (!current()) return;
        closeStream?.();
        finishOwnedStream();
        schedulePoll();
      },
    });
  };

  async function load(tryStream: boolean) {
    if (!current() || streaming()) return;
    try {
      const page = await client.listMessagePage(sessionId, { limit: pageSize }, controller.signal);
      // AbortSignal alone cannot protect already-resolved responses or custom clients.
      if (!current() || streaming()) return;
      useChatStore.getState().applyNewestMessagePage(sessionId, page);
      if (page.messages.some((message) => message.isPending)) {
        if (tryStream) reconnect();
        else schedulePoll();
      }
    } catch {
      if (!current()) return;
      if (tryStream && !cached) onLoadError();
      if (!tryStream) schedulePoll();
    } finally {
      if (current()) onLoading(false);
    }
  }

  onLoading(!cached && !streaming());
  void load(true);
  return () => {
    stopped = true;
    controller.abort();
    if (pollTimer !== undefined) clearTimeout(pollTimer);
    closeStream?.();
    finishOwnedStream();
  };
}
