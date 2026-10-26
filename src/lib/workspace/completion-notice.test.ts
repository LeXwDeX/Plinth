import { describe, expect, it, vi } from 'vitest';

import {
  canRequestPermission,
  mayNotify,
  readNoticePermission,
  requestPermissionFromGesture,
  scanTasks,
  type NoticeTask,
} from '@/lib/workspace/completion-notice';

const task = (taskId: string, extra: Partial<NoticeTask> = {}): NoticeTask => ({
  taskId, terminal: false, notFound: false, failure: null, downloadables: [], prompt: 'p', capability: 'text-to-image', ...extra,
});

describe('completion notice scan', () => {
  it('reports a task that was running and has now succeeded or failed', () => {
    const scan = scanTasks(new Set(['a', 'b']), [
      task('a', { terminal: true, downloadables: [{}] }),
      task('b', { terminal: true, failure: { detail: 'x' } }),
    ]);
    expect(scan.finished.map((item) => [item.task.taskId, item.outcome])).toEqual([['a', 'done'], ['b', 'failed']]);
    expect(scan.active.size).toBe(0);
  });

  it('ignores tasks this page never saw running, such as history restored after a reload', () => {
    const scan = scanTasks(new Set(), [task('old', { terminal: true, downloadables: [{}] })]);
    expect(scan.finished).toEqual([]);
  });

  it('ignores a task the user cancelled and one that vanished', () => {
    const scan = scanTasks(new Set(['c', 'g']), [task('c', { terminal: true }), task('g', { terminal: true, notFound: true, failure: {} })]);
    expect(scan.finished).toEqual([]);
  });

  it('keeps running tasks active without attaching permission behavior to their registration', () => {
    const scan = scanTasks(new Set(['a']), [task('a'), task('n')]);
    expect([...scan.active].sort()).toEqual(['a', 'n']);
    expect(scan).not.toHaveProperty('started');
  });
});

describe('notification permission policy', () => {
  const base = { desktop: true, supported: true, permission: 'default' as const };
  it('allows an explicit request only on a supported desktop with undecided permission', () => {
    expect(canRequestPermission(base)).toBe(true);
    expect(canRequestPermission({ ...base, permission: 'granted' })).toBe(false);
    expect(canRequestPermission({ ...base, permission: 'denied' })).toBe(false);
    expect(canRequestPermission({ ...base, desktop: false })).toBe(false);
    expect(canRequestPermission({ ...base, supported: false, permission: 'unsupported' })).toBe(false);
    expect(canRequestPermission({ ...base, pending: true })).toBe(false);
  });

  it('invokes requestPermission synchronously for the explicit user gesture path', async () => {
    const events: string[] = [];
    const result = requestPermissionFromGesture(
      () => base,
      () => events.push('pending'),
      () => {
        events.push('request');
        return Promise.resolve('granted');
      },
    );
    expect(events).toEqual(['pending', 'request']);
    await expect(result).resolves.toBe('granted');
  });

  it('does not invoke the browser API when the permission state cannot be requested', () => {
    const request = () => { throw new Error('must not request'); };
    const markPending = () => { throw new Error('must not mark pending'); };
    expect(requestPermissionFromGesture(() => ({ ...base, permission: 'denied' }), markPending, request)).toBeNull();
    expect(requestPermissionFromGesture(() => ({ ...base, desktop: false }), markPending, request)).toBeNull();
    expect(requestPermissionFromGesture(() => ({ ...base, supported: false, permission: 'unsupported' }), markPending, request)).toBeNull();
    expect(requestPermissionFromGesture(() => ({ ...base, pending: true }), markPending, request)).toBeNull();
  });

  it('reads current permission and desktop capability at the gesture, not from stale render state', () => {
    const originalNotification = globalThis.Notification;
    let desktop = true;
    const request = vi.fn(() => Promise.resolve('default' as NotificationPermission));
    class LiveNotification extends EventTarget {
      static permission: NotificationPermission = 'default';
      static requestPermission = request;
    }
    globalThis.Notification = LiveNotification as unknown as typeof Notification;
    const liveContext = () => ({
      desktop,
      supported: readNoticePermission() !== 'unsupported',
      permission: readNoticePermission(),
    });
    const markPending = vi.fn();
    try {
      const first = requestPermissionFromGesture(liveContext, markPending, () => Notification.requestPermission());
      expect(first).not.toBeNull();
      expect(request).toHaveBeenCalledTimes(1);
      expect(markPending).toHaveBeenCalledTimes(1);

      LiveNotification.permission = 'denied';
      expect(requestPermissionFromGesture(liveContext, markPending, () => Notification.requestPermission())).toBeNull();
      expect(request).toHaveBeenCalledTimes(1);

      LiveNotification.permission = 'default';
      desktop = false;
      expect(requestPermissionFromGesture(liveContext, markPending, () => Notification.requestPermission())).toBeNull();
      expect(request).toHaveBeenCalledTimes(1);
    } finally {
      globalThis.Notification = originalNotification;
    }
  });

  it('notifies only when granted on a desktop', () => {
    expect(mayNotify({ desktop: true, supported: true, permission: 'granted' })).toBe(true);
    expect(mayNotify({ desktop: true, supported: true, permission: 'denied' })).toBe(false);
    expect(mayNotify({ desktop: true, supported: true, permission: 'default' })).toBe(false);
    expect(mayNotify({ desktop: false, supported: true, permission: 'granted' })).toBe(false);
  });
});
