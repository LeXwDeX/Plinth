import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { canvasArtboard } from './artboard';
import { artboardCentred, initialArtboardViewport, viewportAfterResize, zoomKeepingFrame } from './viewport';

const config = catalog.application.canvas.frontend.viewport;
const board = canvasArtboard(catalog.application.canvas.frontend.artboard, { width: 2048, height: 2048 });
const size = { width: 780, height: 696 };
const centreOf = (view: { zoom: number; panX: number; panY: number }) => ({
  x: (board.x + board.width / 2) * view.zoom + view.panX,
  y: (board.y + board.height / 2) * view.zoom + view.panY,
});

describe('the dashed frame stays centred (#282)', () => {
  it('starts centred in the drawing area', () => {
    const view = initialArtboardViewport(board, size, config);
    expect(artboardCentred(board, size, view)).toBe(true);
  });

  it('zooming in and out with the wheel, pinch or zoom tool keeps a centred frame centred, wherever the pointer is', () => {
    let view = initialArtboardViewport(board, size, config);
    for (const [pointer, factor] of [[{ x: 10, y: 10 }, 1.25], [{ x: 700, y: 600 }, 1.25], [{ x: 50, y: 650 }, 0.6], [{ x: 390, y: 100 }, 0.9]] as const) {
      view = zoomKeepingFrame(view, pointer, factor, config, board, size);
      const centre = centreOf(view);
      expect(centre.x).toBeCloseTo(size.width / 2, 6);
      expect(centre.y).toBeCloseTo(size.height / 2, 6);
    }
  });

  it('after the person pans the frame away, zoom follows the pointer again', () => {
    const start = initialArtboardViewport(board, size, config);
    const panned = { ...start, panX: start.panX + 200 };
    expect(artboardCentred(board, size, panned)).toBe(false);
    const pointer = { x: 100, y: 100 };
    const world = { x: (pointer.x - panned.panX) / panned.zoom, y: (pointer.y - panned.panY) / panned.zoom };
    const zoomed = zoomKeepingFrame(panned, pointer, 2, config, board, size);
    expect(world.x * zoomed.zoom + zoomed.panX).toBeCloseTo(pointer.x, 6);
    expect(world.y * zoomed.zoom + zoomed.panY).toBeCloseTo(pointer.y, 6);
  });

  it('when the drawing area resizes, a centred frame stays centred and a panned one stays put', () => {
    const start = initialArtboardViewport(board, size, config);
    const wider = { width: 1100, height: 696 };
    const resized = viewportAfterResize(start, size, wider, board);
    expect(resized).not.toBeNull();
    expect(centreOf(resized!).x).toBeCloseTo(wider.width / 2, 6);
    expect(centreOf(resized!).y).toBeCloseTo(wider.height / 2, 6);
    expect(resized!.zoom).toBe(start.zoom);
    expect(viewportAfterResize({ ...start, panX: start.panX + 200 }, size, wider, board)).toBeNull();
    expect(viewportAfterResize(start, size, size, board)).toBeNull();
    expect(viewportAfterResize(start, size, wider, null)).toBeNull();
  });
});
