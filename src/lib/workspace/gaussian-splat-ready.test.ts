import { describe, expect, it } from 'vitest';

import { GaussianSplatReadyGate } from './gaussian-splat-ready';

describe('GaussianSplatReadyGate', () => {
  it('accepts a visible frame just before the absolute deadline', () => {
    const gate = new GaussianSplatReadyGate(100, 30_000);

    expect(gate.markReady(30_099)).toBe(true);
    expect(gate.state).toBe('ready');
  });

  it('rejects a frame at the absolute deadline', () => {
    const gate = new GaussianSplatReadyGate(100, 30_000);

    expect(gate.markReady(30_100)).toBe(false);
    expect(gate.state).toBe('pending');
  });

  it('keeps failure terminal if a late frame arrives', () => {
    const gate = new GaussianSplatReadyGate(100, 30_000);

    expect(gate.fail()).toBe(true);
    expect(gate.canContinue).toBe(false);
    expect(gate.markReady(101)).toBe(false);
    expect(gate.state).toBe('failed');
    expect(gate.fail()).toBe(false);
  });

  it('allows camera renders after a successful ready transition', () => {
    const gate = new GaussianSplatReadyGate(100, 30_000);

    expect(gate.markReady(30_099)).toBe(true);
    expect(gate.canContinue).toBe(true);
  });
});
