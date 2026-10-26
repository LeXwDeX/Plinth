import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import sharedTurnSizes from '../../../internal/chat/generationtool/testdata/canvas_turn_sizes.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ParamSchema } from '@/lib/providers/types';
import { effectiveOfferingValues } from './generation-params';
import {
  canvasResizeForChoice, canvasSizeChoice, canvasSizeFit, canvasSizeParameters, canvasTurnOutput, imageSizeAllows, initialCanvasPixels,
  outputPixels, pixelsForChoice, referenceTurnOutput, settingsAfterModelSwitch, type RatioTables,
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

describe('canvas size and the 2D output size (#246)', () => {
  it('reads pixel sizes directly and ratio-only sizes through the Schema ratio table', () => {
    expect(outputPixels([gptParam], { size: '3840x2160' }, ratioPixels)).toEqual({ width: 3840, height: 2160 });
    expect(outputPixels([bananaParam], { aspect_ratio: '16:9' }, ratioPixels)).toEqual({ width: 1344, height: 768 });
    expect(outputPixels([], {}, ratioPixels)).toBeNull();
  });

  it('starts a canvas at the model output, else at the Schema default', () => {
    expect(initialCanvasPixels(artboard, [gptParam], { size: '1536x1024' })).toEqual({ width: 1536, height: 1024 });
    expect(initialCanvasPixels(artboard, [], {})).toEqual({ width: artboard.default_width, height: artboard.default_height });
  });

  it('applies forced values when reading what a choice produces', () => {
    expect(pixelsForChoice(seedream, [seedreamParam], {}, 'size', '2048x2048', ratioPixels)).toEqual({ width: 4096, height: 4096 });
    expect(pixelsForChoice(gpt, [gptParam], {}, 'size', null, ratioPixels)).toEqual({ width: 2048, height: 2048 });
  });

  it('says match, align or conflict, never changing the canvas', () => {
    expect(canvasSizeFit(undefined, [gptParam], {}, { width: 2048, height: 2048 }, ratioPixels)).toEqual({ kind: 'none' });
    expect(canvasSizeFit(gpt, [gptParam], { size: '2048x2048' }, { width: 2048, height: 2048 }, ratioPixels)).toEqual({ kind: 'match' });
    // Same model, another option produces the canvas size.
    expect(canvasSizeFit(gpt, [gptParam], { size: '1024x1024' }, { width: 3840, height: 2160 }, ratioPixels))
      .toEqual({ kind: 'align', settings: { size: '3840x2160' }, output: { width: 1024, height: 1024 } });
    // Seedream cannot produce 2048x2048 (its 2K choice is forced to 4K).
    expect(canvasSizeFit(seedream, [seedreamParam], { size: '4096x4096' }, { width: 2048, height: 2048 }, ratioPixels))
      .toEqual({ kind: 'conflict', output: { width: 4096, height: 4096 } });
    // The named option wins over one forced to the same size.
    expect(canvasSizeFit(seedream, [seedreamParam], { size: '5504x3040' }, { width: 4096, height: 4096 }, ratioPixels))
      .toMatchObject({ kind: 'align', settings: { size: '4096x4096' } });
    // A ratio-only model aligns through the ratio table and conflicts with any other size.
    expect(canvasSizeFit(banana, [bananaParam], { aspect_ratio: '1:1' }, { width: 1344, height: 768 }, ratioPixels))
      .toMatchObject({ kind: 'align', settings: { aspect_ratio: '16:9' } });
    expect(canvasSizeFit(banana, [bananaParam], { aspect_ratio: '1:1' }, { width: 2048, height: 2048 }, ratioPixels))
      .toEqual({ kind: 'conflict', output: { width: 1024, height: 1024 } });
  });

  it('reads a ratio through the tier the Offering is set to (Gemini image_size, official per-tier sizes)', () => {
    const geminiRatio = { ...bananaParam, options: [...(bananaParam.options ?? []), { value: '21:9', label: '21:9' }] } as ParamSchema;
    const gemini = {
      id: 'gemini', available: true,
      parameters: [
        { name: 'aspect_ratio', semantic: 'output-size', default: '1:1', options: geminiRatio.options },
        { name: 'image_size', default: '4K', options: [{ value: '1K', label: '1K' }, { value: '2K', label: '2K' }, { value: '4K', label: '4K' }], presentation: { surface: 'readonly' } },
      ],
    } as unknown as OfferingWire;
    expect(outputPixels([geminiRatio], { aspect_ratio: '1:1', image_size: '4K' }, ratioPixels)).toEqual({ width: 4096, height: 4096 });
    expect(outputPixels([geminiRatio], { aspect_ratio: '16:9', image_size: '2K' }, ratioPixels)).toEqual({ width: 2752, height: 1536 });
    expect(outputPixels([geminiRatio], { aspect_ratio: '21:9', image_size: '1K' }, ratioPixels)).toEqual({ width: 1584, height: 672 });
    // The default tier (4K) applies without an entered value: a new canvas for Gemini is 4096x4096, not 1024.
    expect(pixelsForChoice(gemini, [geminiRatio], {}, 'aspect_ratio', '1:1', ratioPixels)).toEqual({ width: 4096, height: 4096 });
    // A 1024 canvas is a conflict at 4K and a match at 1K.
    expect(canvasSizeFit(gemini, [geminiRatio], { aspect_ratio: '1:1', image_size: '4K' }, { width: 1024, height: 1024 }, ratioPixels))
      .toEqual({ kind: 'conflict', output: { width: 4096, height: 4096 } });
    expect(canvasSizeFit(gemini, [geminiRatio], { aspect_ratio: '1:1', image_size: '1K' }, { width: 1024, height: 1024 }, ratioPixels))
      .toEqual({ kind: 'match' });
    // Without a tier parameter the plain ratio table applies (Nano Banana).
    expect(outputPixels([bananaParam], { aspect_ratio: '1:1' }, ratioPixels)).toEqual({ width: 1024, height: 1024 });
  });

  it('turns a size chosen in the panel into the canvas size only when the canvas has one', () => {
    const limits = { minimum_dimension: 64, maximum_dimension: 8192 };
    expect(canvasResizeForChoice(true, gpt, [gptParam], { size: '2048x2048' }, 'size', '3840x2160', ratioPixels, limits)).toEqual({ width: 3840, height: 2160 });
    // Resetting to the default resizes to the default's pixels.
    expect(canvasResizeForChoice(true, gpt, [gptParam], { size: '1024x1024' }, 'size', null, ratioPixels, limits)).toEqual({ width: 2048, height: 2048 });
    expect(canvasResizeForChoice(true, seedream, [seedreamParam], {}, 'size', '2048x2048', ratioPixels, limits)).toEqual({ width: 4096, height: 4096 });
    expect(canvasResizeForChoice(false, gpt, [gptParam], {}, 'size', '1024x1024', ratioPixels, limits)).toBeNull();
  });

  it('aligns the model setting only after a real model switch in the same session', () => {
    const align = { kind: 'align' as const, settings: { size: '1024x1024' }, output: null };
    expect(settingsAfterModelSwitch({ sessionId: 's', offering: 'banana' }, { sessionId: 's', offering: 'gpt' }, align)).toEqual({ size: '1024x1024' });
    // Opening the board, coming back to it in another session, or the same model: never written.
    expect(settingsAfterModelSwitch({ sessionId: null, offering: '' }, { sessionId: 's', offering: 'gpt' }, align)).toBeNull();
    expect(settingsAfterModelSwitch({ sessionId: 'other', offering: 'banana' }, { sessionId: 's', offering: 'gpt' }, align)).toBeNull();
    expect(settingsAfterModelSwitch({ sessionId: 's', offering: 'gpt' }, { sessionId: 's', offering: 'gpt' }, align)).toBeNull();
    // A model that cannot produce the canvas size is left as it is (the notice says so).
    expect(settingsAfterModelSwitch({ sessionId: 's', offering: 'gpt' }, { sessionId: 's', offering: 'seedream' }, { kind: 'conflict', output: null })).toBeNull();
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

describe('the output size a canvas-sourced turn sends (#272/#284)', () => {
  // The same cases run against the server rule (internal/chat/generationtool, TestCanvasTurnSizesSharedWithTheWebClient).
  for (const test of sharedTurnSizes.cases.filter((item) => !('source' in item) || item.source !== 'reference')) {
    it(`${test.name} (shared with the server)`, () => {
      const offering = schemaOffering(test.offering);
      // A size remembered for the session that differs from the canvas (the #284 scene: 1536x1152 on 2048x2048).
      const stale = effectiveOfferingValues(offering, { size: '1536x1152', aspect_ratio: '1:1' });
      const turn = canvasTurnOutput(offering, stale, { width: test.width, height: test.height }, ratioPixels);
      expect(turn).toMatchObject({ value: test.value, nearest: test.nearest });
    });
  }

  it('reads the pixels it sends: the value itself, or a ratio through the tier the Offering is set to', () => {
    const gptImage = schemaOffering('openai/gpt-image-2/image-to-image');
    expect(canvasTurnOutput(gptImage, effectiveOfferingValues(gptImage, { size: '1536x1152' }), { width: 4096, height: 4096 }, ratioPixels))
      .toEqual({ value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: true });
    const gemini = schemaOffering('gemini/gemini-3-pro-image/image-to-image');
    const turn = canvasTurnOutput(gemini, effectiveOfferingValues(gemini, {}), { width: 2048, height: 1536 }, ratioPixels);
    const tiers = (artboard as unknown as RatioTables).ratio_pixel_tiers ?? {};
    expect(turn).toEqual({ value: '4:3', pixels: tiers.image_size?.['4K']?.['4:3'], ratioOnly: true, nearest: false });
  });

  it('sets only the size a person may choose, and nothing without an Offering or a size setting', () => {
    const gemini = schemaOffering('gemini/gemini-3-pro-image/image-to-image');
    // Gemini's image_size is read-only (the 4K tier is the Offering's own), so only aspect_ratio follows the canvas.
    expect(canvasSizeParameters(gemini).map((wire) => wire.name)).toEqual(['aspect_ratio']);
    expect(canvasTurnOutput(undefined, {}, { width: 2048, height: 2048 }, ratioPixels)).toBeNull();
    expect(canvasTurnOutput({ id: 'x', parameters: [{ name: 'prompt', semantic: 'prompt' }] } as unknown as OfferingWire, {}, { width: 2048, height: 2048 }, ratioPixels)).toBeNull();
    const fixed = { id: 'x', parameters: [{ name: 'size', semantic: 'output-size', fixed: true, default: '1024x1024', options: [{ value: '1024x1024', label: '' }] }] } as unknown as OfferingWire;
    expect(canvasTurnOutput(fixed, { size: '1024x1024' }, { width: 2048, height: 2048 }, ratioPixels)).toBeNull();
  });

  it('chooses the canvas size, its grid size or the nearest option like the server', () => {
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
