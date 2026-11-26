import { describe, expect, it, vi } from 'vitest';

import { createCoalescer } from './coalesce';

describe('reads asked for by a replayed stream (#665)', () => {
  it('folds the asks made while a read is out into one more read', async () => {
    const coalesce = createCoalescer();
    let finish!: () => void;
    const read = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    for (let index = 0; index < 20; index += 1) coalesce('s1', read);
    expect(read).toHaveBeenCalledTimes(1);
    finish();
    await vi.waitFor(() => expect(read).toHaveBeenCalledTimes(2));
    finish();
    await Promise.resolve();
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('keeps keys apart and survives a failed read', async () => {
    const coalesce = createCoalescer();
    const failing = vi.fn(async () => { throw new Error('offline'); });
    coalesce('a', failing);
    coalesce('b', failing);
    expect(failing).toHaveBeenCalledTimes(2);
    await new Promise((resolve) => setTimeout(resolve, 0));
    coalesce('a', failing);
    expect(failing).toHaveBeenCalledTimes(3);
  });
});
