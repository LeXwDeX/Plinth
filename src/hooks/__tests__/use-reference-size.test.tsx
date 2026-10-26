import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasDelivery } from '../use-canvas-artboard';

const measured = vi.hoisted(() => ({ urls: [] as string[] }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
// The picture's real pixels: a 4:3 upload, read once it is known which picture to measure.
vi.mock('@/hooks/use-image-size', () => ({ useImageSize: (url: string) => {
  measured.urls.push(url);
  return url ? { width: 800, height: 600 } : null;
} }));
import { useReferenceSize } from '../use-reference-size';

const gptImage = (catalog.offerings as unknown as Array<{ id: string }>).find((item) => item.id === 'openai/gpt-image-2/image-to-image');

function delivery(source: boolean, states: Record<string, { source: 'default' | 'agent' | 'user' }> = {}): CanvasDelivery {
  return {
    artboard: { x: 0, y: 0, width: 2048, height: 2048 }, pixels: { width: 2048, height: 2048 }, known: true,
    initial: { width: 2048, height: 2048 }, fit: { kind: 'match' }, turn: null, source, settled: true,
    generation: { offering: { ...gptImage, available: true }, values: { size: '2048x2048' }, parameterStates: states } as unknown as CanvasDelivery['generation'],
  };
}

function reference(value: CanvasDelivery, pictures: string[], image = true) {
  let result!: CanvasDelivery;
  function Probe() { result = useReferenceSize(value, pictures, image); return null; }
  renderToStaticMarkup(<Probe />);
  return result.reference;
}

const upload = 'data:image/png;base64,iVBORw0KGgo';

describe('useReferenceSize (#296)', () => {
  it('gives the size an edit of the one composer picture sends when nobody chose a size', () => {
    expect(reference(delivery(false), [upload])).toEqual({
      pixels: { width: 800, height: 600 },
      turn: { value: '1536x1152', pixels: { width: 1536, height: 1152 }, ratioOnly: false, nearest: false, changed: true },
    });
  });

  it('gives nothing for several pictures, an artboard source, a 3D stage or a chosen size', () => {
    measured.urls = [];
    expect(reference(delivery(false), [upload, upload])).toBeNull();
    expect(reference(delivery(true), [upload])).toBeNull();
    expect(reference(delivery(false), [upload], false)).toBeNull();
    // None of those even loads the picture.
    expect(measured.urls.every((url) => url === '')).toBe(true);
    expect(reference(delivery(false, { size: { source: 'user' } }), [upload])).toBeNull();
  });
});
