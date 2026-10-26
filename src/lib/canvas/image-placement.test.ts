import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import { imageNodePlacement } from './image-placement';

const frontend = (defaultCatalog.application.canvas as unknown as CanvasProductSchema).frontend;
const artboard = { x: 50, y: 80, width: 600, height: 300 };
const image: CanvasNode = {
  id: 'existing', type: 'image', taskId: 'task-a', imageUrl: '/api/v1/assets/a',
  x: 50, y: 80, width: 200, height: 100, zIndex: 1,
};

describe('shared canvas image placement', () => {
  it('centers the first screenshot in the selected frame without changing its aspect ratio', () => {
    expect(imageNodePlacement([], { width: 400, height: 400 }, frontend, artboard))
      .toEqual({ x: 200, y: 80, width: 300, height: 300 });
  });

  it('centres a smaller picture in an empty canvas at its own pixels instead of enlarging it (#246)', () => {
    expect(imageNodePlacement([], { width: 120, height: 60 }, frontend, artboard))
      .toEqual({ x: 290, y: 200, width: 120, height: 60 });
  });

  it('preserves existing frame content and places the screenshot after the rightmost image', () => {
    const rightmost = { ...image, id: 'rightmost', x: 900, y: 90 };
    const nodes = [rightmost, image];
    const placement = imageNodePlacement(nodes, { width: 2048, height: 1024 }, frontend, artboard);
    // #246: pixels are world units, so the picture keeps its pixels, shrunk only to fit the 600x300 canvas.
    expect(placement).toEqual({ x: 1100 + frontend.node_gap_pixels, y: 90, width: 600, height: 300 });
    expect(imageNodePlacement(nodes, { width: 240, height: 120 }, frontend, artboard)).toMatchObject({ width: 240, height: 120 });
    expect(nodes).toEqual([rightmost, image]);
  });

  it('uses configured initial placement and fallback dimensions when no frame or pixels are available', () => {
    expect(imageNodePlacement([], { width: 0, height: 0 }, frontend)).toEqual({
      x: frontend.initial_node_x, y: frontend.initial_node_y,
      width: frontend.drawing.default_node_size, height: frontend.drawing.default_node_size,
    });
  });

  it('never places a picture over the frame when the frame holds content: it goes after the frame edge (#284)', () => {
    // The local reproduction of #284: a 2048 canvas (frame -424,-624) holding a 1536 picture centred in it. A
    // canvas-sourced result used to go 20 px after that picture, 236 px into the frame and over the layout.
    const frame = { x: -424, y: -624, width: 2048, height: 2048 };
    const layout: CanvasNode = { ...image, id: 'layout', x: -168, y: -368, width: 1536, height: 1536 };
    const result = imageNodePlacement([layout], { width: 2048, height: 2048 }, frontend, frame);
    expect(result).toEqual({ x: frame.x + frame.width + frontend.node_gap_pixels, y: frame.y, width: 2048, height: 2048 });
    // The next result follows the first one, outside the frame, as before.
    const placed: CanvasNode = { ...image, id: 'result', ...result };
    expect(imageNodePlacement([layout, placed], { width: 1024, height: 1024 }, frontend, frame))
      .toEqual({ x: result.x + result.width + frontend.node_gap_pixels, y: result.y, width: 1024, height: 1024 });
    // A picture that ends exactly at the frame edge: still next to the frame, not over it.
    const flush: CanvasNode = { ...image, id: 'flush', x: frame.x + 1024, y: frame.y + 100, width: 1024, height: 1024 };
    expect(imageNodePlacement([flush], { width: 512, height: 512 }, frontend, frame))
      .toMatchObject({ x: frame.x + frame.width + frontend.node_gap_pixels, y: frame.y });
  });
});
