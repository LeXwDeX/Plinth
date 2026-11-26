// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest';

import { parseThreeViewGroup, type ThreeViewGroup } from './three-view';
import { forgetPendingAdvance, pendingAdvanceOpen, readPendingAdvances, rememberPendingAdvance, settlePendingAdvances, type PendingAdvance } from './three-view-pending';

const groupWith = (left: Record<string, unknown>): ThreeViewGroup => parseThreeViewGroup({
  run_id: 'run_1',
  slots: [{ key: 'front', status: 'succeeded' }, { key: 'left', ...left }],
}) as ThreeViewGroup;

const start: PendingAdvance = { sessionId: 's1', runId: 'run_1', slotKey: 'left', action: 'start', attempt: 0, at: 1_000 };

afterEach(() => { window.sessionStorage.clear(); });

describe('pending three-view starts (#636)', () => {
  it('is open while the view still offers the same action at the same attempt and the limit has not run out', () => {
    const startable = [groupWith({ status: 'not_started', startable: true })];
    expect(pendingAdvanceOpen(start, startable, 2_000, 5_000)).toBe(true);
    expect(pendingAdvanceOpen(start, startable, 7_000, 5_000)).toBe(false);
    expect(pendingAdvanceOpen(start, startable, 2_000, null)).toBe(false);
    expect(pendingAdvanceOpen(start, [groupWith({ status: 'running', attempt: 1 })], 2_000, 5_000)).toBe(false);
    expect(pendingAdvanceOpen(start, [], 2_000, 5_000)).toBe(false);
    const retry: PendingAdvance = { ...start, action: 'retry', attempt: 1 };
    expect(pendingAdvanceOpen(retry, [groupWith({ status: 'failed', retryable: true, attempt: 1 })], 2_000, 5_000)).toBe(true);
    expect(pendingAdvanceOpen(retry, [groupWith({ status: 'failed', retryable: true, attempt: 2 })], 2_000, 5_000)).toBe(false);
  });

  it('forgets the settled starts of the open session and keeps those of other sessions', () => {
    const other: PendingAdvance = { ...start, sessionId: 's2' };
    rememberPendingAdvance(start);
    rememberPendingAdvance(other);
    expect(settlePendingAdvances('s1', [groupWith({ status: 'running', attempt: 1 })], 2_000, 5_000)).toEqual([]);
    expect(readPendingAdvances()).toEqual([other]);
    forgetPendingAdvance(other);
    expect(window.sessionStorage.getItem('plinth:three-view-pending')).toBeNull();
  });

  it('remembers one request per view, the latest', () => {
    rememberPendingAdvance(start);
    rememberPendingAdvance({ ...start, at: 3_000 });
    expect(readPendingAdvances()).toEqual([{ ...start, at: 3_000 }]);
    window.sessionStorage.setItem('plinth:three-view-pending', 'not json');
    expect(readPendingAdvances()).toEqual([]);
  });
});
