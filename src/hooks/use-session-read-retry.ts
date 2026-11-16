'use client';

import { useCallback, useEffect, useRef } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import type { ChatBrowserClient } from '@/lib/chat/browser-client';
import { startSessionReadRetry, type SessionReadRetry } from '@/lib/chat/session-read-retry';

/**
 * #314: reads one per-session value (the session's chat model, its thinking level) when the session opens and keeps
 * trying while the read fails: a bounded number of times with backoff, then once more each time the window regains
 * focus or the network comes back, as long as the last read failed. The Schema declares the bounds
 * (`runtime.frontend.session_read_retry`). The read is cancelled when the session changes or the page leaves, and a
 * cancelled read marks nothing as failed. `read` is a store load function and `failed` its store flag, both module
 * level so the effect only starts again for another session or client (a revalidated Schema). Returns `retry`, for a
 * person who asks to read again.
 */
export function useSessionReadRetry({ client, sessionId, supported, read, failed }: {
  client: ChatBrowserClient;
  sessionId: string | null;
  supported: boolean;
  read: (client: ChatBrowserClient, sessionId: string, signal?: AbortSignal) => Promise<void>;
  failed: (sessionId: string) => boolean;
}): { retry: () => void } {
  const { schema } = useProductSchema();
  const policy = schema.runtime.frontend.session_read_retry;
  const running = useRef<SessionReadRetry | null>(null);

  useEffect(() => {
    if (!supported || !sessionId) return;
    const started = startSessionReadRetry({
      read: (signal) => read(client, sessionId, signal),
      failed: () => failed(sessionId),
      policy,
    });
    running.current = started;
    return () => {
      started.stop();
      if (running.current === started) running.current = null;
    };
  }, [client, sessionId, supported, read, failed, policy]);

  const retry = useCallback(() => running.current?.retry(), []);
  return { retry };
}
