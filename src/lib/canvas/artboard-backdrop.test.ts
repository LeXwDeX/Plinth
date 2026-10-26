import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import { artboardBackdrop } from './artboard-backdrop';

const canvas = catalog.application.canvas as unknown as CanvasProductSchema;

describe('artboardBackdrop (#246)', () => {
  it('shows a checkerboard while the canvas exports with a transparent background (#271)', () => {
    expect(canvas.frontend.export.artboard_background_color).toBe(canvas.frontend.export.transparent_background_value);
    expect(artboardBackdrop(canvas)).toEqual({ kind: 'checker', cell: 8, light: '#ffffff', dark: '#000000', opacity: 0.08 });
  });

  it('fills the canvas with an opaque export background instead', () => {
    const white = { ...canvas, frontend: { ...canvas.frontend, export: { ...canvas.frontend.export, artboard_background_color: '#ffffff' } } };
    expect(artboardBackdrop(white)).toEqual({ kind: 'fill', color: '#ffffff' });
  });
});
