import { describe, expect, it } from 'vitest';

import type { ChatContextCompaction } from '@/lib/chat/browser-client';
import { compactionPlacement } from '@/lib/chat/context-compaction';

const at = (iso: string) => Date.parse(iso);
const message = (id: string, iso: string) => ({ id, createdAt: at(iso) });
const compaction = (id: string, iso: string, trigger: 'manual' | 'auto' = 'manual'): ChatContextCompaction =>
  ({ id, createdAt: iso, trigger, rounds: 3, throughMessageId: 'x' });

const messages = [
  message('m1', '2026-10-22T01:00:00Z'),
  message('m2', '2026-10-22T01:01:00Z'),
  message('m3', '2026-10-22T02:00:00Z'),
  message('m4', '2026-10-22T02:01:00Z'),
];

describe('compactionPlacement (#322)', () => {
  it('puts each compaction after the last message made no later than it', () => {
    const placed = compactionPlacement([
      compaction('c1', '2026-10-22T01:30:00Z'),
      compaction('c2', '2026-10-22T02:01:00Z', 'auto'),
    ], messages, false);
    expect([...placed.byMessage.entries()].map(([id, list]) => [id, list.map((item) => item.id)])).toEqual([['m2', ['c1']], ['m4', ['c2']]]);
    expect(placed.leading).toEqual([]);
  });

  it('keeps several compactions after one message in time order', () => {
    const placed = compactionPlacement([
      compaction('late', '2026-10-22T03:00:00Z'),
      compaction('early', '2026-10-22T02:30:00Z'),
    ], messages, false);
    expect(placed.byMessage.get('m4')?.map((item) => item.id)).toEqual(['early', 'late']);
  });

  it('leaves out a compaction of history not loaded yet, and puts it first once the start is loaded', () => {
    const old = [compaction('c0', '2026-10-21T00:00:00Z')];
    expect(compactionPlacement(old, messages, true)).toEqual({ byMessage: new Map(), leading: [] });
    expect(compactionPlacement(old, messages, false).leading.map((item) => item.id)).toEqual(['c0']);
  });

  it('puts a compaction with an unreadable time last', () => {
    const placed = compactionPlacement([compaction('odd', 'not a time')], messages, false);
    expect(placed.byMessage.get('m4')?.map((item) => item.id)).toEqual(['odd']);
  });

  it('places nothing when the conversation shows no messages', () => {
    expect(compactionPlacement([compaction('c1', '2026-10-22T01:30:00Z')], [], false)).toEqual({ byMessage: new Map(), leading: [] });
  });
});
