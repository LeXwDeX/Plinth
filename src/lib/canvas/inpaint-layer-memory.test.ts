import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { InpaintJob } from './inpaint-layer';

/**
 * A job must survive for this page even when session storage refuses writes, scans or removals.
 * The module keeps its own copy; storage only carries it across a reload.
 */

const job = (taskId: string, extra: Partial<InpaintJob> = {}): InpaintJob => ({
  taskId, sessionId: 's1', maskUrl: '/api/v1/assets/mask', frame: { x: 10, y: 20, width: 300, height: 200 },
  size: { width: 900, height: 600 }, ...extra,
});

type Failure = { set?: boolean; get?: boolean; key?: boolean; remove?: boolean; length?: boolean };

function storageWith(fail: Failure) {
  const data = new Map<string, string>();
  const storage = {
    get length() {
      if (fail.length) throw new Error('denied');
      return data.size;
    },
    key: (index: number) => {
      if (fail.key) throw new Error('denied');
      return [...data.keys()][index] ?? null;
    },
    getItem: (name: string) => {
      if (fail.get) throw new Error('denied');
      return data.get(name) ?? null;
    },
    setItem: (name: string, value: string) => {
      if (fail.set) throw new Error('denied');
      data.set(name, value);
    },
    removeItem: (name: string) => {
      if (fail.remove) throw new Error('denied');
      data.delete(name);
    },
  };
  return { data, storage };
}

async function freshLayer() {
  vi.resetModules();
  return import('./inpaint-layer');
}

beforeEach(() => {
  vi.stubGlobal('sessionStorage', storageWith({}).storage);
});

afterEach(() => vi.unstubAllGlobals());

describe('inpaint jobs without usable session storage', () => {
  it('keeps a job in this page when setItem refuses and reports the storage failure', async () => {
    const { storage } = storageWith({ set: true });
    vi.stubGlobal('sessionStorage', storage);
    const layer = await freshLayer();
    expect(layer.saveInpaintJob(job('t1'))).toBe(false);
    expect(layer.readInpaintJob('t1')).toEqual(job('t1'));
    expect(layer.readInpaintJobs('s1').map((item) => item.taskId)).toEqual(['t1']);
  });

  it('reads the in-memory copy when getItem or the storage scan throws', async () => {
    const { storage } = storageWith({});
    vi.stubGlobal('sessionStorage', storage);
    const layer = await freshLayer();
    expect(layer.saveInpaintJob(job('t2'))).toBe(true);
    vi.stubGlobal('sessionStorage', storageWith({ get: true }).storage);
    expect(layer.readInpaintJob('t2')?.taskId).toBe('t2');
    vi.stubGlobal('sessionStorage', storageWith({ key: true }).storage);
    expect(layer.readInpaintJobs('s1').map((item) => item.taskId)).toEqual(['t2']);
  });

  it('does not resurrect a removed job when removeItem refuses', async () => {
    const { data, storage } = storageWith({});
    vi.stubGlobal('sessionStorage', storage);
    const layer = await freshLayer();
    layer.saveInpaintJob(job('t3'));
    expect(data.size).toBe(1);
    const failing = storageWith({ remove: true });
    failing.data.set('art-ai:inpaint-job:t3', JSON.stringify(job('t3')));
    vi.stubGlobal('sessionStorage', failing.storage);
    layer.removeInpaintJob('t3');
    expect(layer.readInpaintJob('t3')).toBeNull();
    expect(layer.readInpaintJobs('s1')).toEqual([]);
    // An explicit re-save is a new decision and clears the tombstone.
    expect(layer.saveInpaintJob(job('t3'))).toBe(true);
    expect(layer.readInpaintJob('t3')?.taskId).toBe('t3');
  });

  it('merges memory and storage once per task and keeps sessions apart', async () => {
    const { storage } = storageWith({});
    vi.stubGlobal('sessionStorage', storage);
    const layer = await freshLayer();
    layer.saveInpaintJob(job('same'));
    storage.setItem('art-ai:inpaint-job:stored', JSON.stringify(job('stored', { sessionId: 's2' })));
    layer.saveInpaintJob(job('other', { sessionId: 's2' }));
    expect(layer.readInpaintJobs('s1').map((item) => item.taskId)).toEqual(['same']);
    expect(layer.readInpaintJob('same')?.sessionId).toBe('s1');
    expect(layer.readInpaintJobs('s2').map((item) => item.taskId).sort()).toEqual(['other', 'stored']);
  });

  it('does not store an invalid job in memory or accept it back', async () => {
    const layer = await freshLayer();
    expect(layer.saveInpaintJob(job('bad', { frame: { x: 0, y: 0, width: 0, height: 1 } }))).toBe(false);
    expect(layer.readInpaintJob('bad')).toBeNull();
    expect(layer.readInpaintJobs('s1')).toEqual([]);
  });
});
