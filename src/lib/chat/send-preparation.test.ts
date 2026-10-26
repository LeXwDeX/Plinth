import { describe, expect, it, vi } from 'vitest';
import { InFlightGuard } from './in-flight-guard';
import { withSessionSendLock } from './send-preparation';

describe('parameter preparation before starting a turn', () => {
  it('admits one Enter while its scoped PUT is delayed and starts exactly one turn afterwards', async () => {
    const guard = new InFlightGuard();
    let finishPUT!: () => void;
    const delayedPUT = new Promise<void>((resolve) => { finishPUT = resolve; });
    const startTurn = vi.fn();
    const prepare = vi.fn(async () => { await delayedPUT; startTurn(); });
    const first = withSessionSendLock(guard, 'session', prepare);
    await expect(withSessionSendLock(guard, 'session', prepare)).resolves.toBe(false);
    expect(prepare).toHaveBeenCalledTimes(1); expect(startTurn).not.toHaveBeenCalled();
    finishPUT(); await first;
    expect(startTurn).toHaveBeenCalledTimes(1); expect(guard.has('session')).toBe(false);
  });
  it('releases a failed preparation and leaves a different session independent', async () => {
    const guard = new InFlightGuard();
    await expect(withSessionSendLock(guard, 's1', async () => { throw new Error('PUT failed'); })).rejects.toThrow('PUT failed');
    expect(guard.has('s1')).toBe(false);
    guard.begin('s1'); const other = vi.fn(async () => {});
    await expect(withSessionSendLock(guard, 's2', other)).resolves.toBe(true);
    expect(other).toHaveBeenCalledOnce();
  });
});
