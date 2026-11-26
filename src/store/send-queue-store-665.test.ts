import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { cancelQueuedTurn, enqueueTurn, type QueuedTurn } from '@/lib/chat/send-queue';
import { useChatStore } from './chat-store';
import { reloadSendQueue, useSendQueueStore } from './send-queue-store';

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

  it('live for the page when storage is blocked', () => {
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, get: () => { throw new Error('blocked'); } });
    reloadSendQueue();
    expect(() => useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')))).not.toThrow();
    expect(useSendQueueStore.getState().queue.s1).toHaveLength(1);
  });
});
