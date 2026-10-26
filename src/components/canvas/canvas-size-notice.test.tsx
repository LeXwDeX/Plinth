import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
import { CanvasSizeNotice, ReferenceSizeNotice } from './canvas-size-notice';

const messages = catalog.application.content.messages as Record<string, string>;

function delivery(fit: CanvasDelivery['fit'], known = true, turn: CanvasDelivery['turn'] = null, pixels = { width: 2048, height: 2048 }): CanvasDelivery {
  return {
    artboard: { x: 600 - pixels.width / 2, y: 400 - pixels.height / 2, ...pixels }, pixels,
    known, initial: { width: 2048, height: 2048 }, fit, turn, source: turn !== null, settled: true,
    generation: { setValue: vi.fn() } as unknown as CanvasDelivery['generation'],
  };
}
const render = (value: CanvasDelivery) => renderToStaticMarkup(<CanvasSizeNotice sessionId="s" delivery={value} />);

describe('CanvasSizeNotice (#246)', () => {
  it('says nothing when the model matches, has no size setting, or the canvas size is not known yet', () => {
    expect(render(delivery({ kind: 'match' }))).toBe('');
    expect(render(delivery({ kind: 'none' }))).toBe('');
    expect(render(delivery({ kind: 'conflict', output: { width: 4096, height: 4096 } }, false))).toBe('');
  });

  it('offers to set the model to the canvas size when it has that option', () => {
    const html = render(delivery({ kind: 'align', settings: { size: '2048x2048' }, output: { width: 1024, height: 1024 } }));
    expect(html).toContain('data-kind="align"');
    expect(html).toContain('模型将输出 1024×1024，画布是 2048×2048');
    expect(html).toContain('data-testid="canvas-size-align"');
    expect(html).toContain(messages['ui.canvas.size.align']);
  });

  it('states the conflict and offers to adopt the model output, clamped to the Schema canvas range', () => {
    const html = render(delivery({ kind: 'conflict', output: { width: 4096, height: 4096 } }));
    expect(html).toContain('当前模型不能输出画布尺寸 2048×2048，将输出 4096×4096');
    expect(html).toContain('画布改为 4096×4096');
    const unknown = render(delivery({ kind: 'conflict', output: null }));
    expect(unknown).toContain('当前模型不能输出画布尺寸 2048×2048');
    expect(unknown).not.toContain('canvas-size-adopt');
  });

  it('on a canvas-sourced turn says the output follows the artboard and the size really sent, not the remembered one (#284)', () => {
    // The #284 scene: 1536x1152 remembered for the session, a 2048x2048 artboard holding the layout.
    const stale = { kind: 'align' as const, settings: { size: '2048x2048' }, output: { width: 1536, height: 1152 } };
    const html = render(delivery(stale, true, { value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: false }));
    expect(html).toContain('data-kind="follows"');
    expect(html).toContain(messages['ui.canvas.size.follows'].replace('{output}', '2048×2048').replace('{canvas}', '2048×2048'));
    expect(html).not.toContain('1536×1152');
    expect(html).not.toContain('canvas-size-align');
    // Nothing to say when the remembered setting already is the canvas size.
    expect(render(delivery({ kind: 'match' }, true, { value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: false }))).toBe('');
  });

  it('above the model maximum names the nearest legal size and offers to make the canvas that size (#284)', () => {
    const big = { width: 4096, height: 4096 };
    const html = render(delivery({ kind: 'align', settings: {}, output: { width: 1536, height: 1152 } }, true,
      { value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: true }, big));
    expect(html).toContain('data-kind="follows_nearest"');
    expect(html).toContain(messages['ui.canvas.size.follows_nearest'].replace('{canvas}', '4096×4096').replace('{output}', '2048×2048'));
    expect(html).toContain('画布改为 2048×2048');
    // Even when the remembered setting matched the canvas, the nearest size is said (the model cannot give it).
    expect(render(delivery({ kind: 'match' }, true, { value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: true }, big)))
      .toContain('data-kind="follows_nearest"');
  });

  it('shows a ratio-only size with the pixels the Schema maps it to', () => {
    const html = render(delivery({ kind: 'conflict', output: { width: 4096, height: 4096 } }, true,
      { value: '21:9', pixels: { width: 6336, height: 2688 }, ratioOnly: true, nearest: true }, { width: 2048, height: 1000 }));
    expect(html).toContain(messages['ui.canvas.size.ratio_output'].replace('{ratio}', '21:9').replace('{pixels}', '6336×2688'));
  });
});

describe('ReferenceSizeNotice (#296)', () => {
  const renderReference = (value: CanvasDelivery) => renderToStaticMarkup(<ReferenceSizeNotice delivery={value} />);
  const upload = { width: 800, height: 600 };
  const withReference = (turn: NonNullable<CanvasDelivery['reference']>['turn'], canvasTurn: CanvasDelivery['turn'] = null) =>
    ({ ...delivery({ kind: 'match' }, true, canvasTurn), reference: { pixels: upload, turn } });

  it('says the size an edit of the one attached picture sends instead of the default (the #296 scene)', () => {
    const html = renderReference(withReference({ value: '1536x1152', pixels: { width: 1536, height: 1152 }, ratioOnly: false, nearest: false, changed: true }));
    expect(html).toContain('data-kind="follows_reference"');
    expect(html).toContain(messages['ui.canvas.size.follows_reference'].replace('{output}', '1536×1152').replace('{reference}', '800×600'));
  });

  it('names the nearest legal size when the model cannot give the picture its shape', () => {
    const html = renderReference(withReference({ value: '4704x3520', pixels: { width: 4704, height: 3520 }, ratioOnly: false, nearest: true, changed: true }));
    expect(html).toContain('data-kind="follows_reference_nearest"');
    expect(html).toContain(messages['ui.canvas.size.follows_reference_nearest'].replace('{reference}', '800×600').replace('{output}', '4704×3520'));
  });

  it('says nothing when the size is the default anyway, without a reference, or when the artboard is the source', () => {
    expect(renderReference(withReference({ value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: false, changed: false }))).toBe('');
    expect(renderReference(delivery({ kind: 'match' }))).toBe('');
    const canvasTurn = { value: '2048x2048', pixels: { width: 2048, height: 2048 }, ratioOnly: false, nearest: false };
    expect(renderReference(withReference({ value: '1536x1152', pixels: { width: 1536, height: 1152 }, ratioOnly: false, nearest: false, changed: true }, canvasTurn))).toBe('');
  });
});
