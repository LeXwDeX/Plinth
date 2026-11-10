import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ExplicitModelLockError,
  explicitModel3DPending,
  explicitModelImagePending,
  flushExplicitModel3D,
  flushExplicitModelImage,
  pickExplicitModel3D,
  pickExplicitModelImage,
  resetExplicitModel,
} from '@/lib/chat/explicit-model';

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

async function until(check: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error('condition did not become true');
}

beforeEach(() => resetExplicitModel());

describe('the explicit 3D model lock (#103)', () => {
  it('serializes per-session writes and keeps the newest pick', async () => {
    const first = deferred();
    const second = deferred();
    const calls: string[] = [];
    const write = vi.fn((_sessionId: string, model: string) => {
      calls.push(model);
      return model === 'first' ? first.promise : second.promise;
    });
    pickExplicitModel3D({ sessionId: 's1', model: 'first', previous: 'old', write });
    pickExplicitModel3D({ sessionId: 's1', model: 'second', previous: 'first', write });
    expect(calls).toEqual(['first']);

    let flushed = false;
    const flushing = flushExplicitModel3D('s1', write).then(() => { flushed = true; });
    first.resolve();
    await until(() => calls.length === 2);
    expect(calls).toEqual(['first', 'second']);
    expect(flushed).toBe(false);

    second.resolve();
    await flushing;
    expect(flushed).toBe(true);
    expect(explicitModel3DPending('s1')).toBe(false);
  });

  it('does not let a failed older write roll back a newer pick', async () => {
    const first = deferred();
    const second = deferred();
    const onFailure = vi.fn();
    const calls: string[] = [];
    const write = vi.fn((_sessionId: string, model: string) => {
      calls.push(model);
      return model === 'first' ? first.promise : second.promise;
    });
    pickExplicitModel3D({ sessionId: 's1', model: 'first', previous: 'old', write, onFailure });
    pickExplicitModel3D({ sessionId: 's1', model: 'second', previous: 'first', write, onFailure });
    first.reject(new Error('first failed'));
    await until(() => calls.length === 2);
    second.resolve();
    await flushExplicitModel3D('s1', write);
    expect(onFailure).not.toHaveBeenCalled();
  });

  it('rolls back to the previous selection and reports a failed write', async () => {
    const onFailure = vi.fn();
    const write = vi.fn().mockRejectedValue(new Error('offline'));
    pickExplicitModel3D({ sessionId: 's1', model: 'new', previous: 'old', write, onFailure });
    await expect(flushExplicitModel3D('s1', write)).rejects.toBeInstanceOf(ExplicitModelLockError);
    expect(write).toHaveBeenCalledWith('s1', 'new');
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(onFailure).toHaveBeenCalledWith('old', 'new');
    expect(explicitModel3DPending('s1')).toBe(false);
  });

  it('reports a failed write to one send and lets the next send go through', async () => {
    const write = vi.fn().mockResolvedValue(undefined);
    pickExplicitModel3D({ sessionId: 's1', model: 'accepted', previous: null, write });
    await flushExplicitModel3D('s1', write);
    write.mockRejectedValueOnce(new Error('offline'));
    pickExplicitModel3D({ sessionId: 's1', model: 'rejected', previous: null, write, onFailure: vi.fn() });
    await expect(flushExplicitModel3D('s1', write)).rejects.toBeInstanceOf(ExplicitModelLockError);
    await expect(flushExplicitModel3D('s1', write)).resolves.toBeUndefined();
    expect(write).toHaveBeenCalledTimes(2);
    expect(explicitModel3DPending('s1')).toBe(false);
  });

  it('rolls back a later failure to the last model the server accepted', async () => {
    const onFailure = vi.fn();
    const write = vi.fn().mockResolvedValue(undefined);
    pickExplicitModel3D({ sessionId: 's1', model: 'accepted', previous: 'old', write, onFailure });
    await flushExplicitModel3D('s1', write);
    const failure = new Error('offline');
    write.mockRejectedValueOnce(failure);
    pickExplicitModel3D({ sessionId: 's1', model: 'rejected', previous: null, write, onFailure });
    await expect(flushExplicitModel3D('s1', write)).rejects.toBeInstanceOf(ExplicitModelLockError);
    expect(onFailure).toHaveBeenCalledWith('accepted', 'rejected');
  });

  it('keeps a pick made before the session exists until the first send adopts it', async () => {
    const write = vi.fn().mockResolvedValue(undefined);
    pickExplicitModel3D({ sessionId: null, model: 'pending-model', previous: 'old', write });
    expect(write).not.toHaveBeenCalled();
    expect(explicitModel3DPending()).toBe(true);
    await flushExplicitModel3D('new-session', write);
    expect(write).toHaveBeenCalledWith('new-session', 'pending-model');
    expect(explicitModel3DPending('new-session')).toBe(false);
  });

  it('makes the send wait for the adopted write and rejects when it fails', async () => {
    const write = deferred();
    const onFailure = vi.fn();
    pickExplicitModel3D({ sessionId: null, model: 'pending-model', previous: 'old', write: () => write.promise, onFailure });
    let flushed = false;
    const flushing = flushExplicitModel3D('new-session', () => write.promise).then(() => { flushed = true; });
    await Promise.resolve();
    expect(flushed).toBe(false);
    write.reject(new Error('offline'));
    await expect(flushing).rejects.toBeInstanceOf(ExplicitModelLockError);
    expect(onFailure).toHaveBeenCalledWith('old', 'pending-model');
  });

  it('writes the same model again after a new pick clears a failure', async () => {
    const write = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(undefined);
    pickExplicitModel3D({ sessionId: 's1', model: 'chosen', previous: 'old', write });
    await expect(flushExplicitModel3D('s1', write)).rejects.toBeInstanceOf(ExplicitModelLockError);
    pickExplicitModel3D({ sessionId: 's1', model: 'chosen', previous: 'old', write });
    await flushExplicitModel3D('s1', write);
    expect(write).toHaveBeenCalledTimes(2);
  });
});

describe('the explicit 2D model lock (#239)', () => {
  it('keeps the 2D and 3D locks of one session independent', async () => {
    const image = deferred();
    const model = deferred();
    const imageWrite = vi.fn(() => image.promise);
    const modelWrite = vi.fn(() => model.promise);
    pickExplicitModelImage({ sessionId: 's1', model: 'nano-banana-pro', previous: null, write: imageWrite });
    pickExplicitModel3D({ sessionId: 's1', model: 'meshy-7.1', previous: null, write: modelWrite });
    expect(explicitModelImagePending('s1')).toBe(true);
    expect(explicitModel3DPending('s1')).toBe(true);
    const flushingImage = flushExplicitModelImage('s1', imageWrite);
    image.resolve();
    await flushingImage;
    expect(explicitModelImagePending('s1')).toBe(false);
    expect(explicitModel3DPending('s1')).toBe(true);
    const flushingModel = flushExplicitModel3D('s1', modelWrite);
    model.resolve();
    await flushingModel;
    expect(explicitModel3DPending('s1')).toBe(false);
    expect(imageWrite).toHaveBeenCalledWith('s1', 'nano-banana-pro');
    expect(modelWrite).toHaveBeenCalledWith('s1', 'meshy-7.1');
  });

  it('adopts a 2D and a 3D pick made before the session exists, each on its own domain', async () => {
    const write = vi.fn(async () => undefined);
    pickExplicitModelImage({ sessionId: null, model: 'image-model', previous: null, write });
    pickExplicitModel3D({ sessionId: null, model: 'model-3d-model', previous: null, write });
    await flushExplicitModelImage('new-session', write);
    await flushExplicitModel3D('new-session', write);
    expect(write).toHaveBeenCalledWith('new-session', 'image-model');
    expect(write).toHaveBeenCalledWith('new-session', 'model-3d-model');
    expect(explicitModelImagePending('new-session')).toBe(false);
    expect(explicitModel3DPending('new-session')).toBe(false);
  });
});
