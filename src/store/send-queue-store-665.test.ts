import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { cancelQueuedTurn, enqueueTurn, pruneQueuedSessions, queueMayDrain, type QueuedTurn } from '@/lib/chat/send-queue';
import type { ChatMessage } from './chat-store';
import { useChatStore } from './chat-store';
import { onSendQueueStorage, reloadSendQueue, SEND_QUEUE_KEY, useSendQueueStore } from './send-queue-store';

const turn = (id: string): QueuedTurn => ({ id, text: `message ${id}`, images: [], claims: [], selection: [] });

describe('messages queued while a turn runs (#665)', () => {
  const memory = new Map<string, string>();
  const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  beforeEach(() => {
    memory.clear();
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => memory.get(key) ?? null,
        setItem: (key: string, value: string) => { memory.set(key, value); },
        removeItem: (key: string) => { memory.delete(key); },
      },
    });
    reloadSendQueue();
  });
  afterEach(() => {
    if (original) Object.defineProperty(globalThis, 'localStorage', original);
    else delete (globalThis as { localStorage?: unknown }).localStorage;
  });

  it('survive a reload of the page', () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    useSendQueueStore.setState({ queue: {} });
    reloadSendQueue();
    expect(useSendQueueStore.getState().queue.s1?.map((item) => item.id)).toEqual(['q1']);
    useSendQueueStore.getState().update((queue) => cancelQueuedTurn(queue, 's1', 'q1'));
    expect(memory.has('plinth.queuedTurns')).toBe(false);
  });

  it('leave with their session when it is deleted, with its draft', () => {
    vi.stubGlobal('window', { localStorage: globalThis.localStorage });
    memory.set('artai.composer.draft.s1', '{"text":"draft"}');
    memory.set('studio.last-object.s1', '{}');
    useSendQueueStore.getState().update((queue) => enqueueTurn(enqueueTurn(queue, 's1', turn('q1')), 's2', turn('q2')));
    useChatStore.getState().removeSession('s1');
    expect(Object.keys(useSendQueueStore.getState().queue)).toEqual(['s2']);
    reloadSendQueue();
    expect(Object.keys(useSendQueueStore.getState().queue)).toEqual(['s2']);
    expect(memory.has('artai.composer.draft.s1')).toBe(false);
    expect(memory.has('studio.last-object.s1')).toBe(false);
    vi.unstubAllGlobals();
  });

  it('follow another tab: its sent turns leave this tab, and a change here keeps the other tab\'s turns', () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    // Another tab queues q2 and sends q1: storage changes under this tab.
    memory.set(SEND_QUEUE_KEY, JSON.stringify({ s1: [turn('q2')] }));
    onSendQueueStorage({ key: SEND_QUEUE_KEY });
    expect(useSendQueueStore.getState().queue.s1?.map((item) => item.id)).toEqual(['q2']);
    // A change made here before the event arrives still starts from the stored queue, not from this tab's copy.
    memory.set(SEND_QUEUE_KEY, JSON.stringify({ s1: [turn('q2'), turn('q3')] }));
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q4')));
    expect(JSON.parse(memory.get(SEND_QUEUE_KEY) ?? '{}').s1.map((item: QueuedTurn) => item.id)).toEqual(['q2', 'q3', 'q4']);
    onSendQueueStorage({ key: 'unrelated' });
    expect(useSendQueueStore.getState().queue.s1).toHaveLength(3);
  });

  it('are claimed once: a turn another tab already took is not sent again', () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    // The other tab claimed it first; this tab has not seen the storage event yet.
    memory.delete(SEND_QUEUE_KEY);
    expect(useSendQueueStore.getState().claim('s1', 'q1')).toBe(false);
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q2')));
    expect(useSendQueueStore.getState().claim('s1', 'q2')).toBe(true);
    expect(useSendQueueStore.getState().claim('s1', 'q2')).toBe(false);
    expect(memory.has(SEND_QUEUE_KEY)).toBe(false);
  });

  it('are cleared from the page and storage', () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    useSendQueueStore.getState().clear();
    expect(useSendQueueStore.getState().queue).toEqual({});
    expect(memory.has(SEND_QUEUE_KEY)).toBe(false);
  });

  it('leave with sessions the server no longer gives this account', async () => {
    useSendQueueStore.getState().update((queue) => ['s1', 'gone', 'foreign', 'offline'].reduce(
      (current, id) => enqueueTurn(current, id, turn(`q-${id}`)), queue));
    const getSession = vi.fn(async (id: string) => {
      if (id === 'gone') throw Object.assign(new Error('not found'), { status: 404 });
      if (id === 'foreign') throw Object.assign(new Error('forbidden'), { status: 403 });
      throw new Error('offline');
    });
    await pruneQueuedSessions(Object.keys(useSendQueueStore.getState().queue), new Set(['s1']), getSession,
      (id) => useSendQueueStore.getState().forgetSession(id));
    expect(Object.keys(useSendQueueStore.getState().queue).sort()).toEqual(['offline', 's1']);
    expect(getSession).not.toHaveBeenCalledWith('s1');
  });

  it('wait after a reload until the messages were read and no reply is still pending', () => {
    const pendingReply: ChatMessage = { id: 'm', role: 'assistant', content: '', isPending: true, createdAt: 1 };
    const doneReply: ChatMessage = { ...pendingReply, isPending: false };
    const idle = { busy: false, historyLoading: false, streaming: false };
    // Right after a reload the stream buffer is empty: only the read can say the server still runs a turn.
    expect(queueMayDrain({ ...idle, messages: undefined })).toBe(false);
    expect(queueMayDrain({ ...idle, historyLoading: true, messages: [doneReply] })).toBe(false);
    expect(queueMayDrain({ ...idle, messages: [pendingReply] })).toBe(false);
    expect(queueMayDrain({ ...idle, streaming: true, messages: [doneReply] })).toBe(false);
    expect(queueMayDrain({ ...idle, busy: true, messages: [doneReply] })).toBe(false);
    expect(queueMayDrain({ ...idle, messages: [doneReply] })).toBe(true);
  });

  it('live for the page when storage is blocked', () => {
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, get: () => { throw new Error('blocked'); } });
    reloadSendQueue();
    expect(() => useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')))).not.toThrow();
    expect(useSendQueueStore.getState().queue.s1).toHaveLength(1);
    expect(() => useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q2')))).not.toThrow();
    expect(useSendQueueStore.getState().queue.s1).toHaveLength(2);
    expect(useSendQueueStore.getState().claim('s1', 'q1')).toBe(true);
  });

  it('keep the page\'s queue when storage refuses writes', () => {
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: { getItem: () => null, setItem: () => { throw new Error('quota'); }, removeItem: () => undefined },
    });
    reloadSendQueue();
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q2')));
    expect(useSendQueueStore.getState().queue.s1?.map((item) => item.id)).toEqual(['q1', 'q2']);
  });
});
