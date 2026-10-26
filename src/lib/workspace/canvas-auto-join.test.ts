import { describe, expect, it } from 'vitest';

import { SINCE_PREFIX, planAutoJoin, withPlaced } from '@/lib/workspace/canvas-auto-join';
import type { VersionTile } from '@/lib/workspace/versions';

const tile = (taskId: string, createdAt: string, extra: Partial<VersionTile> = {}): VersionTile => ({
  key: taskId, taskId, taskIds: [taskId], domain: 'image', version: 1, status: 'ready', progress: 1,
  capability: 'text-to-image', createdAt, assets: [
    { id: `${taskId}-a1`, role: 'image', variant: '', mediaType: 'image/png', ordinal: 1, url: `/${taskId}/1.png`, kind: 'image' },
  ], thumbnailUrl: '', groupLabel: '', groupComplete: false, slots: [], views: [], parents: [], failure: '',
  origin: 'task', operation: '', captureId: '', size: null, ...extra,
} as VersionTile);

describe('planAutoJoin (#262/#239)', () => {
  it('seeds a legacy canvas without placing its history, keeping running work eligible', () => {
    const plan = planAutoJoin([
      tile('t1', '2026-10-19T01:00:00Z'),
      tile('t2', '2026-10-19T02:00:00Z', { status: 'running', assets: [] }),
    ], null);
    expect(plan.place).toEqual([]);
    expect(plan.placed).toEqual([SINCE_PREFIX + '2026-10-19T02:00:00Z', 't1#t1-a1']);
    // When t2 finishes it joins the canvas once.
    const next = planAutoJoin([tile('t1', '2026-10-19T01:00:00Z'), tile('t2', '2026-10-19T02:00:00Z')], plan.placed);
    expect(next.place.map((item) => item.key)).toEqual(['t2#t2-a1']);
    expect(planAutoJoin([tile('t2', '2026-10-19T02:00:00Z')], next.placed).place).toEqual([]);
  });

  it('never re-places a result the user removed from the canvas', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z', 't1#t1-a1'];
    expect(planAutoJoin([tile('t1', '2026-10-19T01:00:00Z')], placed)).toEqual({ placed: null, register: [], place: [] });
  });

  it('places every image of a multi-image task and concurrent tasks, each once', () => {
    const many = tile('t3', '2026-10-19T03:00:00Z', { assets: [
      { id: 'x1', role: 'image', variant: '', mediaType: 'image/png', ordinal: 1, url: '/x1.png', kind: 'image' },
      { id: 'x2', role: 'image', variant: '', mediaType: 'image/png', ordinal: 2, url: '/x2.png', kind: 'image' },
    ] } as Partial<VersionTile>);
    const plan = planAutoJoin([many, tile('t4', '2026-10-19T03:30:00Z')], [SINCE_PREFIX + '2026-10-19T00:00:00Z']);
    expect(plan.place.map((item) => item.key)).toEqual(['t3#x1', 't3#x2', 't4#t4-a1']);
  });

  it('does not back-fill older versions loaded later, nor models, uploads, captures or failures', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T05:00:00Z'];
    const plan = planAutoJoin([
      tile('old', '2026-10-18T00:00:00Z'),
      tile('m', '2026-10-19T06:00:00Z', { domain: 'model' }),
      tile('u', '2026-10-19T06:00:00Z', { origin: 'upload' }),
      tile('c', '2026-10-19T06:00:00Z', { origin: 'capture' }),
      tile('f', '2026-10-19T06:00:00Z', { status: 'failed', assets: [] }),
    ], placed);
    expect(plan).toEqual({ placed: null, register: [], place: [] });
  });

  it('compares times, not strings, when the watermark and the task use different timestamp formats', () => {
    // A seed taken from a running task (toISOString, 3 fraction digits) and the finished task from the server
    // (Go RFC3339Nano, up to 9 digits): as strings "…00.123456Z" sorts before "…00.123Z".
    const placed = [SINCE_PREFIX + '2026-10-19T02:00:00.123Z'];
    expect(planAutoJoin([tile('t2', '2026-10-19T02:00:00.123456Z')], placed).place.map((item) => item.key)).toEqual(['t2#t2-a1']);
    // No fraction at all ("…01Z") sorts after "…00.999Z" as a string too, but must still be compared as time.
    expect(planAutoJoin([tile('t3', '2026-10-19T02:00:01Z')], [SINCE_PREFIX + '2026-10-19T02:00:00.999Z']).place).toHaveLength(1);
    // An offset is the same instant written another way; earlier results stay out.
    expect(planAutoJoin([tile('t4', '2026-10-19T10:00:00.1+08:00')], placed).place).toEqual([]);
    expect(planAutoJoin([tile('t5', '2026-10-19T10:00:01+08:00')], placed).place).toHaveLength(1);
  });

  it('seeds the watermark from the earliest running task by time, keeping its own text', () => {
    const plan = planAutoJoin([
      tile('a', '2026-10-19T02:00:00.5Z', { status: 'running', assets: [] }),
      // Earliest in time, but "…00Z" sorts after "…00.5Z" as a string.
      tile('b', '2026-10-19T02:00:00Z', { status: 'running', assets: [] }),
      tile('c', '2026-10-19T02:00:00.4Z', { status: 'pending', assets: [] }),
    ], null);
    expect(plan.placed).toEqual([SINCE_PREFIX + '2026-10-19T02:00:00Z']);
    const latest = planAutoJoin([tile('x', '2026-10-19T02:00:00.9Z'), tile('y', '2026-10-19T02:00:00Z')], null);
    expect(latest.placed?.[0]).toBe(SINCE_PREFIX + '2026-10-19T02:00:00.9Z');
  });

  it('registers only suppressed results up front; a normal picture waits until it is on the board', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z'];
    const plan = planAutoJoin([
      tile('inpaint', '2026-10-19T01:00:00Z'),
      tile('normal', '2026-10-19T01:01:00Z'),
    ], placed, { suppressTaskIds: new Set(['inpaint']) });
    expect(plan.register).toEqual(['inpaint#inpaint-a1']);
    expect(plan.place.map((result) => result.key)).toEqual(['normal#normal-a1']);
    expect(plan.placed).toEqual([...placed, 'inpaint#inpaint-a1', 'normal#normal-a1']);
  });

  it('registers the legacy seed together with the history it records', () => {
    const plan = planAutoJoin([tile('t1', '2026-10-19T01:00:00Z')], null);
    expect(plan.register).toEqual(plan.placed);
    expect(plan.place).toEqual([]);
  });

  it('records results the user placed explicitly', () => {
    expect(withPlaced(['since:x', 'a'], ['a', 'b'])).toEqual(['since:x', 'a', 'b']);
    expect(withPlaced(['since:x', 'a'], ['a'])).toBeNull();
    expect(withPlaced(null, ['a'])).toBeNull();
  });
});

describe('inpaint raw results (#308/#309)', () => {
  it('registers a suppressed task without placing its raw picture, and keeps other tasks', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z'];
    const plan = planAutoJoin([
      tile('inpaint', '2026-10-19T01:00:00Z'),
      tile('normal', '2026-10-19T01:01:00Z'),
    ], placed, { suppressTaskIds: new Set(['inpaint']) });
    expect(plan.place.map((item) => item.taskId)).toEqual(['normal']);
    expect(plan.placed).toEqual([...placed, 'inpaint#inpaint-a1', 'normal#normal-a1']);
  });

  it('keeps a suppressed task registered across later plans', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z', 'inpaint#inpaint-a1'];
    expect(planAutoJoin([tile('inpaint', '2026-10-19T01:00:00Z')], placed, { suppressTaskIds: new Set(['inpaint']) }))
      .toEqual({ placed: null, register: [], place: [] });
  });

  it('keeps the legacy seed rule even for a suppressed task', () => {
    const plan = planAutoJoin([tile('inpaint', '2026-10-19T01:00:00Z')], null, { suppressTaskIds: new Set(['inpaint']) });
    expect(plan.place).toEqual([]);
    expect(plan.placed).toEqual([SINCE_PREFIX + '2026-10-19T01:00:00Z', 'inpaint#inpaint-a1']);
  });

  it('places and registers a duplicated lineage entry once', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z'];
    const plan = planAutoJoin([tile('t1', '2026-10-19T01:00:00Z'), tile('t1', '2026-10-19T01:00:00Z')], placed);
    expect(plan.place.map((item) => item.key)).toEqual(['t1#t1-a1']);
    expect(plan.placed).toEqual([...placed, 't1#t1-a1']);
  });

  it('suppresses an inpaint result from the server operation even without a local job', () => {
    // A retry has a new task id and no local InpaintJob, but the server keeps the frozen inpaint operation.
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z'];
    const retry = tile('retry', '2026-10-19T01:00:00Z', { operation: 'inpaint' });
    const plan = planAutoJoin([retry, tile('normal', '2026-10-19T01:01:00Z')], placed);
    expect(plan.place.map((item) => item.taskId)).toEqual(['normal']);
    expect(plan.placed).toEqual([...placed, 'retry#retry-a1', 'normal#normal-a1']);
    // The same operation keeps it registered on later plans.
    expect(planAutoJoin([retry], plan.placed).place).toEqual([]);
  });

  it('registers a duplicated operation-suppressed lineage entry once', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z'];
    const retry = tile('retry', '2026-10-19T01:00:00Z', { operation: 'inpaint' });
    expect(planAutoJoin([retry, { ...retry }], placed)).toEqual({
      placed: [...placed, 'retry#retry-a1'], register: ['retry#retry-a1'], place: [],
    });
  });

  it('registers a duplicated suppressed lineage entry once and places nothing from it', () => {
    const placed = [SINCE_PREFIX + '2026-10-19T00:00:00Z'];
    const plan = planAutoJoin([
      tile('inpaint', '2026-10-19T01:00:00Z'),
      tile('inpaint', '2026-10-19T01:00:00Z'),
    ], placed, { suppressTaskIds: new Set(['inpaint']) });
    expect(plan.place).toEqual([]);
    expect(plan.placed).toEqual([...placed, 'inpaint#inpaint-a1']);
  });
});
