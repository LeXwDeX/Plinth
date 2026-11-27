import { describe, expect, it, vi } from 'vitest';

import { chatErrorToastId, requestTurnStop, sendStageRequest, stageRequestStep } from './turn-signals';

describe('turn signals (#665)', () => {
  it('a refused stop is said and the turn is still followed; a taken stop ends the stream', async () => {
    const handlers = { onStopped: vi.fn(), onRefused: vi.fn() };
    await requestTurnStop({ cancelTurn: vi.fn(async () => { throw new Error('409'); }) }, 's1', handlers);
    expect(handlers.onRefused).toHaveBeenCalledTimes(1);
    expect(handlers.onStopped).not.toHaveBeenCalled();
    const cancelTurn = vi.fn(async () => true);
    await requestTurnStop({ cancelTurn }, 's1', handlers);
    expect(cancelTurn).toHaveBeenCalledWith('s1');
    expect(handlers.onStopped).toHaveBeenCalledTimes(1);
    expect(handlers.onRefused).toHaveBeenCalledTimes(1);
  });

  it('a stage action waits for uploads instead of being dropped, and is dropped only without a session', () => {
    expect(stageRequestStep({ hasSession: true, uploadsPending: true })).toBe('wait');
    expect(stageRequestStep({ hasSession: true, uploadsPending: false })).toBe('send');
    expect(stageRequestStep({ hasSession: false, uploadsPending: true })).toBe('drop');
  });

  it('a replayed error updates its toast, while the same words in another session or turn are their own toast', () => {
    const shown = new Map<string, string>();
    const show = (sessionId: string, replyId: string, text: string) => shown.set(chatErrorToastId(sessionId, replyId, text), text);
    show('s1', 'm1', 'upstream failed');
    // The stream joined again replays the same reply's error: one toast.
    show('s1', 'm1', 'upstream failed');
    expect(shown.size).toBe(1);
    // The same words for another session, or for a later turn's reply, while the first is still shown: not merged.
    show('s2', 'm1', 'upstream failed');
    show('s1', 'm2', 'upstream failed');
    expect(shown.size).toBe(3);
    // Different words of one reply are different toasts.
    show('s1', 'm1', 'quota exceeded');
    expect(shown.size).toBe(4);
  });
});

describe('#691: a stage request is pending until its send is over', () => {
  const store = () => {
    const state = { requested: true, sending: 0 };
    return {
      state,
      pending: () => state.requested || state.sending > 0,
      handlers: (send: () => Promise<void>) => ({
        clear: () => { state.requested = false; },
        begin: () => { state.sending += 1; },
        end: () => { state.sending -= 1; },
        send,
      }),
    };
  };

  it('a dropped request (no session) is no longer pending and nothing is sent', () => {
    const { pending, handlers } = store();
    const send = vi.fn(async () => {});
    expect(sendStageRequest('drop', handlers(send))).toBeNull();
    expect(send).not.toHaveBeenCalled();
    expect(pending()).toBe(false);
  });

  it('a send that never starts a turn (no Agent, a model no longer offered) ends the pending state', async () => {
    const { pending, handlers } = store();
    // The send returns early: it neither queues nor starts a turn.
    const sent = sendStageRequest('send', handlers(async () => {}));
    expect(pending()).toBe(true);
    await sent;
    expect(pending()).toBe(false);
  });

  it('stays pending, without a gap, from the request until the send is over', async () => {
    const { pending, state, handlers } = store();
    let finish: () => void = () => {};
    const seen: boolean[] = [];
    const sent = sendStageRequest('send', {
      ...handlers(() => new Promise<void>((resolve) => { finish = resolve; })),
      clear: () => { seen.push(pending()); state.requested = false; seen.push(pending()); },
    });
    expect(seen).toEqual([true, true]);
    expect(pending()).toBe(true);
    finish();
    await sent;
    expect(pending()).toBe(false);
  });
});
