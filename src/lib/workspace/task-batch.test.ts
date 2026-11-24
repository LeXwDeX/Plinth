import { describe, expect, it } from 'vitest';

import { nextTaskBatch, statusBubbleShown, taskBatchProgress } from './task-progress';

const task = (taskId: string, progress = 0) => ({ taskId, progress, progressMaximum: 100 });

describe('the batch the task block counts (#546)', () => {
  it('gathers parallel tasks into one batch and keeps the ended ones until none runs', () => {
    let batch: ReadonlySet<string> = new Set();
    batch = nextTaskBatch(batch, [task('a'), task('b'), task('c'), task('d')]);
    expect(taskBatchProgress(batch, [task('a'), task('b'), task('c'), task('d')], 100)).toEqual({ done: 0, total: 4, percent: 0 });
    // Two ended: still four in the batch, two done.
    batch = nextTaskBatch(batch, [task('c', 50), task('d')]);
    expect(taskBatchProgress(batch, [task('c', 50), task('d')], 100)).toEqual({ done: 2, total: 4, percent: 63 });
    // A task started while the others still run joins the same batch.
    batch = nextTaskBatch(batch, [task('d'), task('e')]);
    expect(taskBatchProgress(batch, [task('d'), task('e')], 100)).toMatchObject({ done: 3, total: 5 });
  });

  it('starts over once every task has ended', () => {
    let batch = nextTaskBatch(new Set(), [task('a')]);
    batch = nextTaskBatch(batch, []);
    expect(batch.size).toBe(0);
    batch = nextTaskBatch(batch, [task('z')]);
    expect(taskBatchProgress(batch, [task('z')], 100)).toEqual({ done: 0, total: 1, percent: 0 });
  });

  it('returns the same batch when nothing new runs, so a render does not churn it', () => {
    const batch = nextTaskBatch(new Set(), [task('a')]);
    expect(nextTaskBatch(batch, [task('a')])).toBe(batch);
    const empty = new Set<string>();
    expect(nextTaskBatch(empty, [])).toBe(empty);
  });

  it('counts a running task the batch has not seen yet', () => {
    expect(taskBatchProgress(new Set(), [task('a', 40)], 100)).toEqual({ done: 0, total: 1, percent: 40 });
  });
});

describe('one expression of progress at a time (#546)', () => {
  it('shows the generating bubble only while the turn thinks and no task runs', () => {
    expect(statusBubbleShown(true, '正在生成…', 0)).toBe(true);
    expect(statusBubbleShown(true, '正在生成…', 3)).toBe(false);
    expect(statusBubbleShown(false, '正在生成…', 0)).toBe(false);
    expect(statusBubbleShown(true, '', 0)).toBe(false);
  });
});
