import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createBrushCommitter, paintBrushStroke, type BrushContext } from './brush-layer';

function recordingContext() {
  const calls: string[] = [];
  const context: BrushContext = {
    save: () => calls.push('save'), restore: () => calls.push('restore'),
    scale: (x, y) => calls.push(`scale:${x},${y}`),
    beginPath: () => calls.push('begin'),
    moveTo: (x, y) => calls.push(`move:${x},${y}`),
    lineTo: (x, y) => calls.push(`line:${x},${y}`),
    stroke: () => calls.push(`stroke:${context.globalCompositeOperation}:${String(context.strokeStyle)}:${context.lineWidth}`),
    arc: (x, y, radius) => calls.push(`arc:${x},${y},${radius}`),
    fill: () => calls.push(`fill:${context.globalCompositeOperation}:${String(context.fillStyle)}`),
    lineCap: '', lineJoin: '', lineWidth: 0, strokeStyle: '', fillStyle: '', globalCompositeOperation: '',
  };
  return { context, calls };
}

describe('paintBrushStroke', () => {
  it('draws in node units scaled to bitmap pixels with the stroke own colour and size', () => {
    const { context, calls } = recordingContext();
    paintBrushStroke(context, { points: [1, 2, 3, 4], erase: false, size: 12, color: '#123456' }, { x: 2, y: 3 }, '#000');
    expect(calls).toEqual(['save', 'scale:2,3', 'begin', 'move:1,2', 'line:3,4', 'stroke:source-over:#123456:12', 'restore']);
  });

  it('erases with destination-out and draws a tap as a dot', () => {
    const { context, calls } = recordingContext();
    paintBrushStroke(context, { points: [5, 6], erase: true, size: 8, color: '#fff' }, { x: 1, y: 1 }, '#abcdef');
    // #245: a filled circle of the brush size, not a zero-length line some engines skip.
    expect(calls).toEqual(['save', 'scale:1,1', 'begin', 'arc:5,6,4', 'fill:destination-out:#abcdef', 'restore']);
  });
});

describe('brush committer', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const make = (commit: () => Promise<void>) => {
    const onFailure = vi.fn();
    return { onFailure, committer: createBrushCommitter({ idleMilliseconds: 1500, commit, onFailure }) };
  };

  it('uploads once after the pen has been still, however many strokes came before', async () => {
    const commit = vi.fn(async () => undefined);
    const { committer } = make(commit);
    for (let stroke = 0; stroke < 5; stroke += 1) {
      committer.touch();
      await vi.advanceTimersByTimeAsync(500);
    }
    expect(commit).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1500);
    expect(commit).toHaveBeenCalledTimes(1);
    expect(committer.pending()).toBe(false);
  });

  it('flushes immediately when the tool is left, and not twice', async () => {
    const commit = vi.fn(async () => undefined);
    const { committer } = make(commit);
    committer.touch();
    await expect(committer.flush()).resolves.toBe(true);
    await vi.advanceTimersByTimeAsync(5000);
    expect(commit).toHaveBeenCalledTimes(1);
    await expect(committer.flush()).resolves.toBe(true);
    expect(commit).toHaveBeenCalledTimes(1);
  });

  it('a stroke drawn while the upload is in flight is uploaded next, never lost', async () => {
    let release: () => void = () => undefined;
    const order: string[] = [];
    const commit = vi.fn(() => new Promise<void>((resolve) => {
      order.push(`start${commit.mock.calls.length}`);
      release = () => { order.push(`end${commit.mock.calls.length}`); resolve(); };
    }));
    const { committer } = make(commit);
    committer.touch();
    const first = committer.flush();
    expect(commit).toHaveBeenCalledTimes(1);
    committer.touch(); // the next stroke begins the instant the upload is running
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(commit).toHaveBeenCalledTimes(2);
    release();
    await expect(first).resolves.toBe(true);
    expect(order).toEqual(['start1', 'end1', 'start2', 'end2']);
    expect(committer.pending()).toBe(false);
  });

  it('keeps the strokes when the upload fails, reports it once, and retries after the idle time', async () => {
    const commit = vi.fn<() => Promise<void>>()
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValue(undefined);
    const { committer, onFailure } = make(commit);
    committer.touch();
    await expect(committer.flush()).resolves.toBe(false);
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(committer.pending()).toBe(true);
    await vi.advanceTimersByTimeAsync(1500);
    expect(commit).toHaveBeenCalledTimes(2);
    expect(committer.pending()).toBe(false);
  });

  it('cancel forgets pending work', async () => {
    const commit = vi.fn(async () => undefined);
    const { committer } = make(commit);
    committer.touch();
    committer.cancel();
    await vi.advanceTimersByTimeAsync(5000);
    expect(commit).not.toHaveBeenCalled();
  });
});
