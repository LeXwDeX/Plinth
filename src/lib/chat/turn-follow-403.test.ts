import { describe, expect, it, vi } from 'vitest';
import { followTurnStream } from '@/lib/chat/turn-follow';

// #403: a stream that breaks is not the end of a turn. The server is asked whether the turn still runs; if it does,
// the stream is attached again from the turn's first event, a bounded number of times. Past the bound, or when the
// turn is over, the follow ends as interrupted and the caller reads what is stored.

interface Callbacks { onDone(): void; onError(): void }

function fixture(options: { attempts?: number; running?: Array<boolean | 'fail'>; signal?: AbortSignal } = {}) {
  const attachments: Array<{ callbacks: Callbacks; closed: ReturnType<typeof vi.fn> }> = [];
  const order: string[] = [];
  const answers = [...(options.running ?? [])];
  const controller = new AbortController();
  const signal = options.signal ?? controller.signal;
  const stillRunning = vi.fn(async (_signal: AbortSignal) => {
    order.push('read');
    const answer = answers.shift();
    if (answer === 'fail') throw new Error('network down');
    return answer ?? false;
  });
  const rewind = vi.fn(() => { order.push('rewind'); });
  const connect = vi.fn((callbacks: Callbacks) => {
    order.push('connect');
    const closed = vi.fn();
    attachments.push({ callbacks, closed });
    return closed;
  });
  const follow = followTurnStream({ connect, stillRunning, rewind, attempts: options.attempts ?? 3, signal });
  return { follow, attachments, order, connect, stillRunning, rewind, controller };
}

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

describe('following the stream of a turn', () => {
  it('ends as done when the stream reports the end of the turn, without asking the server anything', async () => {
    const f = fixture();
    f.attachments[0].callbacks.onDone();
    await expect(f.follow).resolves.toBe('done');
    expect(f.stillRunning).not.toHaveBeenCalled();
    expect(f.attachments[0].closed).toHaveBeenCalled();
  });

  it('attaches again from the start of the turn when the stream breaks while the server still runs the turn', async () => {
    const f = fixture({ running: [true] });
    f.attachments[0].callbacks.onError();
    await flush();
    // The buffer is rewound before the replay starts, never after.
    expect(f.order).toEqual(['connect', 'read', 'rewind', 'connect']);
    expect(f.stillRunning).toHaveBeenCalledWith(expect.any(AbortSignal));
    f.attachments[1].callbacks.onDone();
    await expect(f.follow).resolves.toBe('done');
  });

  it('ends as interrupted, attaching nothing, when the turn is no longer running', async () => {
    const f = fixture({ running: [false] });
    f.attachments[0].callbacks.onError();
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.connect).toHaveBeenCalledTimes(1);
    expect(f.rewind).not.toHaveBeenCalled();
  });

  it('ends as interrupted when the server cannot be asked', async () => {
    const f = fixture({ running: ['fail'] });
    f.attachments[0].callbacks.onError();
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.connect).toHaveBeenCalledTimes(1);
  });

  it('attaches again only as often as it is allowed, then gives the turn up to the caller', async () => {
    const f = fixture({ attempts: 2, running: [true, true, true, true] });
    f.attachments[0].callbacks.onError();
    await flush();
    f.attachments[1].callbacks.onError();
    await flush();
    expect(f.connect).toHaveBeenCalledTimes(3);
    f.attachments[2].callbacks.onError();
    await expect(f.follow).resolves.toBe('interrupted');
    // The third break is not even asked about: the bound is spent.
    expect(f.stillRunning).toHaveBeenCalledTimes(2);
    expect(f.connect).toHaveBeenCalledTimes(3);
  });

  it('never attaches again when it is allowed no attempt', async () => {
    const f = fixture({ attempts: 0, running: [true] });
    f.attachments[0].callbacks.onError();
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.stillRunning).not.toHaveBeenCalled();
    expect(f.connect).toHaveBeenCalledTimes(1);
  });

  it('ignores what a replaced stream still says', async () => {
    const f = fixture({ running: [true] });
    f.attachments[0].callbacks.onError();
    await flush();
    // The first subscription is gone; its late callbacks must not end or restart the follow.
    f.attachments[0].callbacks.onDone();
    f.attachments[0].callbacks.onError();
    await flush();
    expect(f.connect).toHaveBeenCalledTimes(2);
    expect(f.stillRunning).toHaveBeenCalledTimes(1);
    f.attachments[1].callbacks.onDone();
    await expect(f.follow).resolves.toBe('done');
  });

  it('ends as interrupted when the stream cannot be attached again', async () => {
    const f = fixture({ running: [true] });
    // The first attach already happened; the next one, after the break, throws.
    f.connect.mockImplementationOnce(() => { throw new Error('EventSource failed'); });
    f.attachments[0].callbacks.onError();
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.connect).toHaveBeenCalledTimes(2);
  });
});

describe('stopping the follow', () => {
  it('ends as aborted, attaching nothing, when it is already aborted', async () => {
    const controller = new AbortController();
    controller.abort();
    const f = fixture({ signal: controller.signal });
    await expect(f.follow).resolves.toBe('aborted');
    expect(f.connect).not.toHaveBeenCalled();
  });

  it('closes the stream and ends as aborted when the person stops the turn', async () => {
    const f = fixture();
    f.controller.abort();
    await expect(f.follow).resolves.toBe('aborted');
    expect(f.attachments[0].closed).toHaveBeenCalled();
  });

  it('does not attach again when the stop arrives while the server is being asked', async () => {
    let answer!: (running: boolean) => void;
    const attachments: Callbacks[] = [];
    const controller = new AbortController();
    const connect = vi.fn((callbacks: Callbacks) => { attachments.push(callbacks); return vi.fn(); });
    const follow = followTurnStream({
      connect,
      stillRunning: () => new Promise<boolean>((resolve) => { answer = resolve; }),
      rewind: vi.fn(),
      attempts: 3,
      signal: controller.signal,
    });
    attachments[0].onError();
    await flush();
    controller.abort();
    answer(true);
    await expect(follow).resolves.toBe('aborted');
    await flush();
    expect(connect).toHaveBeenCalledTimes(1);
  });

  it('lets a failure to open the first stream through, as it always did', async () => {
    const controller = new AbortController();
    const failure = new Error('EventSource failed');
    await expect(followTurnStream({
      connect: () => { throw failure; },
      stillRunning: async () => true,
      rewind: vi.fn(),
      attempts: 3,
      signal: controller.signal,
    })).rejects.toBe(failure);
  });
});
