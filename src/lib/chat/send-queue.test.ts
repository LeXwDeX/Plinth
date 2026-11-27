import { describe, expect, it } from 'vitest';

import {
  cancelQueuedTurn, claimStoredTurn, drainAwaitsRead, drainWait, enqueueTurn, foreignHold, holdDrainForRead, holdsToRelease,
  MAX_QUEUE_PROBE_TOTAL, MAX_QUEUE_PROBES, MAX_TURN_LOCK_REFUSALS, MAX_TURN_LOCK_RETRY_MS, MIN_DRAIN_WAIT_MS, turnLockRetryDelay, nextDrainStep, parseQueue, parseSendHold, pruneQueuedSessions,
  queueMayDrain, queuedFor, readCompleted, SEND_HOLD_MS, SEND_QUEUE_KEY, sendHoldKey, sessionGone, takeQueuedTurn, writeQueue,
  type DrainGate, type QueuedTurn,
} from '@/lib/chat/send-queue';
import type { ChatMessage } from '@/store/chat-store';

const turn = (id: string, text = id): QueuedTurn => ({ id, text, images: [], claims: [], selection: [] });

describe('send queue', () => {
  it('keeps each session in order and leaves the others alone', () => {
    let queue = enqueueTurn({}, 'a', turn('1'));
    queue = enqueueTurn(queue, 'a', turn('2'));
    queue = enqueueTurn(queue, 'b', turn('3'));
    expect(queuedFor(queue, 'a').map((item) => item.id)).toEqual(['1', '2']);
    expect(queuedFor(queue, 'b').map((item) => item.id)).toEqual(['3']);
    expect(queuedFor(queue, null)).toEqual([]);
    expect(queuedFor(queue, 'c')).toEqual([]);
  });

  it('sends the oldest first and forgets an empty session', () => {
    const queue = enqueueTurn(enqueueTurn({}, 'a', turn('1')), 'a', turn('2'));
    const first = takeQueuedTurn(queue, 'a');
    expect(first.turn?.id).toBe('1');
    const second = takeQueuedTurn(first.queue, 'a');
    expect(second.turn?.id).toBe('2');
    expect(second.queue).toEqual({});
    expect(takeQueuedTurn(second.queue, 'a').turn).toBeNull();
  });

  it('cancels one waiting message without touching the rest', () => {
    const queue = enqueueTurn(enqueueTurn({}, 'a', turn('1')), 'a', turn('2'));
    expect(queuedFor(cancelQueuedTurn(queue, 'a', '1'), 'a').map((item) => item.id)).toEqual(['2']);
    expect(cancelQueuedTurn(cancelQueuedTurn(queue, 'a', '1'), 'a', '2')).toEqual({});
    expect(cancelQueuedTurn(queue, 'a', 'missing')).toEqual(queue);
  });

  it('keeps the one-turn style-lock exemption the message was queued with (#154)', () => {
    const queue = enqueueTurn({}, 'a', { ...turn('1'), styleLockSkipped: true });
    expect(takeQueuedTurn(queue, 'a').turn?.styleLockSkipped).toBe(true);
  });

  it('keeps the frozen selection mask of a queued canvas turn (#244)', () => {
    const queue = enqueueTurn({}, 'a', {
      ...turn('1'), inputSource: 'canvas',
      selectionMask: { maskAssetId: 'mask-asset' },
    });
    const taken = takeQueuedTurn(queue, 'a').turn;
    expect(taken?.inputSource).toBe('canvas');
    expect(taken?.selectionMask).toEqual({ maskAssetId: 'mask-asset' });
  });

  // #697: a turn queued before the purpose control was removed still carries `attachmentPurposes`; it reads as a turn.
  it('reads a turn stored with attachment purposes before #697 without error', () => {
    const stored = JSON.stringify({ a: [{ ...turn('old'), images: ['/a', '/b'], attachmentPurposes: [{ imageSlot: 1, purpose: 'input' }, { imageSlot: 2, purpose: 'input' }] }] });
    const taken = takeQueuedTurn(parseQueue(stored), 'a').turn;
    expect(taken?.id).toBe('old');
    expect(taken?.images).toEqual(['/a', '/b']);
  });
});

describe('send queue across tabs (#665)', () => {
  const done: ChatMessage = { id: 'm1', role: 'assistant', content: 'ok', isPending: false, createdAt: 1 };
  const pending: ChatMessage = { id: 'm2', role: 'assistant', content: '', isPending: true, createdAt: 2 };
  const sharedStorage = () => {
    const memory = new Map<string, string>();
    return {
      memory,
      getItem: (key: string) => memory.get(key) ?? null,
      setItem: (key: string, value: string) => { memory.set(key, value); },
      removeItem: (key: string) => { memory.delete(key); },
    };
  };
  const idle = { busy: false, historyLoading: false, streaming: false };

  it('lets one tab send while the other waits for its turn to end and for a fresh read', () => {
    const storage = sharedStorage();
    const now = 1_000_000;
    writeQueue(storage, enqueueTurn(enqueueTurn({}, 's1', turn('q1')), 's1', turn('q2')));
    // Tab A claims q1 and sends it; tab B, whose messages do not show A's turn, tries the same oldest turn.
    expect(claimStoredTurn(storage, 's1', 'q1', 'A', now)).toBe('claimed');
    expect(claimStoredTurn(storage, 's1', 'q1', 'B', now)).toBe('held');
    let gate: DrainGate = holdDrainForRead({}, 's1', 1);
    // B's storage event shows q2 next and B's messages have no pending reply: the hold still keeps it from draining.
    expect(queuedFor(parseQueue(storage.getItem(SEND_QUEUE_KEY)), 's1').map((item) => item.id)).toEqual(['q2']);
    const holdB = () => foreignHold(parseSendHold(storage.getItem(sendHoldKey('s1'))), 'B', now) !== null;
    expect(holdB()).toBe(true);
    expect(queueMayDrain({ ...idle, messages: [done], foreignHold: holdB(), awaitingRead: drainAwaitsRead(gate, 's1') })).toBe(false);
    expect(claimStoredTurn(storage, 's1', 'q2', 'B', now)).toBe('held');
    // A's turn ends and it releases the hold: B still waits for a read made after that.
    storage.removeItem(sendHoldKey('s1'));
    expect(holdB()).toBe(false);
    expect(queueMayDrain({ ...idle, messages: [done], awaitingRead: drainAwaitsRead(gate, 's1') })).toBe(false);
    // A read that started earlier (read 0) does not count; read 1 does, and it may still show a pending reply.
    gate = readCompleted(gate, 's1', 0);
    expect(drainAwaitsRead(gate, 's1')).toBe(true);
    gate = readCompleted(gate, 's1', 1);
    expect(queueMayDrain({ ...idle, messages: [done, pending], awaitingRead: drainAwaitsRead(gate, 's1') })).toBe(false);
    expect(queueMayDrain({ ...idle, messages: [done], awaitingRead: drainAwaitsRead(gate, 's1') })).toBe(true);
    // B claims q2 now, and A, racing for it, is refused.
    expect(claimStoredTurn(storage, 's1', 'q2', 'B', now)).toBe('claimed');
    expect(claimStoredTurn(storage, 's1', 'q2', 'A', now)).toBe('held');
    expect(storage.memory.has(SEND_QUEUE_KEY)).toBe(false);
  });

  it('lets a hold lapse when its tab stops refreshing it, and refuses a turn already gone', () => {
    const storage = sharedStorage();
    const now = 5_000;
    writeQueue(storage, enqueueTurn(enqueueTurn({}, 's1', turn('q1')), 's1', turn('q2')));
    expect(claimStoredTurn(storage, 's1', 'q1', 'A', now)).toBe('claimed');
    expect(claimStoredTurn(storage, 's1', 'q2', 'B', now + SEND_HOLD_MS - 1)).toBe('held');
    expect(claimStoredTurn(storage, 's1', 'q2', 'B', now + SEND_HOLD_MS + 1)).toBe('claimed');
    expect(claimStoredTurn(storage, 's1', 'q2', 'A', now + 2 * SEND_HOLD_MS + 2)).toBe('gone');
    // A tab's own hold never stops it.
    writeQueue(storage, enqueueTurn({}, 's1', turn('q3')));
    expect(claimStoredTurn(storage, 's1', 'q3', 'B', now + SEND_HOLD_MS + 2)).toBe('claimed');
  });

  it('makes only the last writer send when two tabs without a lock claim at once', () => {
    const storage = sharedStorage();
    writeQueue(storage, enqueueTurn({}, 's1', turn('q1')));
    // Tab B's hold lands between tab A's write of its hold and its read back.
    const racing = { ...storage, setItem: (key: string, value: string) => {
      storage.setItem(key, value);
      if (key === sendHoldKey('s1')) storage.setItem(key, JSON.stringify({ owner: 'B', expiresAt: 10 + SEND_HOLD_MS }));
    } };
    expect(claimStoredTurn(racing, 's1', 'q1', 'A', 10)).toBe('held');
  });

  it('ignores a hold it cannot read', () => {
    expect(parseSendHold('not json')).toBeNull();
    expect(parseSendHold('{"owner":1,"expiresAt":2}')).toBeNull();
    expect(foreignHold(null, 'A', 0)).toBeNull();
  });

  it('ignores a hold that ends later than any tab writes one, so it cannot hold the send for ever', () => {
    const now = 1_000;
    expect(parseSendHold(JSON.stringify({ owner: 'B', expiresAt: now + SEND_HOLD_MS }), now)?.owner).toBe('B');
    expect(parseSendHold(JSON.stringify({ owner: 'B', expiresAt: now + 10 * SEND_HOLD_MS }), now)).toBeNull();
    expect(parseSendHold(JSON.stringify({ owner: 'B', expiresAt: 1e300 }), now)).toBeNull();
    // Another tab cannot keep a corrupt hold over this tab's claim.
    const memory = new Map<string, string>([[sendHoldKey('s1'), JSON.stringify({ owner: 'B', expiresAt: 8.64e15 })]]);
    const storage = { getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => { memory.set(key, value); }, removeItem: (key: string) => { memory.delete(key); } };
    writeQueue(storage, enqueueTurn({}, 's1', turn('q1')));
    expect(claimStoredTurn(storage, 's1', 'q1', 'A', now)).toBe('claimed');
  });

  it('keeps every drain wait between a small floor and what a timer can count', () => {
    expect(drainWait(-5)).toBe(MIN_DRAIN_WAIT_MS);
    expect(drainWait(0)).toBe(MIN_DRAIN_WAIT_MS);
    expect(drainWait(1e300)).toBe(2 ** 31 - 1);
    expect(drainWait(Number.NaN)).toBe(2 ** 31 - 1);
    expect(drainWait(1_234)).toBe(1_234);
  });
});

describe('the drain\'s next step (#665)', () => {
  const base = { hold: null, now: 1_000, required: undefined, forcedRead: null, pollMilliseconds: 2_000 };

  it('waits for another tab\'s hold first, then for a fresh read, and claims only then', () => {
    expect(nextDrainStep({ ...base, hold: { owner: 'B', expiresAt: 4_000 }, required: 3 })).toEqual({ kind: 'wait', delay: 3_000 + MIN_DRAIN_WAIT_MS });
    // A hold that lapses now still waits the floor, not zero.
    expect(nextDrainStep({ ...base, hold: { owner: 'B', expiresAt: 900 } })).toEqual({ kind: 'wait', delay: MIN_DRAIN_WAIT_MS });
    expect(nextDrainStep({ ...base, required: 3 })).toEqual({ kind: 'read', required: 3 });
    // The read it asked for came back without opening the gate (failed or cut short): ask again after the poll interval.
    expect(nextDrainStep({ ...base, required: 3, forcedRead: 3 })).toEqual({ kind: 'wait', delay: 2_000 });
    expect(nextDrainStep({ ...base, required: 4, forcedRead: 3 })).toEqual({ kind: 'read', required: 4 });
    expect(nextDrainStep(base)).toEqual({ kind: 'claim' });
  });
});

describe('releasing holds per session (#665)', () => {
  it('keeps the hold of a session still being sent or whose reply runs in view, and releases every other one', () => {
    const held = ['a', 'b', 'c'];
    expect(holdsToRelease(held, { sending: 'a', activeSessionId: 'b', activeTurnRunning: true })).toEqual(['c']);
    // The person left b: its hold goes, even with a's send still running.
    expect(holdsToRelease(held, { sending: 'a', activeSessionId: 'c', activeTurnRunning: false })).toEqual(['b', 'c']);
    expect(holdsToRelease(held, { sending: null, activeSessionId: 'a', activeTurnRunning: false })).toEqual(['a', 'b', 'c']);
    expect(holdsToRelease([], { sending: null, activeSessionId: null, activeTurnRunning: false })).toEqual([]);
  });

  it('waits longer after each turn lock refusal in a row, up to a cap', () => {
    expect(turnLockRetryDelay(1)).toBe(SEND_HOLD_MS / 3);
    expect(turnLockRetryDelay(2)).toBe((SEND_HOLD_MS / 3) * 2);
    expect(turnLockRetryDelay(3)).toBe((SEND_HOLD_MS / 3) * 4);
    expect(turnLockRetryDelay(MAX_TURN_LOCK_REFUSALS)).toBe(MAX_TURN_LOCK_RETRY_MS);
    expect(turnLockRetryDelay(1_000)).toBe(MAX_TURN_LOCK_RETRY_MS);
    expect(turnLockRetryDelay(0)).toBe(SEND_HOLD_MS / 3);
  });

  it('releases the idle hold of the session in view once a later claim of it failed', () => {
    // The turn this tab sent ended (no reply runs) and its next claim failed: nothing is being sent, so the hold goes.
    expect(holdsToRelease(['a'], { sending: null, activeSessionId: 'a', activeTurnRunning: false })).toEqual(['a']);
  });
});

describe('pruning queues of sessions that are gone (#665)', () => {
  const notFound = (serverAnswered: boolean, status = 404) => Object.assign(new Error('x'), { status, serverAnswered });

  it('forgets only on the API\'s own 404 or 410, not a gateway 404, a 403 or an offline read', async () => {
    const answers: Record<string, unknown> = {
      gone: notFound(true), removed: notFound(true, 410), gateway: notFound(false), foreign: notFound(true, 403), offline: new Error('offline'),
    };
    const forgotten: string[] = [];
    await pruneQueuedSessions(Object.keys(answers), new Set(), async (id) => { throw answers[id]; }, (id) => forgotten.push(id));
    expect(forgotten.sort()).toEqual(['gone', 'removed']);
    expect(sessionGone(notFound(false))).toBe(false);
  });

  it('stops after MAX_QUEUE_PROBES unanswered asks, does not count answered ones, and stops when aborted', async () => {
    const ids = Array.from({ length: MAX_QUEUE_PROBES + 3 }, (_, index) => `s${index}`);
    const asked: string[] = [];
    await pruneQueuedSessions(ids, new Set(['s0']), async (id) => { asked.push(id); throw new Error('offline'); }, () => undefined);
    expect(asked).toHaveLength(MAX_QUEUE_PROBES);
    expect(asked).not.toContain('s0');

    // Sessions that are still there do not keep a deleted one behind them.
    const live = Array.from({ length: MAX_QUEUE_PROBES + 2 }, (_, index) => `live${index}`);
    const forgotten: string[] = [];
    await pruneQueuedSessions([...live, 'deleted'], new Set(), async (id) => {
      if (id === 'deleted') throw notFound(true);
      return {};
    }, (id) => forgotten.push(id));
    expect(forgotten).toEqual(['deleted']);
    // Still bounded per load.
    const many = Array.from({ length: MAX_QUEUE_PROBE_TOTAL + 5 }, (_, index) => `m${index}`);
    let calls = 0;
    await pruneQueuedSessions(many, new Set(), async () => { calls += 1; return {}; }, () => undefined);
    expect(calls).toBe(MAX_QUEUE_PROBE_TOTAL);

    const controller = new AbortController();
    const seen: (AbortSignal | undefined)[] = [];
    const aborted: string[] = [];
    await pruneQueuedSessions(['a', 'b', 'c'], new Set(), async (id, signal) => {
      seen.push(signal);
      if (id === 'a') controller.abort();
      throw notFound(true);
    }, (id) => aborted.push(id), controller.signal);
    expect(seen).toEqual([controller.signal]);
    expect(aborted).toEqual([]);
  });
});
