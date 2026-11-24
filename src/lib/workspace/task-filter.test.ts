import { describe, expect, it } from 'vitest';

import { filterTasks, isQueuedTask, isTaskFilter, TASK_FILTERS } from './task-filter';

const task = (id: string, extra: Partial<{ terminal: boolean; notFound: boolean; failure: { detail: string } | null; liveError: string }> = {}) =>
  ({ taskId: id, terminal: false, notFound: false, failure: null, liveError: '', ...extra }) as never as Parameters<typeof filterTasks>[0][number] & { taskId: string };

describe('filterTasks', () => {
  const running = task('running');
  const done = task('done', { terminal: true });
  const failed = task('failed', { terminal: true, failure: { detail: 'x' } });
  const lost = task('lost', { terminal: true, notFound: true });
  const all = [running, done, failed, lost];

  it('keeps everything for all', () => {
    expect(filterTasks(all, 'all')).toHaveLength(4);
  });

  it('keeps what is still running for active', () => {
    expect(filterTasks(all, 'active').map((item) => (item as { taskId: string }).taskId)).toEqual(['running']);
  });

  it('keeps failed and lost tasks for failed', () => {
    expect(filterTasks(all, 'failed').map((item) => (item as { taskId: string }).taskId)).toEqual(['failed', 'lost']);
  });

  it('recognises the stored values', () => {
    expect(isTaskFilter('failed')).toBe(true);
    expect(isTaskFilter('hidden')).toBe(false);
  });
});

describe('the queue filter (#546)', () => {
  // The Schema's outcome per state; QUEUED, PENDING and RETRY_PENDING wait.
  const outcomeOf = (state: string) => (['QUEUED', 'PENDING', 'RETRY_PENDING'].includes(state) ? 'pending' : state === 'SUCCEEDED' ? 'success' : 'running');
  const task = (id: string, state: string, extra: object = {}) =>
    ({ taskId: id, state, terminal: state === 'SUCCEEDED', notFound: false, failure: null, liveError: '', ...extra }) as never as Parameters<typeof filterTasks>[0][number];
  const all = [
    task('queued', 'QUEUED'),
    task('retry', 'RETRY_PENDING'),
    task('provider', 'RUNNING', { queueNotice: { reason: 'upstream_queued', detail: 'x' } }),
    task('running', 'RUNNING'),
    task('done', 'SUCCEEDED'),
    task('unknown', ''),
  ];

  it('keeps the tasks whose Schema outcome waits, and those the server says are queued', () => {
    expect(filterTasks(all, 'queued', outcomeOf).map((item) => (item as unknown as { taskId: string }).taskId)).toEqual(['queued', 'retry', 'provider']);
    expect(isQueuedTask(task('gone', 'QUEUED', { notFound: true }) as never, outcomeOf)).toBe(false);
  });

  it('is a filter the preference may hold, between in progress and failed', () => {
    expect(TASK_FILTERS).toEqual(['all', 'active', 'queued', 'failed']);
    expect(isTaskFilter('queued')).toBe(true);
  });
});
