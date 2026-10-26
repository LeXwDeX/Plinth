import { describe, expect, it } from 'vitest';

import { cancelQueuedTurn, enqueueTurn, queuedFor, takeQueuedTurn, type QueuedTurn } from '@/lib/chat/send-queue';

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
});
