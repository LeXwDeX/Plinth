import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { createLineageRefreshGate, createSettledCounter, settledOutsideChain, lineageWatchSignature, type LineageWatch } from './lineage-refresh';

const watch = (sessionId: string, settled = '', viewsDone = '', revision = 0): LineageWatch =>
  ({ sessionId, signature: lineageWatchSignature(settled, viewsDone, revision) });

/** Twelve consumers of one session, as on the studio page (#263: 22–26 identical lineage GETs per load). */
function consumers(gate: ReturnType<typeof createLineageRefreshGate>, count = 12) {
  const seen: (LineageWatch | null)[] = Array.from({ length: count }, () => null);
  return (next: LineageWatch) => seen.reduce((refreshes, previous, index) => {
    seen[index] = next;
    return refreshes + (gate(previous, next) ? 1 : 0);
  }, 0);
}

describe('explicit lineage refreshes shared by every useSessionVersions consumer', () => {
  it('asks for none when a session opens: the shared SWR key revalidates once on its own', () => {
    const observe = consumers(createLineageRefreshGate(() => 0));
    expect(observe(watch('s1', 'a', '', 3))).toBe(0);
    // Switching to another session (or back) is an open as well.
    expect(observe(watch('s2', 'b', '', 3))).toBe(0);
    expect(observe(watch('s1', 'a', '', 3))).toBe(0);
  });

  it('asks for exactly one when something that changes the chain is seen by every consumer at once', () => {
    let now = 1_000;
    const observe = consumers(createLineageRefreshGate(() => now));
    observe(watch('s1'));
    expect(observe(watch('s1', 'task-1'))).toBe(1);
    expect(observe(watch('s1', 'task-1'))).toBe(0);
    now += 50;
    expect(observe(watch('s1', 'task-1', 'view-1'))).toBe(1);
    expect(observe(watch('s1', 'task-1', 'view-1', 1))).toBe(1);
  });

  it('keeps sessions apart, and a later identical change still refreshes once the moment has passed', () => {
    let now = 0;
    const gate = createLineageRefreshGate(() => now);
    expect(gate(watch('s1'), watch('s1', 'x'))).toBe(true);
    expect(gate(watch('s2'), watch('s2', 'x'))).toBe(true);
    expect(gate(watch('s1'), watch('s1', 'x'))).toBe(false);
    now += 10_000;
    expect(gate(watch('s1'), watch('s1', 'x'))).toBe(true);
  });

  it('is what useSessionVersions uses instead of a refresh on every mount', () => {
    const source = readFileSync(new URL('../../hooks/use-session-versions.ts', import.meta.url), 'utf8');
    expect(source).not.toMatch(/useEffect\(\(\) => \{ if \(enabled\) void mutate\(\); \}/);
    expect(source).toContain('claimLineageRefresh(previous, next)');
  });

  it('does not count finished tasks the loaded chain already shows (opening a session reads once, #263)', () => {
    expect(settledOutsideChain(['t2', 't1'], null)).toBe('');
    expect(settledOutsideChain(['t2', 't1'], new Set(['t1', 't2']))).toBe('');
    expect(settledOutsideChain(['t3', 't1'], new Set(['t1']))).toBe('t3');
  });

  it('gives consumers mounted at different times the same signature, so one change refreshes once', () => {
    const count = createSettledCounter();
    const gate = createLineageRefreshGate(() => 0);
    // X was mounted when T succeeded; Y mounts later (a details panel, the other stage).
    const x0 = watch('s1', count('s1', ''));
    const x1 = watch('s1', count('s1', 't'));
    expect(gate(x0, x1)).toBe(true);
    const y0 = watch('s1', count('s1', ''));
    // U succeeds: both consumers see the same signature and only the first refreshes.
    const signature = count('s1', 'u');
    expect(gate(x1, watch('s1', signature))).toBe(true);
    expect(gate(y0, watch('s1', signature))).toBe(false);
  });

  it('only grows: a task the refresh brings into the chain does not change the signature back', () => {
    const count = createSettledCounter();
    expect(count('s1', 't')).toBe('t');
    expect(count('s1', '')).toBe('t');
    expect(count('s2', '')).toBe('');
  });

  it('is shared by every useSessionVersions consumer', () => {
    const source = readFileSync(new URL('../../hooks/use-session-versions.ts', import.meta.url), 'utf8');
    expect(source).toContain('settledOutsideChain(settledIds, chainFinished)');
    expect(source).toContain('countSettledChanges(sessionId, settled)');
    expect(source).not.toContain('useRef<{ sessionId: string; ids: Set<string> }');
  });
});
