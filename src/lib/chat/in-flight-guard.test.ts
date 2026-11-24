import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';

import { InFlightGuard } from '@/lib/chat/in-flight-guard';

describe('InFlightGuard', () => {
  it('lets only the first of two synchronous clicks on one card through', () => {
    const guard = new InFlightGuard();
    expect([guard.begin('r1'), guard.begin('r1')]).toEqual([true, false]);
  });

  it('does not let one busy card block another', () => {
    const guard = new InFlightGuard();
    expect(guard.begin('r1')).toBe(true);
    expect(guard.begin('r2')).toBe(true);
    expect(guard.has('r1') && guard.has('r2')).toBe(true);
  });

  it('accepts the card again once its decision ended, however it ended', () => {
    const guard = new InFlightGuard();
    guard.begin('r1');
    guard.end('r1');
    expect(guard.begin('r1')).toBe(true);
  });

  it('runs the work holding the claim and releases it when the work ends (#503)', async () => {
    const guard = new InFlightGuard();
    const seen: boolean[] = [];
    expect(await guard.run('r1', async () => { seen.push(guard.has('r1')); })).toBe(true);
    expect(seen).toEqual([true]);
    expect(guard.has('r1')).toBe(false);
  });

  it('does not run the work while the same request is claimed (#503)', async () => {
    const guard = new InFlightGuard();
    guard.begin('r1');
    const work = vi.fn(async () => {});
    expect(await guard.run('r1', work)).toBe(false);
    expect(work).not.toHaveBeenCalled();
    expect(guard.has('r1')).toBe(true);
  });

  it('releases the claim when the work throws (#503)', async () => {
    const guard = new InFlightGuard();
    await expect(guard.run('r1', async () => { throw new Error('boom'); })).rejects.toThrow('boom');
    expect(guard.has('r1')).toBe(false);
  });
});

describe('the chat column decision handler', () => {
  const chat = readFileSync(new URL('../../components/studio/chat-column.tsx', import.meta.url), 'utf8');

  it('guards with a ref read synchronously, not with the render state', () => {
    expect(chat).toContain('const deciding = useRef(new InFlightGuard());');
    expect(chat).toContain('deciding.current.run(requestId, async () => {');
    expect(chat).not.toContain('if (!activeSessionId || decisionBusy) return;');
  });

  it('marks only the card being decided as busy', () => {
    expect(chat).toContain('decisionBusy.has(card.requestId)');
    expect(chat).not.toContain('decisionBusy === card.requestId');
  });

  it('guards a production plan decision the same way, and releases it however it ends', () => {
    expect(chat).toContain('deciding.current.run(budgetId, async () => {');
    expect(chat).toContain('decisionBusy.has(item.requestId)');
  });

  // #503: a hand-written claim once read the result inverted; every handler goes through `run`.
  it('never claims or releases a request by hand', () => {
    expect(chat).not.toMatch(/deciding\.current\.(?:begin|end)\(/);
  });
});
