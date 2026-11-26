import { describe, expect, it, vi } from 'vitest';

import { chatErrorToastId, requestTurnStop, stageRequestStep } from './turn-signals';

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
