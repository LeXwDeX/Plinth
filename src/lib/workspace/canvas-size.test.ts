import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import sharedTurnSizes from '../../../internal/chat/generationtool/testdata/canvas_turn_sizes.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ParamSchema } from '@/lib/providers/types';
import { effectiveOfferingValues } from './generation-params';
import {
  canvasPixelsForRatio, canvasRatioChoice, canvasRatioOptions, canvasRatioTurnOutput, canvasRatioValues, canvasShapeOf, canvasSizeChoice, canvasSizeParameters,
  canvasTierParameters, canvasTurnOutput, imageSizeAllows, initialCanvasPixels, outputPixels, referenceTurnOutput, type RatioTables,
} from './canvas-size';

const artboard = catalog.application.canvas.frontend.artboard;
const ratioPixels = artboard as unknown as RatioTables;

const gptSizes = ['1024x1024', '1536x1024', '2048x2048', '3840x2160'];
const gptParam = { name: 'size', label: 'size', type: 'select', default: '2048x2048', options: gptSizes.map((value) => ({ value, label: value })) } as unknown as ParamSchema;
const gpt = {
  id: 'gpt', available: true,
  parameters: [{ name: 'size', semantic: 'output-size', default: '2048x2048', options: gptSizes.map((value) => ({ value, label: value })) }],
} as unknown as OfferingWire;

const seedreamSizes = ['2048x2048', '4096x4096', '5504x3040'];
const seedreamParam = { name: 'size', label: 'size', type: 'select', default: '4096x4096', options: seedreamSizes.map((value) => ({ value, label: value })) } as unknown as ParamSchema;
const seedream = {
  id: 'seedream', available: true,
  parameters: [{
    name: 'size', semantic: 'output-size', default: '4096x4096', options: seedreamSizes.map((value) => ({ value, label: value })),
    // Seedream turns its 2K choice into the 4K size (the catalog's forced_when).
    forced_when: [{ when: { parameter: 'size', operator: 'equals', value: '2048x2048' }, value: '4096x4096' }],
  }],
} as unknown as OfferingWire;

const ratios = ['1:1', '16:9', '9:16'];
const bananaParam = { name: 'aspect_ratio', label: 'ratio', type: 'select', default: '1:1', options: ratios.map((value) => ({ value, label: value })) } as unknown as ParamSchema;
const banana = {
  id: 'banana', available: true,
  parameters: [{ name: 'aspect_ratio', semantic: 'output-size', default: '1:1', options: ratios.map((value) => ({ value, label: value })) }],
} as unknown as OfferingWire;

describe('the canvas is a ratio (#653)', () => {
  it('reads pixel sizes directly and ratio-only sizes through the Schema ratio table', () => {
    expect(outputPixels([gptParam], { size: '3840x2160' }, ratioPixels)).toEqual({ width: 3840, height: 2160 });
    expect(outputPixels([bananaParam], { aspect_ratio: '16:9' }, ratioPixels)).toEqual({ width: 1344, height: 768 });
    expect(outputPixels([seedreamParam], {}, ratioPixels)).toEqual({ width: 4096, height: 4096 });
    expect(outputPixels([], {}, ratioPixels)).toBeNull();
  });

  it('starts a new canvas at the Schema default, 1:1, whatever the model', () => {
    const initial = initialCanvasPixels(artboard);
    expect(initial).toEqual({ width: artboard.default_width, height: artboard.default_height });
    expect(canvasShapeOf(initial, ratioPixels).label).toBe('1:1');
  });

  it('reads the ratio of an existing canvas from its pixels, without migrating it', () => {
    expect(canvasShapeOf({ width: 2048, height: 1152 }, ratioPixels)).toEqual({ label: '16:9', ratio: 16 / 9 });
    expect(canvasShapeOf({ width: 2048, height: 878 }, ratioPixels).label).toBe('21:9');
    // The pixels the Schema maps a ratio to name that ratio (canvases made for a ratio-only model before #653).
    expect(canvasShapeOf({ width: 1344, height: 768 }, ratioPixels).label).toBe('16:9');
    expect(canvasShapeOf({ width: 3072, height: 5504 }, ratioPixels).label).toBe('9:16');
    // Any other shape keeps its own ratio, named simply.
    expect(canvasShapeOf({ width: 1000, height: 700 }, ratioPixels)).toEqual({ label: '10:7', ratio: 1000 / 700 });
    expect(canvasShapeOf({ width: 2048, height: 683 }, ratioPixels).label).toBe('3:1');
  });

  it('offers the Schema ratios from the tallest to the widest and keeps the long edge when one is picked', () => {
    expect(canvasRatioOptions(ratioPixels)).toEqual(['9:16', '2:3', '3:4', '4:5', '1:1', '5:4', '4:3', '3:2', '16:9', '21:9']);
    const limits = { minimum_dimension: 64, maximum_dimension: 8192 };
    expect(canvasPixelsForRatio('16:9', artboard, limits)).toEqual({ width: 2048, height: 1152 });
    expect(canvasPixelsForRatio('9:16', artboard, limits)).toEqual({ width: 1152, height: 2048 });
    // Any canvas takes the default long edge: a large legacy canvas can be made smaller, a small one grows.
    expect(canvasPixelsForRatio('4:3', artboard, limits)).toEqual({ width: 2048, height: 1536 });
    expect(canvasPixelsForRatio('1:1', artboard, limits)).toEqual({ width: 2048, height: 2048 });
    for (const ratio of canvasRatioOptions(ratioPixels)) {
      expect(canvasShapeOf(canvasPixelsForRatio(ratio, artboard, limits)!, ratioPixels).label).toBe(ratio);
    }
    expect(canvasPixelsForRatio('auto', artboard, limits)).toBeNull();
  });

  it('takes the most pixels of the canvas ratio, else of the nearest ratio', () => {
    const constraint = { min_pixels: 655360, max_pixels: 8294400, max_edge: 3840, multiple: 16, max_ratio: 3 };
    const options = gptSizes.map((value) => ({ value, label: value }));
    // Options alone: the largest of the ratio; custom sizes: the largest within the bounds.
    expect(canvasRatioChoice({ options }, canvasShapeOf({ width: 2048, height: 2048 }, ratioPixels))).toBe('2048x2048');
    expect(canvasRatioChoice({ options, image_size: constraint }, canvasShapeOf({ width: 2048, height: 2048 }, ratioPixels))).toBe('2880x2880');
    // No option of the ratio: the nearest ratio, at its most pixels.
    expect(canvasRatioChoice({ options }, canvasShapeOf({ width: 2048, height: 1536 }, ratioPixels))).toBe('1536x1024');
    expect(canvasRatioChoice({ options: [{ value: 'auto', label: '' }] }, { label: '1:1', ratio: 1 })).toBeNull();
  });

  it('switching models never changes the canvas: only the output size is computed again', () => {
    const canvas = Object.freeze({ width: 2048, height: 1152 });
    const gptImage = schemaOffering('openai/gpt-image-2/image-to-image');
    const gemini = schemaOffering('gemini/gemini-3-pro-image/image-to-image');
    expect(canvasRatioTurnOutput(gptImage, effectiveOfferingValues(gptImage, {}), canvas, ratioPixels))
      .toEqual({ value: '3840x2160', pixels: { width: 3840, height: 2160 }, ratioOnly: false, nearest: false, ratio: '16:9', outputRatio: '16:9' });
    expect(canvasRatioTurnOutput(gemini, effectiveOfferingValues(gemini, {}), canvas, ratioPixels))
      .toEqual({ value: '16:9', pixels: { width: 5504, height: 3072 }, ratioOnly: true, nearest: false, ratio: '16:9', outputRatio: '16:9' });
    expect(canvas).toEqual({ width: 2048, height: 1152 });
  });

  it('names the nearest ratio when the model has none of the canvas ratio', () => {
    const banana = schemaOffering('meshy/nano-banana-pro/image-to-image');
    expect(canvasRatioTurnOutput(banana, effectiveOfferingValues(banana, {}), { width: 2048, height: 878 }, ratioPixels))
      .toMatchObject({ value: '16:9', nearest: true, ratio: '21:9', outputRatio: '16:9' });
    const gptImage = schemaOffering('openai/gpt-image-2/image-to-image');
    expect(canvasRatioTurnOutput(gptImage, effectiveOfferingValues(gptImage, {}), { width: 8192, height: 2048 }, ratioPixels))
      .toMatchObject({ value: '3840x1280', nearest: true, ratio: '4:1', outputRatio: '3:1' });
  });

  it('sets the highest tier only when a person may choose the tier', () => {
    const gemini = schemaOffering('gemini/gemini-3-pro-image/image-to-image');
    // Gemini's image_size is read-only (its 4K default is the Offering's own).
    expect(canvasTierParameters(gemini, ratioPixels)).toEqual([]);
    expect(canvasRatioTurnOutput(gemini, effectiveOfferingValues(gemini, { image_size: '1K' }), { width: 2048, height: 2048 }, ratioPixels))
      .toMatchObject({ value: '1:1', pixels: { width: 1024, height: 1024 } });
  });
});

/** An Offering of the default Schema as GET /api/v1/offerings sends it (the parameters are serialised as declared). */
function schemaOffering(id: string): OfferingWire {
  const offering = (catalog.offerings as unknown as Array<{ id: string; parameters: unknown[] }>).find((item) => item.id === id);
  if (!offering) throw new Error(`no offering ${id}`);
  return { ...offering, available: true } as unknown as OfferingWire;
}

describe('the output size a chat edit of one picture sends (#296)', () => {
  // The same cases run against the server rule (reference_size.go, TestCanvasTurnSizesSharedWithTheWebClient).
  for (const test of sharedTurnSizes.cases.filter((item) => 'source' in item && item.source === 'reference')) {
    it(`${test.name} (shared with the server)`, () => {
      const offering = schemaOffering(test.offering);
      // Nobody chose a size: the panel shows the Offering's defaults.
      const defaults = effectiveOfferingValues(offering, {});
      const turn = referenceTurnOutput(offering, defaults, {}, { width: test.width, height: test.height }, ratioPixels);
      expect(turn).toMatchObject({ value: test.value, nearest: test.nearest });
    });
  }

  it('says nothing once the person or the Agent chose a size, and marks a size the defaults already give', () => {
    const gptImage = schemaOffering('openai/gpt-image-2/image-to-image');
    const defaults = effectiveOfferingValues(gptImage, {});
    const upload = { width: 800, height: 600 };
    expect(referenceTurnOutput(gptImage, defaults, { size: { source: 'user' } }, upload, ratioPixels)).toBeNull();
    expect(referenceTurnOutput(gptImage, defaults, { size: { source: 'agent' } }, upload, ratioPixels)).toBeNull();
    // Restored to the default: the reference sets the size again.
    expect(referenceTurnOutput(gptImage, defaults, { size: { source: 'default' } }, upload, ratioPixels))
      .toEqual({ value: '1536x1152', pixels: { width: 1536, height: 1152 }, ratioOnly: false, nearest: false, changed: true });
    // A square picture gets the default square: nothing changes.
    expect(referenceTurnOutput(gptImage, defaults, {}, { width: 2048, height: 2048 }, ratioPixels)).toMatchObject({ value: '2048x2048', changed: false });
    expect(referenceTurnOutput(undefined, {}, {}, upload, ratioPixels)).toBeNull();
  });
});

describe('the output size a canvas-sourced turn sends (#272/#653)', () => {
  const fixtureOfferings = (sharedTurnSizes as unknown as { offerings: Record<string, OfferingWire> }).offerings;
  // The same cases run against the server rule (internal/chat/generationtool, TestCanvasTurnSizesSharedWithTheWebClient).
  for (const test of sharedTurnSizes.cases.filter((item) => !('source' in item) || item.source !== 'reference')) {
    it(`${test.name} (shared with the server)`, () => {
      const fixture = fixtureOfferings[test.offering];
      const offering = fixture ? ({ ...fixture, available: true } as OfferingWire) : schemaOffering(test.offering);
      // A size remembered for the session that differs from the canvas (the #284 scene: 1536x1152 on 2048x2048).
      const stale = effectiveOfferingValues(offering, { size: '1536x1152', aspect_ratio: '1:1' });
      const canvas = { width: test.width, height: test.height };
      const turn = canvasRatioTurnOutput(offering, stale, canvas, ratioPixels);
      expect(turn).toMatchObject({ value: test.value, nearest: test.nearest, ratio: test.ratio });
      const extra = test as { pixels?: string; also?: Record<string, string> };
      if (extra.pixels) expect(turn?.pixels && `${turn.pixels.width}x${turn.pixels.height}`).toBe(extra.pixels);
      if (extra.also) expect(canvasRatioValues(offering, canvas, ratioPixels)).toMatchObject(extra.also);
      // The canvas is not changed by the model.
      expect(canvas).toEqual({ width: test.width, height: test.height });
    });
  }

  it('says nothing once the Agent chose the size: the server carries out the Agent\'s size (#614)', () => {
    const gptImage = schemaOffering('openai/gpt-image-2/image-to-image');
    const agentSize = effectiveOfferingValues(gptImage, { size: '3840x2160' });
    const square = { width: 1024, height: 1024 };
    expect(canvasRatioTurnOutput(gptImage, agentSize, square, ratioPixels, { size: { source: 'agent' } })).toBeNull();
    // The person's panel choice stays under the canvas: the canvas still sets the size.
    expect(canvasRatioTurnOutput(gptImage, agentSize, square, ratioPixels, { size: { source: 'user' } }))
      .toMatchObject({ value: '2880x2880', nearest: false });
    expect(canvasRatioTurnOutput(gptImage, agentSize, square, ratioPixels, { size: { source: 'default' } }))
      .toMatchObject({ value: '2880x2880', nearest: false });
  });

  it('sets only the size a person may choose, and nothing without an Offering or a size setting', () => {
    const gemini = schemaOffering('gemini/gemini-3-pro-image/image-to-image');
    // Gemini's image_size is read-only (the 4K tier is the Offering's own), so only aspect_ratio follows the canvas.
    expect(canvasSizeParameters(gemini).map((wire) => wire.name)).toEqual(['aspect_ratio']);
    expect(canvasRatioTurnOutput(undefined, {}, { width: 2048, height: 2048 }, ratioPixels)).toBeNull();
    expect(canvasRatioTurnOutput({ id: 'x', parameters: [{ name: 'prompt', semantic: 'prompt' }] } as unknown as OfferingWire, {}, { width: 2048, height: 2048 }, ratioPixels)).toBeNull();
    const fixed = { id: 'x', parameters: [{ name: 'size', semantic: 'output-size', fixed: true, default: '1024x1024', options: [{ value: '1024x1024', label: '' }] }] } as unknown as OfferingWire;
    expect(canvasRatioTurnOutput(fixed, { size: '1024x1024' }, { width: 2048, height: 2048 }, ratioPixels)).toBeNull();
  });
});

describe('the legal size closest to a picture (#284/#308: one reference picture and inpainting)', () => {
  it('reads the pixels it sends: the value itself, or a ratio through the tier the Offering is set to', () => {
    const gptImage = schemaOffering('openai/gpt-image-2/image-to-image');
    expect(canvasTurnOutput(gptImage, effectiveOfferingValues(gptImage, { size: '1536x1152' }), { width: 4096, height: 4096 }, ratioPixels))
      .toEqual({ value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: true });
    const gemini = schemaOffering('gemini/gemini-3-pro-image/image-to-image');
    const turn = canvasTurnOutput(gemini, effectiveOfferingValues(gemini, {}), { width: 2048, height: 1536 }, ratioPixels);
    const tiers = (artboard as unknown as RatioTables).ratio_pixel_tiers ?? {};
    expect(turn).toEqual({ value: '4:3', pixels: tiers.image_size?.['4K']?.['4:3'], ratioOnly: true, nearest: false });
  });

  it('chooses the picture size, its grid size or the nearest option like the server', () => {
    const constraint = { min_pixels: 655360, max_pixels: 8294400, max_edge: 3840, multiple: 16, max_ratio: 3 };
    expect(imageSizeAllows(constraint, 1920, 1088)).toBe(true);
    expect(imageSizeAllows(constraint, 1000, 704)).toBe(false); // off the 16 grid
    expect(imageSizeAllows(constraint, 4096, 2048)).toBe(false); // above the maximum edge
    expect(imageSizeAllows(constraint, 3840, 1024)).toBe(false); // longer than 3:1
    const options = ['1024x1024', '2048x2048', '3840x2160'].map((value) => ({ value, label: value }));
    expect(canvasSizeChoice({ options, image_size: constraint }, { width: 1920, height: 1088 })).toBe('1920x1088');
    expect(canvasSizeChoice({ options }, { width: 1920, height: 1088 })).toBe('3840x2160');
    expect(canvasSizeChoice({ options }, { width: 4096, height: 4096 })).toBe('2048x2048');
    expect(canvasSizeChoice({ options: [{ value: 'auto', label: '' }] }, { width: 4096, height: 4096 })).toBeNull();
  });
});
