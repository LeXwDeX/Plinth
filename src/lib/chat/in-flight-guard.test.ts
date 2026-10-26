import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

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
});

describe('the chat column decision handler', () => {
  const chat = readFileSync(new URL('../../components/studio/chat-column.tsx', import.meta.url), 'utf8');

  it('guards with a ref read synchronously, not with the render state', () => {
    expect(chat).toContain('const deciding = useRef(new InFlightGuard());');
    expect(chat).toContain('if (!activeSessionId || !deciding.current.begin(card.requestId)) return;');
    expect(chat).toContain('deciding.current.end(requestId);');
    expect(chat).not.toContain('if (!activeSessionId || decisionBusy) return;');
  });

  it('marks only the card being decided as busy', () => {
    expect(chat).toContain('decisionBusy.has(card.requestId)');
    expect(chat).not.toContain('decisionBusy === card.requestId');
  });
});
