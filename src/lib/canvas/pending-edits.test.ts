import { describe, expect, it, vi } from 'vitest';

import { flushPendingEdits, registerBoardSave, registerPendingEdits, settleBoardForTurn } from './pending-edits';

describe('flushPendingEdits (#286)', () => {
  it('is saved when nothing is pending', async () => {
    await expect(flushPendingEdits('session-none')).resolves.toBe(true);
  });

  it('says whether every pending edit reached the server', async () => {
    const saved = registerPendingEdits('session-a', async () => true);
    await expect(flushPendingEdits('session-a')).resolves.toBe(true);
    // The brush's upload failed: its strokes are still only on screen.
    const failed = registerPendingEdits('session-a', async () => false);
    await expect(flushPendingEdits('session-a')).resolves.toBe(false);
    failed();
    saved();
  });

  it('treats a flusher that reports nothing as done', async () => {
    const done = registerPendingEdits('session-b', async () => undefined);
    await expect(flushPendingEdits('session-b')).resolves.toBe(true);
    done();
  });
});

describe('settleBoardForTurn (#709)', () => {
  const canvasSource = 'canvas';

  it('does not wait when the session has no board', async () => {
    vi.useFakeTimers();
    try {
      await expect(settleBoardForTurn({ sessionId: 'session-empty', inputSource: undefined, canvasInputSource: canvasSource, waitMilliseconds: 2000 }))
        .resolves.toBe('idle');
    } finally {
      vi.useRealTimers();
    }
  });

  it('flushes the brush bitmaps first, then the debounced node save, before a turn that is not a canvas turn', async () => {
    const order: string[] = [];
    const brush = registerPendingEdits('session-c', async () => { order.push('brush'); return true; });
    const save = registerBoardSave('session-c', async () => { order.push('save'); return true; });
    await expect(settleBoardForTurn({ sessionId: 'session-c', inputSource: undefined, canvasInputSource: canvasSource, waitMilliseconds: 2000 }))
      .resolves.toBe('saved');
    expect(order).toEqual(['brush', 'save']);
    brush();
    save();
  });

  it('stops waiting at the Schema bound and lets the turn go out', async () => {
    vi.useFakeTimers();
    const save = registerBoardSave('session-d', () => new Promise<boolean>(() => undefined));
    try {
      const settled = settleBoardForTurn({ sessionId: 'session-d', inputSource: undefined, canvasInputSource: canvasSource, waitMilliseconds: 2000 });
      let outcome = '';
      void settled.then((value) => { outcome = value; });
      await vi.advanceTimersByTimeAsync(1999);
      expect(outcome).toBe('');
      await vi.advanceTimersByTimeAsync(1);
      expect(outcome).toBe('timed_out');
    } finally {
      save();
      vi.useRealTimers();
    }
  });

  it('lets a failed save go out like a slow one', async () => {
    const save = registerBoardSave('session-e', async () => { throw new Error('offline'); });
    await expect(settleBoardForTurn({ sessionId: 'session-e', inputSource: undefined, canvasInputSource: canvasSource, waitMilliseconds: 2000 }))
      .resolves.toBe('failed');
    save();
  });

  it('leaves a canvas turn alone: its own snapshot path flushes the brush', async () => {
    const flush = vi.fn(async () => true);
    const brush = registerPendingEdits('session-f', flush);
    const save = registerBoardSave('session-f', flush);
    await expect(settleBoardForTurn({ sessionId: 'session-f', inputSource: canvasSource, canvasInputSource: canvasSource, waitMilliseconds: 2000 }))
      .resolves.toBe('canvas_turn');
    expect(flush).not.toHaveBeenCalled();
    brush();
    save();
  });
});
