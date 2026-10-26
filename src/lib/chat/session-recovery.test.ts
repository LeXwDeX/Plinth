import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useChatStore, type ChatMessage } from '@/store/chat-store';
import { recoverChatSession } from './session-recovery';

const pending: ChatMessage[] = [{ id: 'm', role: 'assistant', content: '', isPending: true, createdAt: 1 }];
const complete: ChatMessage[] = [{ id: 'm', role: 'assistant', content: 'finished image', createdAt: 1 }];

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function fixture(sessionId = 'session-a') {
  const callbacks = { onDone: () => {}, onError: () => {} };
  const closed = vi.fn();
  const listMessages = vi.fn<(id: string, signal?: AbortSignal) => Promise<ChatMessage[]>>();
  const connect = vi.fn((_id: string, options: { onDone(): void; onError(): void }) => {
    Object.assign(callbacks, options);
    return closed;
  });
  const onLoading = vi.fn();
  const onLoadError = vi.fn();
  // The recoverer reads only the newest page; each fixture response is a whole conversation.
  const pageQueries: Array<{ before?: string; limit: number }> = [];
  const listMessagePage = (id: string, query: { before?: string; limit: number }, signal?: AbortSignal) => {
    pageQueries.push(query);
    return listMessages(id, signal).then((messages) => ({ messages, nextCursor: '' }));
  };
  const start = () => recoverChatSession({
    sessionId, client: { listMessagePage }, pageSize: 50, connect, pollMilliseconds: 500,
    onLoading, onLoadError,
  });
  return { start, callbacks, closed, listMessages, pageQueries, connect, onLoading, onLoadError };
}

describe('history session recovery', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useChatStore.setState({ activeSessionId: 'session-a', messagesBySession: {}, streamingBuffers: {} });
  });
  afterEach(() => vi.useRealTimers());

  it('waits for a one-second message load, subscribes once, and reloads the completed result', async () => {
    const f = fixture();
    const loading = deferred<ChatMessage[]>();
    f.listMessages.mockReturnValueOnce(loading.promise).mockResolvedValue(complete);
    const stop = f.start();
    await vi.advanceTimersByTimeAsync(1000);
    expect(f.connect).not.toHaveBeenCalled();
    loading.resolve(pending);
    await vi.advanceTimersByTimeAsync(0);
    expect(f.connect).toHaveBeenCalledTimes(1);
    expect(useChatStore.getState().streamingBuffers['session-a']).toBeDefined();
    f.callbacks.onDone();
    await vi.advanceTimersByTimeAsync(0);
    expect(useChatStore.getState().messagesBySession['session-a']).toEqual(complete);
    expect(useChatStore.getState().streamingBuffers['session-a']).toBeUndefined();
    expect(f.listMessages).toHaveBeenCalledTimes(2);
    stop();
  });

  it('reads only the newest page and keeps earlier pages the person already loaded', async () => {
    const earlier: ChatMessage = { id: 'e', role: 'user', content: 'earlier', createdAt: 0 };
    useChatStore.setState({
      messagesBySession: { 'session-a': [earlier, ...pending] },
      olderCursorBySession: { 'session-a': 'cursor-before-e' },
    });
    const f = fixture();
    const listMessagePage = vi.fn(async () => ({ messages: complete, nextCursor: 'cursor-before-m' }));
    const stop = recoverChatSession({
      sessionId: 'session-a', client: { listMessagePage }, pageSize: 50, connect: f.connect,
      pollMilliseconds: 500, onLoading: f.onLoading, onLoadError: f.onLoadError,
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(listMessagePage).toHaveBeenCalledWith('session-a', { limit: 50 }, expect.any(AbortSignal));
    expect(useChatStore.getState().messagesBySession['session-a']).toEqual([earlier, ...complete]);
    expect(useChatStore.getState().olderCursorBySession['session-a']).toBe('cursor-before-e');
    stop();
  });

  it('discards a delayed old session response even if the client ignores abort', async () => {
    const a = fixture();
    const loading = deferred<ChatMessage[]>();
    a.listMessages.mockReturnValue(loading.promise);
    const stopA = a.start();
    const signal = a.listMessages.mock.calls[0][1];
    stopA();
    useChatStore.setState({ activeSessionId: 'session-b' });
    const b = fixture('session-b');
    b.listMessages.mockResolvedValue(complete);
    const stopB = b.start();
    loading.resolve(pending);
    await vi.advanceTimersByTimeAsync(0);
    expect(signal?.aborted).toBe(true);
    expect(a.connect).not.toHaveBeenCalled();
    expect(useChatStore.getState().messagesBySession['session-a']).toBeUndefined();
    expect(useChatStore.getState().messagesBySession['session-b']).toEqual(complete);
    expect(a.onLoading.mock.calls).toEqual([[true]]);
    stopB();
  });

  it('closes only its recovered stream when switching, and can recover again on return', async () => {
    const f = fixture();
    f.listMessages.mockResolvedValue(pending);
    const stop = f.start();
    await vi.advanceTimersByTimeAsync(0);
    stop();
    expect(f.closed).toHaveBeenCalledOnce();
    expect(useChatStore.getState().streamingBuffers['session-a']).toBeUndefined();
    const stopAgain = f.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.connect).toHaveBeenCalledTimes(2);
    stopAgain();
  });

  it('polls after SSE failure without overlapping requests and stops on completion', async () => {
    const f = fixture();
    const poll = deferred<ChatMessage[]>();
    f.listMessages.mockResolvedValueOnce(pending).mockReturnValueOnce(poll.promise);
    const stop = f.start();
    await vi.advanceTimersByTimeAsync(0);
    f.callbacks.onError();
    await vi.advanceTimersByTimeAsync(5000);
    expect(f.listMessages).toHaveBeenCalledTimes(2);
    poll.resolve(complete);
    await vi.advanceTimersByTimeAsync(5000);
    expect(f.listMessages).toHaveBeenCalledTimes(2);
    expect(useChatStore.getState().messagesBySession['session-a']).toEqual(complete);
    stop();
  });

  it('protects a send that begins while messages are loading', async () => {
    const f = fixture();
    const loading = deferred<ChatMessage[]>();
    f.listMessages.mockReturnValue(loading.promise);
    const stop = f.start();
    useChatStore.getState().initStreaming('session-a', { content: 'new request' });
    loading.resolve(pending);
    await vi.advanceTimersByTimeAsync(0);
    expect(f.connect).not.toHaveBeenCalled();
    stop();
    expect(useChatStore.getState().streamingBuffers['session-a']?.userMessage?.content).toBe('new request');
  });

  it('loads the selected session while another session is streaming', async () => {
    useChatStore.getState().initStreaming('session-b', { content: 'background request' });
    const f = fixture();
    f.listMessages.mockResolvedValue(complete);
    const stop = f.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(useChatStore.getState().messagesBySession['session-a']).toEqual(complete);
    stop();
    expect(useChatStore.getState().streamingBuffers['session-b']).toBeDefined();
  });
});
