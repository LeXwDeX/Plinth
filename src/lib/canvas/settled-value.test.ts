import { describe, expect, it } from 'vitest';
import { createSettledValue } from './settled-value';

describe('createSettledValue (#246 capture initial size)', () => {
  it('resolves at once when settled, and otherwise with the value it settles on', async () => {
    const settled = createSettledValue({ width: 1536, height: 1024 }, true);
    await expect(settled.read()).resolves.toEqual({ width: 1536, height: 1024 });

    const gate = createSettledValue({ width: 2048, height: 2048 });
    let seen: unknown = null;
    const reading = gate.read().then((value) => { seen = value; });
    // The Schema default while the model's settings load is never handed out.
    gate.update({ width: 2048, height: 2048 }, false);
    await Promise.resolve();
    expect(seen).toBeNull();
    gate.update({ width: 4096, height: 4096 }, true);
    await reading;
    expect(seen).toEqual({ width: 4096, height: 4096 });
    // Later reads see later settled values.
    gate.update({ width: 1024, height: 1024 }, true);
    await expect(gate.read()).resolves.toEqual({ width: 1024, height: 1024 });
  });
});
