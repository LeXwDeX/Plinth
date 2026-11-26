import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
import { CanvasSizeNotice, ReferenceSizeNotice } from './canvas-size-notice';

const messages = catalog.application.content.messages as Record<string, string>;

type Plan = NonNullable<CanvasDelivery['plan']>;
function delivery(plan: Plan | null, source = false, known = true, pixels = { width: 2048, height: 2048 }, ratio = '1:1'): CanvasDelivery {
  return {
    artboard: { x: 600 - pixels.width / 2, y: 400 - pixels.height / 2, ...pixels }, pixels, ratio: { label: ratio, ratio: pixels.width / pixels.height },
    known, initial: { width: 2048, height: 2048 }, plan, turn: source ? plan : null, source, settled: true,
    generation: { setValue: vi.fn() } as unknown as CanvasDelivery['generation'],
  };
}
const render = (value: CanvasDelivery) => renderToStaticMarkup(<CanvasSizeNotice sessionId="s" delivery={value} />);
const square: Plan = { value: '2880x2880', pixels: { width: 2880, height: 2880 }, ratioOnly: false, nearest: false, ratio: '1:1', outputRatio: '1:1' };
const wide = { width: 2048, height: 878 };
const bananaWide: Plan = { value: '16:9', pixels: { width: 1344, height: 768 }, ratioOnly: true, nearest: true, ratio: '21:9', outputRatio: '16:9' };

describe('CanvasSizeNotice (#653)', () => {
  it('says nothing without a size the canvas sets, before the canvas size is known, or off a canvas turn when the model has the ratio', () => {
    expect(render(delivery(null, true))).toBe('');
    expect(render(delivery(square, true, false))).toBe('');
    expect(render(delivery(square, false))).toBe('');
  });

  it('on a canvas turn says the canvas ratio and the highest quality the model gives it, not any pixel mismatch', () => {
    const html = render(delivery(square, true));
    expect(html).toContain('data-kind="follows"');
    expect(html).toContain(messages['ui.canvas.size.follows'].replace('{ratio}', '1:1').replace('{output}', '2880×2880'));
    expect(html).toContain('最高画质');
    expect(html).not.toContain('2048×2048');
    expect(html).not.toContain('canvas-size-adopt');
    expect(html).not.toContain('unavailable-icon');
  });

  it('shows a ratio-only size with the pixels of its highest tier', () => {
    const gemini: Plan = { value: '21:9', pixels: { width: 6336, height: 2688 }, ratioOnly: true, nearest: false, ratio: '21:9', outputRatio: '21:9' };
    const html = render(delivery(gemini, true, true, wide, '21:9'));
    expect(html).toContain(messages['ui.canvas.size.ratio_output'].replace('{ratio}', '21:9').replace('{pixels}', '6336×2688'));
  });

  it('a model without the canvas ratio: the nearest ratio, and the offer to make the canvas that ratio', () => {
    const html = render(delivery(bananaWide, true, true, wide, '21:9'));
    expect(html).toContain('data-kind="follows_nearest"');
    expect(html).toContain(messages['ui.canvas.size.follows_nearest'].replaceAll('{ratio}', '21:9').replaceAll('{nearest}', '16:9')
      .replace('{output}', messages['ui.canvas.size.ratio_output'].replace('{ratio}', '16:9').replace('{pixels}', '1344×768')));
    expect(html).toContain('画布改为 16:9');
    // Off a canvas turn the missing ratio is still said, before the person sends from the board.
    const idle = render(delivery(bananaWide, false, true, wide, '21:9'));
    expect(idle).toContain('data-kind="ratio_missing"');
    expect(idle).toContain('画布改为 16:9');
  });

  it('says a missing ratio as the shared unavailable notice: icon, short reason, full sentence in the hint (#94)', () => {
    const html = render(delivery(bananaWide, false, true, wide, '21:9'));
    expect(html).toContain('data-testid="unavailable-icon"');
    expect(html).toContain('无法使用，模型没有该画布比例');
    expect(html).toMatch(/data-testid="unavailable-detail"[^>]*>当前模型没有画布比例 21:9/);
  });

  it('never offers to resize the canvas to the size it already has', () => {
    // A 4:1 canvas whose nearest legal ratio rounds back to the same pixels (an extreme the clamp cannot change).
    const pinned: Plan = { value: '3840x1280', pixels: { width: 3840, height: 1280 }, ratioOnly: false, nearest: true, ratio: '4:1', outputRatio: '4:1' };
    expect(render(delivery(pinned, true, true, { width: 8192, height: 2048 }, '4:1'))).not.toContain('canvas-size-adopt');
  });
});

describe('ReferenceSizeNotice (#296)', () => {
  const renderReference = (value: CanvasDelivery) => renderToStaticMarkup(<ReferenceSizeNotice delivery={value} />);
  const upload = { width: 800, height: 600 };
  const withReference = (turn: NonNullable<CanvasDelivery['reference']>['turn'], canvasTurn: Plan | null = null) =>
    ({ ...delivery(canvasTurn, canvasTurn !== null), reference: { pixels: upload, turn } });

  it('says the size an edit of the one attached picture sends instead of the default (the #296 scene)', () => {
    const html = renderReference(withReference({ value: '1536x1152', pixels: { width: 1536, height: 1152 }, ratioOnly: false, nearest: false, changed: true }));
    expect(html).toContain('data-kind="follows_reference"');
    expect(html).toContain(messages['ui.canvas.size.follows_reference'].replace('{output}', '1536×1152').replace('{reference}', '800×600'));
  });

  it('names the nearest legal size when the model cannot give the picture its shape', () => {
    const html = renderReference(withReference({ value: '4704x3520', pixels: { width: 4704, height: 3520 }, ratioOnly: false, nearest: true, changed: true }));
    expect(html).toContain('data-kind="follows_reference_nearest"');
    expect(html).toContain('data-testid="unavailable-icon"');
    expect(html).toContain(messages['ui.unavailable.reason.canvas.reference_ratio_unsupported']);
    expect(html).toContain(messages['ui.canvas.size.follows_reference_nearest'].replace('{reference}', '800×600').replace('{output}', '4704×3520'));
  });

  it('says nothing when the size is the default anyway, without a reference, or when the artboard is the source', () => {
    expect(renderReference(withReference({ value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: false, changed: false }))).toBe('');
    expect(renderReference(delivery(null))).toBe('');
    const canvasTurn = square;
    expect(renderReference(withReference({ value: '1536x1152', pixels: { width: 1536, height: 1152 }, ratioOnly: false, nearest: false, changed: true }, canvasTurn))).toBe('');
  });
});
