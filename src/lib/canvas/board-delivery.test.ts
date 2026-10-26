import { afterEach, describe, expect, it, vi } from 'vitest';

const exported = vi.hoisted(() => ({ exportArtboardToBlob: vi.fn(async () => new Blob(['x'])) }));
vi.mock('./export-canvas', () => exported);
vi.mock('./pending-edits', () => ({ flushPendingEdits: vi.fn(async () => undefined) }));

import { boardDeliveryOf, publishBoardDelivery, rasteriseSessionBoard, boardPixelsFor3D } from './board-delivery';
import type { ProductSchema } from '@/lib/product-schema';

const schema = { canvas: { marker: 'canvas', frontend: { to_3d_long_edge_pixels: 1024 } } } as unknown as ProductSchema;
const delivery = { artboard: { x: 1, y: 2, width: 30, height: 20 }, pixels: { width: 300, height: 200 } };

afterEach(() => {
  publishBoardDelivery('s', null);
  exported.exportArtboardToBlob.mockClear();
});

describe('board delivery', () => {
  it('remembers the published frame per session and forgets it on null', () => {
    expect(boardDeliveryOf('s')).toBeNull();
    publishBoardDelivery('s', delivery);
    expect(boardDeliveryOf('s')).toBe(delivery);
    expect(boardDeliveryOf('other')).toBeNull();
    publishBoardDelivery('s', null);
    expect(boardDeliveryOf('s')).toBeNull();
  });

  it('rasterises through the single exporter at the delivery size', async () => {
    publishBoardDelivery('s', delivery);
    await rasteriseSessionBoard(schema, 's');
    expect(exported.exportArtboardToBlob).toHaveBeenCalledWith(expect.any(Array), schema.canvas, delivery.artboard, delivery.pixels);
  });

  it('refuses when no board published a frame', async () => {
    await expect(rasteriseSessionBoard(schema, 'none')).rejects.toThrow('canvas artboard unavailable');
  });

  it('sizes the 3D picture from the Schema long edge when no exact pixels are chosen', () => {
    expect(boardPixelsFor3D(schema, { artboard: { x: 0, y: 0, width: 512, height: 341 }, pixels: null })).toEqual({ width: 1024, height: 682 });
    expect(boardPixelsFor3D(schema, { artboard: { x: 0, y: 0, width: 512, height: 512 }, pixels: null })).toEqual({ width: 1024, height: 1024 });
    expect(boardPixelsFor3D(schema, delivery)).toEqual({ width: 300, height: 200 });
  });
});
