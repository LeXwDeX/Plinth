import { describe, expect, it } from 'vitest';

import { newestFirst, taskListPage } from './task-list-page';

const task = (id: string, startedAt?: number, state = 'RUNNING') => ({ id, startedAt, state });

describe('the task drawer order (#546)', () => {
  it('puts the newest task on top by creation time', () => {
    const order = newestFirst([task('old', 100), task('newest', 300), task('middle', 200)]).map((item) => item.id);
    expect(order).toEqual(['newest', 'middle', 'old']);
  });

  it('keeps a row in place when its state changes', () => {
    const before = [task('a', 100), task('b', 200), task('c', 300)];
    const after = before.map((item) => (item.id === 'a' ? { ...item, state: 'SUCCEEDED' } : item.id === 'c' ? { ...item, state: 'FAILED' } : item));
    expect(newestFirst(after).map((item) => item.id)).toEqual(newestFirst(before).map((item) => item.id));
  });

  it('puts a task with no known time on top, and the later registration first on a tie', () => {
    expect(newestFirst([task('a', 100), task('just-registered'), task('b', 100)]).map((item) => item.id)).toEqual(['just-registered', 'b', 'a']);
  });
});

describe('the task drawer pages (#546)', () => {
  const tasks = Array.from({ length: 23 }, (_, index) => index);

  it('shows the first page only, and says how many more there are', () => {
    expect(taskListPage(tasks, 1, 10)).toEqual({ shown: tasks.slice(0, 10), more: 13 });
  });

  it('opens ten more with each page, up to the end', () => {
    expect(taskListPage(tasks, 2, 10).shown).toHaveLength(20);
    expect(taskListPage(tasks, 3, 10)).toEqual({ shown: tasks, more: 0 });
  });

  it('shows everything with no "more" when the list fits on a page', () => {
    expect(taskListPage(tasks.slice(0, 10), 1, 10).more).toBe(0);
  });
});
