import { describe, expect, it } from 'vitest';

import { drawOutsideDim, outsideDimRects, revealSteps, visibleWorld, type RevealPainter, type RevealStep } from './outside-dim';

const area = (rects: Array<{ width: number; height: number }>) => rects.reduce((sum, rect) => sum + rect.width * rect.height, 0);

describe('outside dim', () => {
  it('covers exactly the visible area minus the frame', () => {
    const visible = { x: 0, y: 0, width: 1000, height: 800 };
    const frame = { x: 200, y: 100, width: 300, height: 400 };
    const rects = outsideDimRects(visible, frame);
    expect(area(rects)).toBe(1000 * 800 - 300 * 400);
    for (const rect of rects) {
      const overlapX = Math.min(rect.x + rect.width, frame.x + frame.width) - Math.max(rect.x, frame.x);
      const overlapY = Math.min(rect.y + rect.height, frame.y + frame.height) - Math.max(rect.y, frame.y);
      expect(overlapX > 0 && overlapY > 0).toBe(false);
    }
  });

  it('copes with a frame partly or wholly out of view', () => {
    const visible = { x: 0, y: 0, width: 100, height: 100 };
    expect(area(outsideDimRects(visible, { x: 50, y: 50, width: 200, height: 200 }))).toBe(10000 - 2500);
    expect(area(outsideDimRects(visible, { x: 500, y: 500, width: 10, height: 10 }))).toBe(10000);
    expect(outsideDimRects(visible, { x: -10, y: -10, width: 500, height: 500 })).toEqual([]);
  });

  it('turns the viewport into the visible world rectangle', () => {
    expect(visibleWorld({ zoom: 2, panX: -100, panY: 40 }, { width: 800, height: 600 })).toEqual({ x: 50, y: -20, width: 400, height: 300 });
  });
});

describe('selected layers outside the frame show at full brightness (#91)', () => {
  const frame = { x: 0, y: 0, width: 100, height: 100 };
  const inside = { id: 'inside', rect: { x: 10, y: 10, width: 50, height: 50 } };
  const outside = { id: 'outside', rect: { x: 150, y: 20, width: 40, height: 40 } };
  const across = { id: 'across', rect: { x: 80, y: 30, width: 50, height: 20 } };
  const cover = { id: 'cover', rect: { x: 160, y: 30, width: 40, height: 40 } };
  const elsewhere = { id: 'elsewhere', rect: { x: 300, y: 300, width: 20, height: 20 } };
  const below = { id: 'below', rect: { x: 150, y: 20, width: 40, height: 40 } };

  it('reveals nothing while nothing is selected', () => {
    expect(revealSteps([inside, outside, across, cover], new Set(), frame)).toEqual([]);
  });

  it('reveals a selected layer outside the frame, or the part of one across it, but not one inside it', () => {
    expect(revealSteps([inside, outside, across], new Set(['outside']), frame)).toEqual([{ id: 'outside', reveal: true }]);
    expect(revealSteps([inside, outside, across], new Set(['across']), frame)).toEqual([{ id: 'across', reveal: true }]);
    expect(revealSteps([inside, outside, across], new Set(['inside']), frame)).toEqual([]);
    // A layer touching the frame edge from inside is still wholly inside.
    expect(revealSteps([{ id: 'edge', rect: { x: 50, y: 0, width: 50, height: 100 } }], new Set(['edge']), frame)).toEqual([]);
  });

  it('keeps unselected layers dimmed: the ones painted over a selected layer take their pixels back', () => {
    // `cover` is above `outside` and overlaps it; `elsewhere` does not overlap; `below` is painted first.
    expect(revealSteps([below, outside, elsewhere, cover], new Set(['outside']), frame)).toEqual([
      { id: 'outside', reveal: true },
      { id: 'cover', reveal: false },
    ]);
    // Selecting the top layer instead leaves nothing above it to subtract.
    expect(revealSteps([outside, cover], new Set(['cover']), frame)).toEqual([{ id: 'cover', reveal: true }]);
  });

  it('follows the paint order across several selected layers', () => {
    expect(revealSteps([outside, cover, across], new Set(['outside', 'across']), frame)).toEqual([
      { id: 'outside', reveal: true },
      { id: 'cover', reveal: false },
      { id: 'across', reveal: true },
    ]);
  });

  it('ignores empty layers', () => {
    expect(revealSteps([{ id: 'empty', rect: { x: 200, y: 200, width: 0, height: 10 } }], new Set(['empty']), frame)).toEqual([]);
  });

  /** A veil context that records each drawing call with its composite state. */
  function veilContext() {
    const log: string[] = [];
    const saved: Array<{ alpha: number; op: string; fill: unknown }> = [];
    const context = {
      fillStyle: '' as unknown,
      globalAlpha: 1,
      globalCompositeOperation: 'source-over',
      save: () => { saved.push({ alpha: context.globalAlpha, op: context.globalCompositeOperation, fill: context.fillStyle }); },
      restore: () => {
        const state = saved.pop();
        if (state) { context.globalAlpha = state.alpha; context.globalCompositeOperation = state.op; context.fillStyle = state.fill; }
      },
      setTransform: (...args: number[]) => log.push(`setTransform:${args.join(',')}`),
      fillRect: (...args: number[]) => log.push(`fillRect:${args.join(',')}:${context.globalCompositeOperation}:${context.globalAlpha}:${String(context.fillStyle)}`),
      drawImage: (source: unknown, x: number, y: number) => log.push(`drawImage:${String(source)}:${x},${y}:${context.globalCompositeOperation}:${context.globalAlpha}`),
    };
    return { context, log };
  }

  function recordingPainter(available = true) {
    const painted: RevealStep[] = [];
    let begun = 0;
    const painter: RevealPainter = {
      begin: () => { begun += 1; return available; },
      paint: (step) => { painted.push(step); },
      result: () => 'mask',
    };
    return { painter, painted, begun: () => begun };
  }

  const style = { color: '#000000', opacity: 0.45 };
  const rects = outsideDimRects({ x: -50, y: -50, width: 300, height: 200 }, frame);

  it('draws only the veil when nothing selected is outside the frame', () => {
    const { context, log } = veilContext();
    const { painter, begun } = recordingPainter();
    expect(drawOutsideDim(context, rects, style, [], painter)).toBe(false);
    expect(log).toHaveLength(rects.length);
    expect(log.every((entry) => entry.endsWith(':source-over:0.45:#000000'))).toBe(true);
    expect(begun()).toBe(0);
    expect(context.globalAlpha).toBe(1);
  });

  it('cuts the selected layer out of the veil once, in device pixels, after the veil is drawn', () => {
    const { context, log } = veilContext();
    const { painter, painted } = recordingPainter();
    const steps = [{ id: 'outside', reveal: true }, { id: 'cover', reveal: false }];
    expect(drawOutsideDim(context, rects, style, steps, painter)).toBe(true);
    expect(painted).toEqual(steps);
    expect(log.slice(0, rects.length).every((entry) => entry.startsWith('fillRect:'))).toBe(true);
    expect(log.slice(rects.length)).toEqual(['setTransform:1,0,0,1,0,0', 'drawImage:mask:0,0:destination-out:1']);
    // The context is left as it was found.
    expect(context.globalCompositeOperation).toBe('source-over');
    expect(context.globalAlpha).toBe(1);
  });

  it('keeps the plain veil when only subtracting steps remain or the scratch canvas is unavailable', () => {
    const subtractOnly = veilContext();
    expect(drawOutsideDim(subtractOnly.context, rects, style, [{ id: 'cover', reveal: false }], recordingPainter().painter)).toBe(false);
    expect(subtractOnly.log.some((entry) => entry.startsWith('drawImage'))).toBe(false);
    const unavailable = veilContext();
    expect(drawOutsideDim(unavailable.context, rects, style, [{ id: 'outside', reveal: true }], recordingPainter(false).painter)).toBe(false);
    expect(unavailable.log.some((entry) => entry.startsWith('drawImage'))).toBe(false);
    const noPainter = veilContext();
    expect(drawOutsideDim(noPainter.context, rects, style, [{ id: 'outside', reveal: true }], null)).toBe(false);
    expect(noPainter.log).toHaveLength(rects.length);
  });

  it('draws nothing when the frame fills the view', () => {
    const { context, log } = veilContext();
    expect(drawOutsideDim(context, [], style, [{ id: 'outside', reveal: true }], recordingPainter().painter)).toBe(false);
    expect(log).toEqual([]);
  });
});
