import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { resizeAnchorFor } from './apply-canvas-resize';

const artboard = (catalog.application as unknown as ProductSchema).canvas.frontend.artboard;

describe('resizeAnchorFor (#246 phase 2)', () => {
  it('uses the Schema default (centre) until the viewer picks an offered anchor', () => {
    expect(resizeAnchorFor(artboard, null)).toBe('center');
    expect(resizeAnchorFor(artboard, 'top-left')).toBe('top-left');
    expect(resizeAnchorFor({ ...artboard, resize: { default_anchor: 'bottom', anchors: ['bottom', 'center'] } }, 'top-left')).toBe('bottom');
  });
});
