import { describe, expect, it } from 'vitest';

import { filterTasks, isTaskFilter } from './task-filter';

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
