import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { canvasArtboard } from './artboard';
import { artboardLabelBox } from './artboard-label';

const artboardSchema = catalog.application.canvas.frontend.artboard;
const board = canvasArtboard(artboardSchema, { width: 2048, height: 2048 });

describe('the size label sits inside the frame (#282)', () => {
  it('reads "工作区：2048×2048，1:1" from the Schema text', () => {
    const template = schemaText(catalog.application as unknown as ProductSchema, 'ui.canvas.size.label');
    expect(formatSchemaText(template, { width: 2048, height: 2048, ratio: '1:1' })).toBe('工作区：2048×2048，1:1');
  });

  it('is inset in the top-left corner and keeps its screen size at any zoom', () => {
    for (const zoom of [0.1, 0.34, 1, 4]) {
      const box = artboardLabelBox(board, zoom, 120, artboardSchema.label);
      const inset = artboardSchema.label.offset_pixels / zoom;
      expect(box.plate.x).toBeCloseTo(board.x + inset, 9);
      expect(box.plate.y).toBeCloseTo(board.y + inset, 9);
      expect(box.plate.x).toBeGreaterThan(board.x);
      expect(box.plate.y).toBeGreaterThan(board.y);
      expect(box.plate.width * zoom).toBeCloseTo(120 + artboardSchema.label.padding_pixels * 2, 9);
      expect(box.plate.height * zoom).toBeCloseTo(artboardSchema.label.font_size_pixels + artboardSchema.label.padding_pixels * 2, 9);
      expect(box.text.scale).toBeCloseTo(1 / zoom, 12);
      expect(box.text.x).toBeGreaterThan(box.plate.x);
    }
  });
});
