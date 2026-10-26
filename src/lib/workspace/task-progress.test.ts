import { describe, expect, it } from 'vitest';

import { chatTaskCards, elapsedClockNeeded, elapsedSeconds, formatElapsed, progressPercent } from '@/lib/workspace/task-progress';
import type { TrackedTask } from '@/store/workspace-store';

function task(patch: Partial<TrackedTask> = {}): TrackedTask {
  return {
    taskId: 't1', sessionId: 's1', capability: 'image-to-3d', prompt: '', state: 'running', progress: 0, progressMaximum: 100,
    terminal: false, failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false,
    notFound: false, updatedAt: 0, ...patch,
  };
}

describe('progress card arithmetic', () => {
  it('reads the percentage on the task scale and clamps it', () => {
    expect(progressPercent(task({ progress: 45 }), 100)).toBe(45);
    expect(progressPercent(task({ progress: 1, progressMaximum: 4 }), 100)).toBe(25);
    expect(progressPercent(task({ progress: 140 }), 100)).toBe(100);
    expect(progressPercent(task({ progress: -3 }), 100)).toBe(0);
    expect(progressPercent(task({ progress: 50, progressMaximum: 0 }), 200)).toBe(25);
    expect(progressPercent(task({ progress: 50, progressMaximum: 0 }), 0)).toBe(0);
  });

  it('counts time used from the server start and distrusts a start that cannot be right', () => {
    const now = 1_000_000_000;
    expect(elapsedSeconds(task({ startedAt: now - 83_000 }), now)).toBe(83);
    expect(elapsedSeconds(task({ startedAt: now + 60_000, updatedAt: now - 5_000 }), now)).toBe(5);
    expect(elapsedSeconds(task({ startedAt: now - 3 * 24 * 3600_000 }), now, now - 7_000)).toBe(7);
    expect(elapsedSeconds(task({ startedAt: undefined, updatedAt: now - 2_000 }), now)).toBe(2);
  });

  it('writes m:ss, then h:mm:ss', () => {
    expect(formatElapsed(0)).toBe('0:00');
    expect(formatElapsed(83)).toBe('1:23');
    expect(formatElapsed(3725)).toBe('1:02:05');
  });
});

describe('cards in the conversation', () => {
  const none = new Set<string>();

  it('shows running tasks of this session only', () => {
    const cards = chatTaskCards([task(), task({ taskId: 't2', sessionId: 's2' }), task({ taskId: 't3', terminal: true, state: 'succeeded' })], 's1', none, none);
    expect(cards.running.map((item) => item.taskId)).toEqual(['t1']);
    expect(cards.failed).toEqual([]);
    expect(chatTaskCards([task()], null, none, none).running).toEqual([]);
  });

  it('notices a failure only for a task this page watched run, and only until dismissed', () => {
    const failed = task({ taskId: 'f1', terminal: true, failure: { failureClass: 'insufficient_balance', detail: '', retryable: false, ambiguous: false } });
    const old = task({ taskId: 'f2', terminal: true, liveError: 'boom' });
    expect(chatTaskCards([failed, old], 's1', new Set(['f1']), none).failed.map((item) => item.taskId)).toEqual(['f1']);
    expect(chatTaskCards([failed, old], 's1', new Set(['f1']), new Set(['f1'])).failed).toEqual([]);
    expect(chatTaskCards([failed, old], 's1', none, none).failed).toEqual([]);
  });

  it('does not call a succeeded or missing task a failure', () => {
    const done = task({ taskId: 'ok', terminal: true, state: 'succeeded' });
    const missing = task({ taskId: 'gone', terminal: true, notFound: true, liveError: 'x' });
    expect(chatTaskCards([done, missing], 's1', new Set(['ok', 'gone']), none).failed).toEqual([]);
  });
});

describe('elapsedClockNeeded', () => {
  it('ticks only for the running cards while a task runs', () => {
    const running = { running: [task()], failed: [] };
    expect(elapsedClockNeeded('running', running)).toBe(true);
    // The failure notices show no elapsed time: a running task elsewhere must not re-render them every second.
    expect(elapsedClockNeeded('failed', running)).toBe(false);
    expect(elapsedClockNeeded('running', { running: [], failed: [task({ terminal: true })] })).toBe(false);
  });
});
