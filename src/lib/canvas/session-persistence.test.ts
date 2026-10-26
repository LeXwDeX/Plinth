import { describe, expect, it, vi } from 'vitest';
import { beginSessionCanvasCreate, enqueueSessionCanvasSave, finishSessionCanvasCreate, waitForSessionCanvasSaves } from './session-persistence';
import type { CanvasProject } from './browser-client';

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe('session canvas save queue', () => {
  it('serializes a remounted board and capture while other sessions stay independent', async () => {
    const gate = deferred();
    const calls: string[] = [];
    const first = enqueueSessionCanvasSave('queue-A', async () => { calls.push('board'); await gate.promise; });
    const second = enqueueSessionCanvasSave('queue-A', async () => { calls.push('capture'); return 42; });
    await enqueueSessionCanvasSave('queue-B', async () => { calls.push('other'); });
    expect(calls).toEqual(['board', 'other']);
    gate.resolve();
    await first;
    expect(await second).toBe(42);
    expect(calls).toEqual(['board', 'other', 'capture']);
  });

  it('a rejected write does not poison later saves or create an unhandled cleanup rejection', async () => {
    const error = new Error('offline');
    const failed = enqueueSessionCanvasSave('queue-rejected', async () => { throw error; });
    const recovered = enqueueSessionCanvasSave('queue-rejected', async () => 'saved');
    await expect(failed).rejects.toBe(error);
    expect(await recovered).toBe('saved');
    await waitForSessionCanvasSaves('queue-rejected');
    // Vitest also fails this test on unhandled promise rejections.
  });

  it('wait includes writes added during its wait and leaves drained sessions reusable', async () => {
    const firstGate = deferred();
    const secondGate = deferred();
    void enqueueSessionCanvasSave('queue-wait', async () => firstGate.promise);
    let idle = false;
    const wait = waitForSessionCanvasSaves('queue-wait').then(() => { idle = true; });
    const second = enqueueSessionCanvasSave('queue-wait', async () => secondGate.promise);
    firstGate.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(idle).toBe(false);
    secondGate.resolve();
    await second;
    await wait;
    expect(idle).toBe(true);
    await waitForSessionCanvasSaves('queue-wait');
    expect(await enqueueSessionCanvasSave('queue-wait', async () => 7)).toBe(7);
    await waitForSessionCanvasSaves('queue-unused');
  });
});

describe('session canvas creation receipt', () => {
  const project: CanvasProject = {
    id: 'created-board', userId: 'owner', sessionId: 'create-A', name: '', viewport: '{}', bgStyle: 'dots',
    revision: 1, created: true, createdAt: '', updatedAt: '',
  };

  it('shares the actual creation across cancelled effects/remounts and consumes it once', async () => {
    const gate = deferred<CanvasProject>();
    const create = vi.fn(() => gate.promise);
    const first = beginSessionCanvasCreate('create-A', create);
    // StrictMode effect cleanup or switching away does not consume a request that is still loading.
    const remount = beginSessionCanvasCreate('create-A', create);
    expect(remount).toBe(first);
    gate.resolve(project);
    expect(await remount).toEqual(project);
    expect(beginSessionCanvasCreate('create-A', create)).toBe(first);
    expect(create).toHaveBeenCalledTimes(1);
    finishSessionCanvasCreate('create-A', remount);
    const existing = beginSessionCanvasCreate('create-A', async () => ({ ...project, created: false }));
    expect((await existing).created).toBe(false);
  });

  it('keeps sessions independent, retries failed creates and ignores stale cleanup', async () => {
    const first = beginSessionCanvasCreate('create-failed', async () => { throw new Error('offline'); });
    await expect(first).rejects.toThrow('offline');
    const retry = beginSessionCanvasCreate('create-failed', async () => project);
    await retry;
    finishSessionCanvasCreate('create-failed', first);
    expect(beginSessionCanvasCreate('create-failed', async () => { throw new Error('unexpected repeat'); })).toBe(retry);
    const other = beginSessionCanvasCreate('create-other', async () => ({ ...project, id: 'other' }));
    expect((await other).id).toBe('other');
    finishSessionCanvasCreate('create-failed', retry);
    finishSessionCanvasCreate('create-other', other);
  });
});
