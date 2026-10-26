import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { drawInpaintLayer, InpaintSizeMismatchError, readInpaintJob, readInpaintJobs, removeInpaintJob, saveInpaintJob, type InpaintJob } from './inpaint-layer';

const job = (taskId: string, extra: Partial<InpaintJob> = {}): InpaintJob => ({
  taskId, sessionId: 's1', maskUrl: '/api/v1/assets/mask', frame: { x: 10, y: 20, width: 300, height: 200 }, size: { width: 900, height: 600 }, ...extra,
});

function picture(tag: string, width: number, height: number): CanvasImageSource {
  return { tag, width, height } as unknown as CanvasImageSource;
}

function contextRecorder() {
  const calls: string[] = [];
  const context = {
    globalCompositeOperation: 'source-over',
    clearRect: (...args: number[]) => calls.push(`clear:${args.join(',')}`),
    drawImage: (image: { tag: string }, ...args: number[]) => calls.push(`draw:${image.tag}:${context.globalCompositeOperation}:${args.join(',')}`),
  };
  return { calls, context };
}

describe('inpaint layer drawing', () => {
  it('draws the result at the mask size, then knocks the mask out of it', () => {
    const { calls, context } = contextRecorder();
    drawInpaintLayer(context as never, picture('result', 900, 600), picture('mask', 900, 600), { width: 900, height: 600 });
    expect(calls).toEqual(['clear:0,0,900,600', 'draw:result:source-over:0,0,900,600', 'draw:mask:destination-out:0,0,900,600']);
    expect(context.globalCompositeOperation).toBe('source-over');
  });

  it('refuses a result whose pixels do not match the mask size instead of stretching it', () => {
    const { calls, context } = contextRecorder();
    expect(() => drawInpaintLayer(context as never, picture('result', 2048, 2048), picture('mask', 3840, 2160), { width: 3840, height: 2160 }))
      .toThrow(InpaintSizeMismatchError);
    expect(calls).toEqual([]);
  });

  it('refuses a mask whose pixels do not match the target size', () => {
    const { calls, context } = contextRecorder();
    expect(() => drawInpaintLayer(context as never, picture('result', 3840, 2160), picture('mask', 2048, 2048), { width: 3840, height: 2160 }))
      .toThrow(InpaintSizeMismatchError);
    expect(calls).toEqual([]);
  });

});

describe('inpaint jobs in the browser session', () => {
  beforeEach(() => {
    const data = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      get length() { return data.size; },
      key: (index: number) => [...data.keys()][index] ?? null,
      getItem: (name: string) => data.get(name) ?? null,
      setItem: (name: string, value: string) => { data.set(name, value); },
      removeItem: (name: string) => { data.delete(name); },
      clear: () => data.clear(),
    });
  });
  afterEach(() => vi.unstubAllGlobals());

  it('remembers a job across a reload and forgets it when done', () => {
    saveInpaintJob(job('t1'));
    expect(readInpaintJob('t1')).toEqual(job('t1'));
    removeInpaintJob('t1');
    expect(readInpaintJob('t1')).toBeNull();
  });

  it('lists only the jobs of the session in view', () => {
    saveInpaintJob(job('t1'));
    saveInpaintJob(job('t2', { sessionId: 's2' }));
    expect(readInpaintJobs('s1').map((item) => item.taskId)).toEqual(['t1']);
  });

  it('ignores corrupt or mismatched records', () => {
    sessionStorage.setItem('art-ai:inpaint-job:t3', '{not json');
    sessionStorage.setItem('art-ai:inpaint-job:t4', JSON.stringify({ ...job('t4'), size: { width: 0, height: 1 } }));
    sessionStorage.setItem('art-ai:inpaint-job:t5', JSON.stringify(job('other')));
    sessionStorage.setItem('art-ai:inpaint-job:t7', JSON.stringify(job('t7', { maskAssetId: 'not-an-asset' })));
    expect(readInpaintJob('t3')).toBeNull();
    expect(readInpaintJob('t4')).toBeNull();
    expect(readInpaintJob('t5')).toBeNull();
    expect(readInpaintJob('t7')).toBeNull();
  });

  it('keeps the platform mask asset of a finished patch', () => {
    const maskAssetId = 'asset_0123456789abcdef0123456789abcdef';
    saveInpaintJob(job('t6', { maskAssetId }));
    expect(readInpaintJob('t6')?.maskAssetId).toBe(maskAssetId);
  });
});
