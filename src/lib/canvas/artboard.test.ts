import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import { artboardRatioLabel, canvasArtboard, clampCanvasPixels, legacyArtboard, migrateLegacyNodes } from './artboard';
import { exportCanvasToBlob } from './export-canvas';

const schema = defaultCatalog.application.canvas as unknown as CanvasProductSchema;

describe('canvas pixel frame (#246)', () => {
  afterEach(() => vi.unstubAllGlobals());
  const anchor = schema.frontend.artboard;

  it('is the canvas pixel size centred on the Schema anchor: one world unit per pixel', () => {
    expect(canvasArtboard(anchor, { width: 2048, height: 2048 })).toEqual({ x: -424, y: -624, width: 2048, height: 2048 });
    expect(canvasArtboard(anchor, { width: 3840, height: 2160 })).toEqual({ x: -1320, y: -680, width: 3840, height: 2160 });
  });

  it('changes size with the resolution even when the ratio stays the same, keeping its centre', () => {
    const small = canvasArtboard(anchor, { width: 1024, height: 1024 });
    const large = canvasArtboard(anchor, { width: 2048, height: 2048 });
    expect(large.width / small.width).toBe(2);
    expect(small.x + small.width / 2).toBe(large.x + large.width / 2);
    expect(small.y + small.height / 2).toBe(large.y + large.height / 2);
  });

  it('rejects an invalid anchor or size rather than guessing a frame', () => {
    expect(() => canvasArtboard({ ...anchor, center_x: NaN }, { width: 10, height: 10 })).toThrow('invalid canvas artboard');
    expect(() => canvasArtboard(anchor, { width: 0, height: 10 })).toThrow('invalid canvas artboard');
    expect(() => legacyArtboard({ ...anchor, legacy_max_edge: 0 }, { width: 10, height: 10 })).toThrow('invalid canvas artboard');
  });

  it('reproduces the legacy 512-unit frame for the ratio of a size', () => {
    expect(legacyArtboard(anchor, { width: 2048, height: 2048 })).toEqual({ x: 344, y: 144, width: 512, height: 512 });
    expect(legacyArtboard(anchor, { width: 1536, height: 1024 })).toEqual({ x: 344, y: 229 + 1 / 3, width: 512, height: 341 + 1 / 3 });
  });

  it('clamps a size into the Schema canvas range in whole pixels', () => {
    expect(clampCanvasPixels({ width: 10000, height: 10.4 }, { minimum_dimension: 64, maximum_dimension: 8192 })).toEqual({ width: 8192, height: 64 });
    expect(clampCanvasPixels(null, { minimum_dimension: 64, maximum_dimension: 8192 })).toBeNull();
    expect(clampCanvasPixels({ width: NaN, height: 1 }, { minimum_dimension: 64, maximum_dimension: 8192 })).toBeNull();
  });

  it('migrates a legacy canvas into the pixel frame around the centre, keeping the composition', () => {
    const size = { width: 2048, height: 2048 };
    const from = legacyArtboard(anchor, size);
    const to = canvasArtboard(anchor, size);
    const nodes: CanvasNode[] = [
      // Fills the old frame exactly.
      { id: 'image', type: 'image', x: 344, y: 144, width: 512, height: 512, zIndex: 1, imageUrl: 'a', taskId: 't', crop: { x: 1, y: 2, width: 3, height: 4 }, rotation: 15 },
      // Half outside the old frame on the left.
      { id: 'shape', type: 'shape', shapeType: 'rectangle', x: 300, y: 400, width: 88, height: 10, zIndex: 2, imageUrl: '', taskId: '' },
      { id: 'text', type: 'text', text: 'hi', x: 600, y: 400, width: 100, height: 20, zIndex: 3, imageUrl: '', taskId: '', fontSize: 24 },
      { id: 'pencil', type: 'pencil', pathData: 'M 4 4 L 10.5 20', x: 500, y: 300, width: 20, height: 30, zIndex: 4, imageUrl: '', taskId: '', strokeWidth: 3 },
    ];
    const migrated = migrateLegacyNodes(nodes, from, to, { strokeWidth: 2, fontSize: 24 });
    expect(migrated[0]).toMatchObject({ x: to.x, y: to.y, width: 2048, height: 2048, crop: { x: 1, y: 2, width: 3, height: 4 }, rotation: 15 });
    expect(migrated[1]).toMatchObject({ x: 600 + (300 - 600) * 4, y: 400, width: 352, height: 40, strokeWidth: 8 });
    expect(migrated[2]).toMatchObject({ x: 600, y: 400, width: 400, height: 80, fontSize: 96 });
    expect(migrated[3]).toMatchObject({ pathData: 'M 16 16 L 42 80', strokeWidth: 12, width: 80, height: 120 });
    // The relation to the frame is unchanged: same place relative to the frame, in frame units.
    for (const [index, node] of nodes.entries()) {
      expect((migrated[index].x - to.x) / to.width).toBeCloseTo((node.x - from.x) / from.width, 10);
      expect((migrated[index].y - to.y) / to.height).toBeCloseTo((node.y - from.y) / from.height, 10);
    }
    expect(nodes[0].width).toBe(512);
  });

  it('keeps a path it cannot scale safely', () => {
    const from = legacyArtboard(anchor, { width: 1024, height: 1024 });
    const to = canvasArtboard(anchor, { width: 1024, height: 1024 });
    const [node] = migrateLegacyNodes([
      { id: 'arc', type: 'pencil', pathData: 'M 0 0 A 5 5 0 0 1 10 10', x: 0, y: 0, width: 10, height: 10, zIndex: 1, imageUrl: '', taskId: '' },
    ], from, to, { strokeWidth: 2, fontSize: 24 });
    expect(node.pathData).toBe('M 0 0 A 5 5 0 0 1 10 10');
    expect(node.strokeWidth).toBe(4);
  });

  it('exports the fallback frame pixels and clips outside nodes instead of their bounding box', async () => {
    const context = {
      fillStyle: '', globalAlpha: 1, font: '', textBaseline: '',
      fillRect: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(),
      save: vi.fn(), restore: vi.fn(), translate: vi.fn(), rotate: vi.fn(), fillText: vi.fn(),
      measureText: (text: string) => ({ width: text.length * 6, fontBoundingBoxAscent: 10, fontBoundingBoxDescent: 3 }),
    };
    const canvas = {
      width: 0, height: 0,
      getContext: () => context,
      toBlob: (done: (blob: Blob) => void) => done(new Blob(['png'], { type: 'image/png' })),
    };
    vi.stubGlobal('document', { createElement: () => canvas });
    const nodes: CanvasNode[] = [
      { id: 'inside', type: 'text', x: 400, y: 200, width: 30, height: 20, zIndex: 1, text: 'inside', imageUrl: '', taskId: '' },
      { id: 'outside', type: 'text', x: -900, y: -400, width: 30, height: 20, zIndex: 2, text: 'outside', imageUrl: '', taskId: '' },
    ];
    const frame = canvasArtboard(schema.frontend.artboard, { width: 512, height: 512 });
    const blob = await exportCanvasToBlob(nodes, schema, { frame });
    expect(blob.type).toBe('image/png');
    expect(canvas.width).toBe(frame.width);
    expect(canvas.height).toBe(frame.height);
    expect(context.rect).toHaveBeenCalledWith(0, 0, frame.width, frame.height);
    expect(context.clip).toHaveBeenCalledOnce();
    expect(context.translate).toHaveBeenCalledWith(400 - frame.x, 200 - frame.y);
    expect(context.translate).toHaveBeenCalledWith(-900 - frame.x, -400 - frame.y);
  });
});

describe('artboardRatioLabel', () => {
  const common = ['1:1', '4:3', '3:4', '3:2', '2:3', '16:9', '9:16', '21:9'];
  it('reads sizes as their common ratio', () => {
    expect(artboardRatioLabel(1536, 1024, common)).toBe('3:2');
    expect(artboardRatioLabel(512, 512, common)).toBe('1:1');
    expect(artboardRatioLabel(1080, 1920, common)).toBe('9:16');
  });
  it('picks the nearest common ratio', () => {
    expect(artboardRatioLabel(1000, 760, common)).toBe('4:3');
  });
  it('is empty for invalid sizes', () => {
    expect(artboardRatioLabel(0, 10, common)).toBe('');
  });
});
