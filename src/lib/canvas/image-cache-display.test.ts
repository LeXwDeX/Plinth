import { describe, expect, it, vi } from 'vitest';

import { CanvasImageCache, MAXIMUM_DISPLAY_REDUCTION, displayReduction, type CanvasPicture } from './image-cache';

// #291: a zoomed-out board draws each layer from a reduced copy instead of the 4K original.

const bitmap = (width: number, height = width) => ({ width, height, close: () => {} } as unknown as ImageBitmap);

function setup() {
  const loads: Array<{ resolve: (picture: CanvasPicture) => void }> = [];
  const reductions: Array<{ picture: CanvasPicture; width: number; height: number; resolve: () => CanvasPicture; reject: () => void }> = [];
  const onChange = vi.fn();
  const cache = new CanvasImageCache({
    resolve: (url) => url,
    onChange,
    load: () => new Promise<CanvasPicture>((resolve) => { loads.push({ resolve }); }),
    reduce: (picture, width, height) => new Promise<CanvasPicture>((resolve, reject) => {
      reductions.push({
        picture, width, height,
        resolve: () => { const copy = bitmap(width, height); resolve(copy); return copy; },
        reject: () => reject(new Error('no copy')),
      });
    }),
  });
  const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
  return { cache, onChange, loads, reductions, settle };
}

describe('displayReduction', () => {
  it('picks the smallest halving that is still at least the shown size', () => {
    expect(displayReduction(3840, 3840)).toBe(0);
    expect(displayReduction(3840, 2000)).toBe(0);
    expect(displayReduction(3840, 1920)).toBe(1);
    expect(displayReduction(3840, 1000)).toBe(1);
    expect(displayReduction(3840, 960)).toBe(2);
    expect(displayReduction(3840, 480)).toBe(3);
    expect(displayReduction(3840, 20)).toBe(MAXIMUM_DISPLAY_REDUCTION);
  });

  it('draws the original for sizes it cannot read', () => {
    expect(displayReduction(0, 100)).toBe(0);
    expect(displayReduction(3840, 0)).toBe(0);
    expect(displayReduction(Number.NaN, 100)).toBe(0);
  });
});

describe('CanvasImageCache display copies (#291)', () => {
  it('keeps drawing the original until the reduced copy is ready, then draws the copy', async () => {
    const { cache, onChange, loads, reductions, settle } = setup();
    const url = '/api/v1/assets/a';
    cache.sync([url], [url]);
    const original = bitmap(3840, 2160);
    loads[0].resolve(original);
    await settle();
    expect(cache.display(url, 1)).toBe(original);
    expect(reductions).toHaveLength(0);
    // Shown at a quarter: a copy at 1/4 is made, the original is drawn meanwhile.
    expect(cache.display(url, 0.25)).toBe(original);
    expect(reductions).toHaveLength(1);
    expect(reductions[0]).toMatchObject({ picture: original, width: 960, height: 540 });
    expect(cache.display(url, 0.25)).toBe(original);
    expect(reductions).toHaveLength(1);
    const copy = reductions[0].resolve();
    await settle();
    expect(onChange).toHaveBeenCalledTimes(2);
    expect(cache.display(url, 0.25)).toBe(copy);
    // The original is still what painting, picking and export read.
    expect(cache.get(url)).toBe(original);
  });

  it('keeps one copy per picture: a view that needs a larger copy replaces a too-small one', async () => {
    const { cache, loads, reductions, settle } = setup();
    const url = '/api/v1/assets/a';
    cache.sync([url], [url]);
    const original = bitmap(4096);
    loads[0].resolve(original);
    await settle();
    // Shown at a quarter: a 1/4 copy is made.
    cache.display(url, 0.25);
    const quarter = reductions[0].resolve();
    await settle();
    expect(cache.display(url, 0.25)).toBe(quarter);
    // Further out: the 1/4 copy is still at least as large as shown, so it is drawn and no 1/8 copy is made.
    expect(cache.display(url, 0.1)).toBe(quarter);
    expect(reductions).toHaveLength(1);
    // Back in close: the original is drawn, and a view too large for the 1/4 copy asks for a 1/2 copy.
    expect(cache.display(url, 0.6)).toBe(original);
    expect(cache.display(url, 0.5)).toBe(original);
    expect(reductions).toHaveLength(2);
    const half = reductions[1].resolve();
    await settle();
    expect(cache.display(url, 0.5)).toBe(half);
    // The larger copy serves the smaller views too; nothing else is made.
    expect(cache.display(url, 0.25)).toBe(half);
    expect(cache.display(url, 0.1)).toBe(half);
    expect(reductions).toHaveLength(2);
  });

  it('keeps one copy that two layers of the same picture can share, without remaking it on every redraw (#313)', async () => {
    const { cache, onChange, loads, reductions, settle } = setup();
    const url = '/api/v1/assets/shared';
    cache.sync([url], [url]);
    const original = bitmap(4096);
    loads[0].resolve(original);
    await settle();
    onChange.mockClear();
    // The same picture on a 2048 px layer and a 1024 px layer at zoom 0.5: reductions 2 and 3.
    const frame = () => [cache.display(url, 0.25), cache.display(url, 0.125)];
    frame();
    expect(reductions).toHaveLength(1);
    expect(reductions[0]).toMatchObject({ width: 1024, height: 1024 });
    const copy = reductions[0].resolve();
    await settle();
    expect(onChange).toHaveBeenCalledTimes(1);
    // No user input: every later redraw asks for the same two sizes and must not start more work.
    for (let redraw = 0; redraw < 4; redraw += 1) {
      expect(frame()).toEqual([copy, copy]);
    }
    await settle();
    expect(reductions).toHaveLength(1);
    expect(onChange).toHaveBeenCalledTimes(1);
    // Painting, picking and export still read the original.
    expect(cache.get(url)).toBe(original);
  });

  it('ignores a copy that arrives after its picture was replaced or let go, and keeps the original when no copy can be made', async () => {
    const { cache, onChange, loads, reductions, settle } = setup();
    const url = '/api/v1/assets/a';
    cache.sync([url], [url]);
    const original = bitmap(3840);
    loads[0].resolve(original);
    await settle();
    cache.display(url, 0.25);
    const painted = bitmap(3840);
    cache.set(url, painted);
    reductions[0].resolve();
    await settle();
    expect(cache.display(url, 1)).toBe(painted);
    expect(onChange).toHaveBeenCalledTimes(1);
    // A failed copy: the original stays, and the same size is not asked for again.
    cache.display(url, 0.25);
    reductions[1].reject();
    await settle();
    // Repeated redraws draw the original and do not ask again.
    for (let redraw = 0; redraw < 3; redraw += 1) expect(cache.display(url, 0.25)).toBe(painted);
    expect(reductions).toHaveLength(2);
    cache.sync([], []);
    expect(cache.display(url, 0.25)).toBeUndefined();
  });

  it('draws the original where reduced copies cannot be made', async () => {
    const cache = new CanvasImageCache({ resolve: (url) => url, onChange: () => {}, load: async () => bitmap(3840) });
    const url = '/api/v1/assets/a';
    const native = globalThis.createImageBitmap;
    // No createImageBitmap (as in the node test runner, and browsers without it).
    expect(typeof native).toBe('undefined');
    cache.sync([url], [url]);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(cache.display(url, 0.25)).toBe(cache.get(url));
  });
});
