import { describe, expect, it, vi } from 'vitest';
import { SessionConsentController } from './session-consent';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const pending = (id: string) => ({ consent_pending: { cost: 1 }, consent_request_id: id, consent_decision: 'pending' });
const flush = async () => { await Promise.resolve(); await Promise.resolve(); };
function setup(scope = 'session-a') {
  const gets: ReturnType<typeof deferred<Record<string, unknown> | null>>[] = [];
  const puts: ReturnType<typeof deferred<void>>[] = [];
  const client = {
    getConsent: vi.fn((_scope: string, _signal?: AbortSignal) => {
      const next = deferred<Record<string, unknown> | null>(); gets.push(next); return next.promise;
    }),
    putConsent: vi.fn((_scope: string, _id: string, _approved: boolean, _signal?: AbortSignal) => {
      const next = deferred<void>(); puts.push(next); return next.promise;
    }),
  };
  let tick = () => {};
  const clock = {
    setInterval: vi.fn((callback: () => void, _milliseconds: number) => { tick = callback; return 1; }),
    clearInterval: vi.fn(),
  };
  const controller = new SessionConsentController(scope, client, 723, clock);
  return { controller, client, gets, puts, clock, tick: () => tick() };
}

describe('session consent request coordination', () => {
  it('uses the supplied interval and serializes polling without submitting decisions', async () => {
    const s = setup(); s.controller.start(); s.tick();
    expect(s.clock.setInterval).toHaveBeenCalledWith(expect.any(Function), 723);
    expect(s.client.getConsent).toHaveBeenCalledTimes(1);
    s.gets[0].resolve(pending('request-a')); await flush(); s.tick();
    expect(s.client.getConsent).toHaveBeenCalledTimes(2);
    expect(s.client.putConsent).not.toHaveBeenCalled();
    s.controller.stop();
    expect(s.client.getConsent.mock.calls[1][1]?.aborted).toBe(true);
    expect(s.clock.clearInterval).toHaveBeenCalledWith(1);
  });

  it('starts a new scope empty and ignores a late old GET even when the client ignores abort', async () => {
    const old = setup(); old.controller.start();
    const next = new SessionConsentController('session-b', old.client, 723, old.clock);
    old.controller.stop(); next.start();
    expect(next.getSnapshot()).toEqual({ state: null, busy: false });
    old.gets[0].resolve(pending('old')); await flush();
    expect(old.controller.getSnapshot().state).toBeNull();
    expect(next.getSnapshot().state).toBeNull();
    old.gets[1].resolve(pending('new')); await flush();
    expect(next.getSnapshot().state).toEqual(pending('new'));
    next.stop();
  });

  it('requires the current pending request ID and locks synchronously against double clicks', async () => {
    const s = setup(); s.controller.start();
    s.gets[0].resolve(pending('current')); await flush();
    await s.controller.decide('stale', true);
    expect(s.client.putConsent).not.toHaveBeenCalled();
    const decision = s.controller.decide('current', false);
    const duplicate = s.controller.decide('current', true);
    s.tick();
    expect(s.controller.getSnapshot().busy).toBe(true);
    expect(s.client.putConsent).toHaveBeenCalledTimes(1);
    expect(s.client.putConsent.mock.calls[0].slice(0, 3)).toEqual(['session-a', 'current', false]);
    s.puts[0].resolve(); await flush();
    s.gets[1].resolve({ ...pending('current'), consent_decision: 'denied' });
    await Promise.all([decision, duplicate]);
    expect(s.controller.getSnapshot().busy).toBe(false);
    await s.controller.decide('current', true);
    expect(s.client.putConsent).toHaveBeenCalledTimes(1);
    s.controller.stop();
  });

  it('prevents a polling response from overwriting the decision readback', async () => {
    const s = setup(); s.controller.start();
    s.gets[0].resolve(pending('current')); await flush(); s.tick();
    const decision = s.controller.decide('current', true);
    expect(s.client.getConsent.mock.calls[1][1]?.aborted).toBe(true);
    s.puts[0].resolve(); await flush();
    s.gets[2].resolve({ ...pending('current'), consent_decision: 'approved' }); await decision;
    s.gets[1].resolve(pending('current')); await flush();
    expect(s.controller.getSnapshot().state?.consent_decision).toBe('approved');
    s.controller.stop();
  });

  it('aborts an old decision and prevents its late completion from reading or changing the new session', async () => {
    const s = setup(); s.controller.start(); s.gets[0].resolve(pending('old')); await flush();
    const decision = s.controller.decide('old', true);
    s.controller.stop();
    const next = new SessionConsentController('session-b', s.client, 723, s.clock); next.start();
    s.gets[1].resolve(pending('new')); await flush();
    s.puts[0].resolve(); await decision;
    expect(s.client.putConsent.mock.calls[0][3]?.aborted).toBe(true);
    expect(s.client.getConsent).toHaveBeenCalledTimes(2);
    expect(next.getSnapshot()).toEqual({ state: pending('new'), busy: false });
    next.stop();
  });

  it('ignores a late decision GET after disposal and reports current decision errors with the lock released', async () => {
    const s = setup(); s.controller.start(); s.gets[0].resolve(pending('old')); await flush();
    const decision = s.controller.decide('old', true); s.puts[0].resolve(); await flush();
    s.controller.stop(); s.gets[1].resolve(pending('old')); await decision;
    expect(s.controller.getSnapshot()).toEqual({ state: null, busy: false });
    s.controller.start(); s.gets[2].resolve(pending('retry')); await flush();
    const failure = s.controller.decide('retry', true);
    s.puts[1].reject(new Error('failed'));
    await expect(failure).rejects.toThrow('failed');
    expect(s.controller.getSnapshot()).toEqual({ state: pending('retry'), busy: false });
    s.controller.stop();
  });
});
