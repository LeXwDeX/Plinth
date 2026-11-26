import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { cancelQueuedTurn, enqueueTurn, pruneQueuedSessions, queueMayDrain, type QueuedTurn } from '@/lib/chat/send-queue';
import type { ChatMessage } from './chat-store';
import { useChatStore } from './chat-store';
import { sendHoldKey } from '@/lib/chat/send-queue';
import { MAX_TURN_LOCK_REFUSALS, MAX_TURN_LOCK_RETRY_MS, SEND_HOLD_MS, TURN_LOCK_REFUSALS_END_MS, turnLockRetryDelay } from '@/lib/chat/send-queue';
import {
  onSendQueueStorage, refreshAllHolds, releaseAllHolds, reloadSendQueue, SEND_QUEUE_KEY, sweepLapsedHolds, useSendQueueStore,
} from './send-queue-store';

const turn = (id: string): QueuedTurn => ({ id, text: `message ${id}`, images: [], claims: [], selection: [] });

/** A LockManager for one browser: `held` names the locks some tab keeps; `ifAvailable` gets null for those. */
function fakeLocks(held = new Set<string>()) {
  const requested: string[] = [];
  const request = async (name: string, ...rest: unknown[]) => {
    const callback = rest[rest.length - 1] as (lock: { name: string } | null) => unknown;
    const options = (rest.length > 1 ? rest[0] : {}) as { ifAvailable?: boolean };
    requested.push(name);
    if (held.has(name)) {
      if (options.ifAvailable) return callback(null);
      throw new Error('a test lock is never waited for');
    }
    held.add(name);
    try {
      return await callback({ name });
    } finally {
      held.delete(name);
    }
  };
  return { held, requested, locks: { request } };
}
const settled = () => new Promise((done) => { setTimeout(done, 0); });
const turnLock = (sessionId: string) => `${sendHoldKey(sessionId)}:turn`;

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
    useSendQueueStore.setState({ foreignChanges: {} });
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

  it('are claimed once: a turn another tab already took is not sent again', async () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    // The other tab claimed it first; this tab has not seen the storage event yet.
    memory.delete(SEND_QUEUE_KEY);
    expect(await useSendQueueStore.getState().claim('s1', 'q1')).toBe(false);
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q2')));
    expect(await useSendQueueStore.getState().claim('s1', 'q2')).toBe(true);
    expect(await useSendQueueStore.getState().claim('s1', 'q2')).toBe(false);
    expect(memory.has(SEND_QUEUE_KEY)).toBe(false);
    useSendQueueStore.getState().releaseHold('s1');
    expect(memory.has(sendHoldKey('s1'))).toBe(false);
  });

  it('hold the session\'s send for the claiming tab, under a Web Lock when the browser has one', async () => {
    const { held, requested, locks } = fakeLocks();
    vi.stubGlobal('navigator', { locks });
    useSendQueueStore.getState().update((queue) => enqueueTurn(enqueueTurn(queue, 's1', turn('q1')), 's1', turn('q2')));
    expect(await useSendQueueStore.getState().claim('s1', 'q1')).toBe(true);
    expect(requested).toEqual([`${SEND_QUEUE_KEY}:claim`, turnLock('s1')]);
    // The turn lock stays taken while the turn runs, and goes with the release.
    expect(held.has(turnLock('s1'))).toBe(true);
    // This tab's own hold is not another tab's: it neither blocks this tab nor shows as foreign.
    expect(useSendQueueStore.getState().foreignHold('s1')).toBeNull();
    const own = JSON.parse(memory.get(sendHoldKey('s1')) ?? 'null');
    expect(own.expiresAt).toBeGreaterThan(Date.now());
    useSendQueueStore.getState().releaseHold('s1');
    await settled();
    expect(held.has(turnLock('s1'))).toBe(false);
    // Another tab takes the hold (say after this one lapsed): this tab now waits, and its claim is refused.
    memory.set(sendHoldKey('s1'), JSON.stringify({ owner: 'other-tab', expiresAt: Date.now() + 10_000 }));
    expect(useSendQueueStore.getState().foreignHold('s1')?.owner).toBe('other-tab');
    expect(await useSendQueueStore.getState().claim('s1', 'q2')).toBe(false);
    await settled();
    expect(held.has(turnLock('s1'))).toBe(false);
    // Releasing leaves another tab's hold alone.
    useSendQueueStore.getState().releaseHold('s1');
    expect(memory.has(sendHoldKey('s1'))).toBe(true);
    vi.unstubAllGlobals();
  });

  it('refuse a claim while another tab keeps the turn lock, though its stored hold lapsed while it was hidden', async () => {
    const { locks } = fakeLocks(new Set([turnLock('s1')]));
    vi.stubGlobal('navigator', { locks });
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    memory.set(sendHoldKey('s1'), JSON.stringify({ owner: 'hidden-tab', expiresAt: Date.now() - 1 }));
    expect(useSendQueueStore.getState().foreignHold('s1')).toBeNull();
    expect(await useSendQueueStore.getState().claim('s1', 'q1')).toBe(false);
    // The turn stays queued, and the drain waits a while before it tries again instead of reading at once.
    expect(useSendQueueStore.getState().queue.s1?.map((item) => item.id)).toEqual(['q1']);
    const wait = useSendQueueStore.getState().foreignHold('s1');
    expect(wait?.expiresAt).toBeGreaterThan(Date.now());
    expect(wait!.expiresAt - Date.now()).toBeLessThanOrEqual(SEND_HOLD_MS / 3);
    vi.unstubAllGlobals();
  });

  it('back off from a turn lock a frozen tab keeps, then count the stored hold alone, until the session is forgotten', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    const { locks } = fakeLocks(new Set([turnLock('f')]));
    vi.stubGlobal('navigator', { locks });
    const store = () => useSendQueueStore.getState();
    store().update((queue) => enqueueTurn(enqueueTurn(queue, 'f', turn('f1')), 'f', turn('f2')));
    const waits: number[] = [];
    for (let refusal = 0; refusal < MAX_TURN_LOCK_REFUSALS; refusal += 1) {
      expect(await store().claim('f', 'f1')).toBe(false);
      const wait = store().foreignHold('f')!.expiresAt - Date.now();
      waits.push(wait);
      vi.setSystemTime(Date.now() + wait);
      expect(store().foreignHold('f')).toBeNull();
    }
    // The wait grows with each refusal in a row, up to its cap.
    expect(waits).toEqual(Array.from({ length: MAX_TURN_LOCK_REFUSALS }, (_, index) => turnLockRetryDelay(index + 1)));
    expect(waits[0]).toBe(SEND_HOLD_MS / 3);
    expect(Math.max(...waits)).toBe(MAX_TURN_LOCK_RETRY_MS);
    // Refused too often: the lock no longer blocks the session, the stored hold does.
    memory.set(sendHoldKey('f'), JSON.stringify({ owner: 'other-tab', expiresAt: Date.now() + 10_000 }));
    expect(await store().claim('f', 'f1')).toBe(false);
    expect(store().foreignHold('f')?.owner).toBe('other-tab');
    memory.delete(sendHoldKey('f'));
    expect(await store().claim('f', 'f1')).toBe(true);
    store().releaseHold('f');
    // Forgetting the session ends the count: a new refusal waits the first delay again.
    store().forgetSession('f');
    store().update((queue) => enqueueTurn(queue, 'f', turn('f3')));
    expect(await store().claim('f', 'f3')).toBe(false);
    expect(store().foreignHold('f')!.expiresAt - Date.now()).toBe(SEND_HOLD_MS / 3);
    // Clearing the queue (a logout) ends it too.
    store().clear();
    expect(store().foreignHold('f')).toBeNull();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('end a refusal count whose last wait ended long ago: a new episode starts from the first wait', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    const { locks } = fakeLocks(new Set([turnLock('e')]));
    vi.stubGlobal('navigator', { locks });
    const store = () => useSendQueueStore.getState();
    store().update((queue) => enqueueTurn(queue, 'e', turn('e1')));
    for (let refusal = 0; refusal < MAX_TURN_LOCK_REFUSALS; refusal += 1) {
      expect(await store().claim('e', 'e1')).toBe(false);
      vi.setSystemTime(store().foreignHold('e')!.expiresAt);
    }
    // Counted out: the lock no longer blocks, the stored hold of the other tab does.
    memory.set(sendHoldKey('e'), JSON.stringify({ owner: 'other-tab', expiresAt: Date.now() + 10_000 }));
    expect(await store().claim('e', 'e1')).toBe(false);
    expect(store().foreignHold('e')?.owner).toBe('other-tab');
    memory.delete(sendHoldKey('e'));
    // Long after the last wait the count is over: the lock blocks again, with the first wait.
    vi.setSystemTime(Date.now() + TURN_LOCK_REFUSALS_END_MS + 1);
    expect(await store().claim('e', 'e1')).toBe(false);
    expect(store().foreignHold('e')!.expiresAt - Date.now()).toBe(turnLockRetryDelay(1));
    store().forgetSession('e');
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('end the refusal count once the turn lock is free again', async () => {
    const { held, locks } = fakeLocks(new Set([turnLock('g')]));
    vi.stubGlobal('navigator', { locks });
    useSendQueueStore.getState().update((queue) => enqueueTurn(enqueueTurn(queue, 'g', turn('g1')), 'g', turn('g2')));
    expect(await useSendQueueStore.getState().claim('g', 'g1')).toBe(false);
    expect(await useSendQueueStore.getState().claim('g', 'g1')).toBe(false);
    held.delete(turnLock('g'));
    expect(await useSendQueueStore.getState().claim('g', 'g1')).toBe(true);
    useSendQueueStore.getState().releaseHold('g');
    await settled();
    held.add(turnLock('g'));
    expect(await useSendQueueStore.getState().claim('g', 'g2')).toBe(false);
    const wait = useSendQueueStore.getState().foreignHold('g')!.expiresAt - Date.now();
    expect(wait).toBeLessThanOrEqual(SEND_HOLD_MS / 3);
    vi.unstubAllGlobals();
  });

  it('keep a hold per session, and release each on its own or with its session', async () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(enqueueTurn(queue, 'a', turn('qa')), 'b', turn('qb')));
    expect(await useSendQueueStore.getState().claim('a', 'qa')).toBe(true);
    expect(await useSendQueueStore.getState().claim('b', 'qb')).toBe(true);
    expect(useSendQueueStore.getState().heldSessions().sort()).toEqual(['a', 'b']);
    useSendQueueStore.getState().releaseHold('a');
    expect(memory.has(sendHoldKey('a'))).toBe(false);
    expect(memory.has(sendHoldKey('b'))).toBe(true);
    useSendQueueStore.getState().forgetSession('b');
    expect(memory.has(sendHoldKey('b'))).toBe(false);
    expect(useSendQueueStore.getState().heldSessions()).toEqual([]);
  });

  it('refresh a hold on a heartbeat and when shown again, but never write over another tab\'s hold', async () => {
    vi.useFakeTimers();
    try {
      useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
      expect(await useSendQueueStore.getState().claim('s1', 'q1')).toBe(true);
      const first = JSON.parse(memory.get(sendHoldKey('s1')) ?? 'null').expiresAt;
      vi.advanceTimersByTime(SEND_HOLD_MS / 3);
      expect(JSON.parse(memory.get(sendHoldKey('s1')) ?? 'null').expiresAt).toBe(first + SEND_HOLD_MS / 3);
      vi.setSystemTime(Date.now() + 1_000);
      refreshAllHolds();
      expect(JSON.parse(memory.get(sendHoldKey('s1')) ?? 'null').expiresAt).toBe(Date.now() + SEND_HOLD_MS);
      // This tab was hidden past its hold, and another tab claimed: the refresh gives the hold up instead.
      const other = JSON.stringify({ owner: 'other-tab', expiresAt: Date.now() + SEND_HOLD_MS });
      memory.set(sendHoldKey('s1'), other);
      const changes = useSendQueueStore.getState().foreignChanges.s1 ?? 0;
      vi.advanceTimersByTime(SEND_HOLD_MS / 3);
      expect(memory.get(sendHoldKey('s1'))).toBe(other);
      expect(useSendQueueStore.getState().heldSessions()).toEqual([]);
      // Losing it counts as another tab's change: this tab reads the session again before draining.
      expect(useSendQueueStore.getState().foreignChanges.s1).toBe(changes + 1);
      vi.advanceTimersByTime(SEND_HOLD_MS);
      expect(memory.get(sendHoldKey('s1'))).toBe(other);
    } finally {
      releaseAllHolds();
      vi.useRealTimers();
    }
  });

  it('sweep the lapsed and unreadable holds of other sessions on load, not live ones or this tab\'s', async () => {
    const storage = globalThis.localStorage as unknown as Record<string, unknown>;
    storage.key = (index: number) => [...memory.keys()][index] ?? null;
    Object.defineProperty(storage, 'length', { configurable: true, get: () => memory.size });
    const now = Date.now();
    memory.set(sendHoldKey('crashed'), JSON.stringify({ owner: 'gone-tab', expiresAt: now - 1 }));
    memory.set(sendHoldKey('corrupt'), JSON.stringify({ owner: 'x', expiresAt: 1e300 }));
    memory.set(sendHoldKey('live'), JSON.stringify({ owner: 'other-tab', expiresAt: now + 5_000 }));
    memory.set('unrelated', 'kept');
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 'mine', turn('q1')));
    expect(await useSendQueueStore.getState().claim('mine', 'q1')).toBe(true);
    // This tab's own hold, even lapsed, is its to refresh.
    sweepLapsedHolds(now + 2 * SEND_HOLD_MS);
    expect(memory.has(sendHoldKey('mine'))).toBe(true);
    expect(memory.has(sendHoldKey('live'))).toBe(false);
    releaseAllHolds();
    memory.set(sendHoldKey('live'), JSON.stringify({ owner: 'other-tab', expiresAt: now + 5_000 }));
    memory.set(sendHoldKey('crashed'), JSON.stringify({ owner: 'gone-tab', expiresAt: now - 1 }));
    sweepLapsedHolds(now);
    expect(memory.has(sendHoldKey('crashed'))).toBe(false);
    expect(memory.has(sendHoldKey('corrupt'))).toBe(false);
    expect(memory.has(sendHoldKey('live'))).toBe(true);
    expect(memory.get('unrelated')).toBe('kept');
  });

  it('send a claimed turn once even when storage refuses the hold', async () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(enqueueTurn(queue, 's1', turn('q1')), 's1', turn('q2')));
    const storage = globalThis.localStorage as unknown as { setItem: (key: string, value: string) => void };
    const setItem = storage.setItem;
    storage.setItem = (key, value) => {
      if (key === sendHoldKey('s1')) throw new Error('quota');
      setItem(key, value);
    };
    expect(await useSendQueueStore.getState().claim('s1', 'q1')).toBe(true);
    // The claimed turn left the page and storage (no other tab sends it again); the next one is still queued.
    expect(useSendQueueStore.getState().queue.s1?.map((item) => item.id)).toEqual(['q2']);
    expect(JSON.parse(memory.get(SEND_QUEUE_KEY) ?? '{}').s1.map((item: QueuedTurn) => item.id)).toEqual(['q2']);
  });

  it('count another tab\'s hold changes per session, so the page reads the session again', () => {
    const before = useSendQueueStore.getState().foreignChanges.s9 ?? 0;
    onSendQueueStorage({ key: sendHoldKey('s9') });
    onSendQueueStorage({ key: sendHoldKey('s9') });
    expect(useSendQueueStore.getState().foreignChanges.s9).toBe(before + 2);
    expect(useSendQueueStore.getState().foreignChanges.s1 ?? 0).toBe(0);
  });

  it('are cleared from the page and storage', () => {
    useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')));
    useSendQueueStore.getState().clear();
    expect(useSendQueueStore.getState().queue).toEqual({});
    expect(memory.has(SEND_QUEUE_KEY)).toBe(false);
  });

  it('leave with sessions the server no longer gives this account', async () => {
    useSendQueueStore.getState().update((queue) => ['s1', 'gone', 'gateway', 'offline'].reduce(
      (current, id) => enqueueTurn(current, id, turn(`q-${id}`)), queue));
    const getSession = vi.fn(async (id: string) => {
      if (id === 'gone') throw Object.assign(new Error('not found'), { status: 404, serverAnswered: true });
      // A proxy's 404 page during a deploy is not the server saying the session is gone.
      if (id === 'gateway') throw Object.assign(new Error('not found'), { status: 404, serverAnswered: false });
      throw new Error('offline');
    });
    await pruneQueuedSessions(Object.keys(useSendQueueStore.getState().queue), new Set(['s1']), getSession,
      (id) => useSendQueueStore.getState().forgetSession(id));
    expect(Object.keys(useSendQueueStore.getState().queue).sort()).toEqual(['gateway', 'offline', 's1']);
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

  it('live for the page when storage is blocked', async () => {
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, get: () => { throw new Error('blocked'); } });
    reloadSendQueue();
    expect(() => useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q1')))).not.toThrow();
    expect(useSendQueueStore.getState().queue.s1).toHaveLength(1);
    expect(() => useSendQueueStore.getState().update((queue) => enqueueTurn(queue, 's1', turn('q2')))).not.toThrow();
    expect(useSendQueueStore.getState().queue.s1).toHaveLength(2);
    expect(await useSendQueueStore.getState().claim('s1', 'q1')).toBe(true);
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
