import { describe, expect, it } from 'vitest';

import { brushCursorRadius, drawMaskOverlay, maskOverlaySteps, overlayTintKind } from './mask-overlay';
import type { MaskStroke } from '@/store/mask-store';

const artboard = { x: -100, y: -50, width: 400, height: 200 };
const style = { tintColor: 'oklch(0.72 0.125 230)', tintOpacity: 0.75 };

/** A 2D context that records what it is asked to do and the state at each drawing call. */
function recordingContext(canvas: { width: number; height: number }, transform = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 }) {
  const log: string[] = [];
  const draws: Array<{ source: unknown; alpha: number; op: string; transform: number[] }> = [];
  const strokes: Array<{ alpha: number; op: string; style: unknown }> = [];
  let current = [transform.a, transform.b, transform.c, transform.d, transform.e, transform.f];
  const saved: Array<{ transform: number[]; alpha: number; op: string }> = [];
  const context = {
    canvas,
    globalAlpha: 1,
    globalCompositeOperation: 'source-over',
    fillStyle: '' as unknown,
    strokeStyle: '' as unknown,
    lineWidth: 1,
    lineCap: 'butt',
    lineJoin: 'miter',
    getTransform: () => ({ a: current[0], b: current[1], c: current[2], d: current[3], e: current[4], f: current[5] }),
    setTransform: (a: number, b: number, c: number, d: number, e: number, f: number) => { current = [a, b, c, d, e, f]; log.push(`setTransform:${[a, b, c, d, e, f].join(',')}`); },
    translate: (x: number, y: number) => { current = [current[0], current[1], current[2], current[3], current[4] + current[0] * x + current[2] * y, current[5] + current[1] * x + current[3] * y]; log.push(`translate:${x},${y}`); },
    save: () => { saved.push({ transform: [...current], alpha: context.globalAlpha, op: context.globalCompositeOperation }); log.push('save'); },
    restore: () => {
      const state = saved.pop();
      if (state) { current = state.transform; context.globalAlpha = state.alpha; context.globalCompositeOperation = state.op; }
      log.push('restore');
    },
    clearRect: (...args: number[]) => log.push(`clear:${args.join(',')}`),
    fillRect: (...args: number[]) => log.push(`fillRect:${args.join(',')}:${context.globalCompositeOperation}:${String(context.fillStyle)}`),
    beginPath: () => undefined,
    moveTo: () => undefined,
    lineTo: () => undefined,
    closePath: () => undefined,
    fill: () => log.push(`fill:${context.globalCompositeOperation}`),
    stroke: () => strokes.push({ alpha: context.globalAlpha, op: context.globalCompositeOperation, style: context.strokeStyle }),
    drawImage: (source: unknown) => draws.push({ source, alpha: context.globalAlpha, op: context.globalCompositeOperation, transform: [...current] }),
  };
  return { context, log, draws, strokes };
}

function scratchCanvas() {
  const canvas = { width: 0, height: 0, getContext: () => recorder.context };
  const recorder = recordingContext(canvas);
  return { canvas, recorder };
}

describe('mask overlay (#310: one layer, constant strength)', () => {
  const strokeA: MaskStroke = { points: [10, 10, 200, 10], size: 20 };
  const strokeB: MaskStroke = { points: [50, 0, 50, 100], size: 20 };

  it('lists the stroke being drawn after the finished ones, so it joins the same layer', () => {
    expect(maskOverlaySteps([strokeA], strokeB)).toEqual([strokeA, strokeB]);
    expect(maskOverlaySteps([strokeA], null)).toEqual([strokeA]);
    expect(maskOverlaySteps(undefined, strokeB)).toEqual([strokeB]);
    expect(maskOverlaySteps(undefined, null)).toEqual([]);
  });

  it('paints every stroke at full strength off screen and lays the tint over the board exactly once', () => {
    const target = recordingContext({ width: 2732, height: 1536 }, { a: 2.5, b: 0, c: 0, d: 2.5, e: 40, f: 30 });
    const scratch = scratchCanvas();
    // Overlapping strokes, the same stroke twice and a stroke crossing itself.
    const steps = [strokeA, strokeB, strokeA, { points: [10, 50, 300, 50, 20, 50, 290, 50], size: 30 }];
    expect(drawMaskOverlay(target.context, scratch.canvas, artboard, steps, style)).toBe(true);

    // The scratch matches the board canvas in device pixels and uses its transform, shifted to the artboard.
    expect(scratch.canvas).toMatchObject({ width: 2732, height: 1536 });
    expect(scratch.recorder.log).toContain('setTransform:2.5,0,0,2.5,40,30');
    expect(scratch.recorder.log).toContain('translate:-100,-50');
    // Every stroke reaches the scratch at full strength; the tint opacity is never applied per stroke.
    expect(scratch.recorder.strokes).toHaveLength(4);
    for (const stroke of scratch.recorder.strokes) expect(stroke.alpha).toBe(1);
    // The editable area becomes the tint colour at full strength (the same paintMask as the submitted mask).
    expect(scratch.recorder.log.some((entry) => entry.startsWith('fillRect:0,0,400,200:source-out:oklch(0.72 0.125 230)'))).toBe(true);

    // One composite, in device pixels, at the Schema mask tint opacity: overlaps cannot darken.
    expect(target.draws).toHaveLength(1);
    expect(target.draws[0]).toMatchObject({ source: scratch.canvas, alpha: 0.75, op: 'source-over', transform: [1, 0, 0, 1, 0, 0] });
    // The board context is left as it was found.
    expect(target.context.globalAlpha).toBe(1);
    expect(target.context.getTransform()).toEqual({ a: 2.5, b: 0, c: 0, d: 2.5, e: 40, f: 30 });
  });

  it('keeps the opacity a parent already applied', () => {
    const target = recordingContext({ width: 100, height: 100 });
    target.context.globalAlpha = 0.5;
    drawMaskOverlay(target.context, scratchCanvas().canvas, artboard, [strokeA], style);
    expect(target.draws[0].alpha).toBeCloseTo(0.375);
  });

  it('draws nothing when there is nothing selected', () => {
    const target = recordingContext({ width: 100, height: 100 });
    expect(drawMaskOverlay(target.context, scratchCanvas().canvas, artboard, [], style)).toBe(false);
    expect(target.draws).toHaveLength(0);
  });
});

describe('overlay tint kind', () => {
  const region: MaskStroke = { points: [0, 0, 10, 0, 10, 10, 0, 10], size: 0, region: true };
  const invert: MaskStroke = { points: [], size: 0, invert: true };

  it('keeps the selection tint for a pure marquee / lasso / inversion selection', () => {
    expect(overlayTintKind([region])).toBe('selection');
    expect(overlayTintKind([region, { ...region, erase: true }, invert])).toBe('selection');
  });

  it('uses the mask tint as soon as the brush painted or erased, mixed steps included', () => {
    expect(overlayTintKind([{ points: [1, 1, 5, 5], size: 4 }])).toBe('mask');
    expect(overlayTintKind([region, { points: [1, 1], size: 4, erase: true }, invert])).toBe('mask');
  });
});

describe('brush cursor radius', () => {
  it('is half the brush size in canvas units, so its screen diameter is the stroke width at any zoom', () => {
    expect(brushCursorRadius(20, 1)).toBe(10);
    expect(brushCursorRadius(20, 3)).toBe(10);
    expect(brushCursorRadius(1, 0.25)).toBe(4);
    expect(brushCursorRadius(1, 4)).toBe(0.5);
  });
});
