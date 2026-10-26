import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { bindCanvasZoomGestures, fitArtboardViewport, fitContentOrArtboardViewport, fitRectViewport, fitViewportToRect, initialArtboardViewport, recenterArtboardViewport, viewportUnchanged, visibleContentBounds, zoomAroundCenter, zoomCanvasAtPoint, zoomStepViewport } from './viewport';
import { canvasArtboard } from './artboard';

const config = catalog.application.canvas.frontend.viewport;
// A 512-pixel square canvas around the Schema anchor.
const board = canvasArtboard(catalog.application.canvas.frontend.artboard, { width: 512, height: 512 });

describe('canvas viewport gestures', () => {
  const setup = () => {
    const target = Object.assign(new EventTarget(), { getBoundingClientRect: () => ({ left: 10, top: 20 }) });
    const zoom = vi.fn();
    const pan = vi.fn();
    const cleanup = bindCanvasZoomGestures(target as unknown as HTMLElement, config, { zoom, pan });
    const dispatch = (type: string, values: Record<string, unknown>) => {
      const event = Object.assign(new Event(type, { cancelable: true }), values);
      target.dispatchEvent(event);
      return event;
    };
    return { zoom, pan, dispatch, cleanup };
  };

  it('pans with the plain wheel and a two-finger swipe, vertically by deltaY and horizontally by deltaX', () => {
    const { zoom, pan, dispatch } = setup();
    const event = dispatch('wheel', { deltaX: 0, deltaY: 120, deltaMode: 0, clientX: 110, clientY: 220 });
    expect(event.defaultPrevented).toBe(true);
    expect(pan).toHaveBeenLastCalledWith(-0, -120);
    dispatch('wheel', { deltaX: 30, deltaY: -10, deltaMode: 0, clientX: 110, clientY: 220 });
    expect(pan).toHaveBeenLastCalledWith(-30, 10);
    // Line-mode wheels are converted with the Schema's line height.
    dispatch('wheel', { deltaX: 0, deltaY: 3, deltaMode: 1, clientX: 110, clientY: 220 });
    expect(pan).toHaveBeenLastCalledWith(-0, -3 * config.wheel_line_height_pixels);
    expect(zoom).not.toHaveBeenCalled();
  });

  it('pans sideways with Shift+wheel whichever axis the browser reports', () => {
    const { pan, dispatch } = setup();
    dispatch('wheel', { shiftKey: true, deltaX: 0, deltaY: 80, deltaMode: 0, clientX: 110, clientY: 220 });
    expect(pan).toHaveBeenLastCalledWith(-80, 0);
    dispatch('wheel', { shiftKey: true, deltaX: 60, deltaY: 0, deltaMode: 0, clientX: 110, clientY: 220 });
    expect(pan).toHaveBeenLastCalledWith(-60, 0);
  });

  it('zooms at the pointer with Alt+wheel, like Ctrl+wheel, and never pans on those', () => {
    const { zoom, pan, dispatch } = setup();
    dispatch('wheel', { altKey: true, deltaY: -100, deltaMode: 0, clientX: 110, clientY: 220 });
    expect(zoom).toHaveBeenLastCalledWith({ x: 100, y: 200 }, 1 + config.zoom_step);
    dispatch('wheel', { ctrlKey: true, altKey: false, deltaY: 100, deltaMode: 0, clientX: 110, clientY: 220 });
    expect(zoom).toHaveBeenLastCalledWith({ x: 100, y: 200 }, 1 / (1 + config.zoom_step));
    expect(pan).not.toHaveBeenCalled();
  });

  it('zooms ctrl-wheel pinch at the pointer', () => {
    const { zoom, dispatch } = setup();
    const event = dispatch('wheel', { ctrlKey: true, deltaY: -100, clientX: 110, clientY: 220 });
    expect(event.defaultPrevented).toBe(true);
    expect(zoom).toHaveBeenLastCalledWith({ x: 100, y: 200 }, 1 + config.zoom_step);
    dispatch('wheel', { ctrlKey: true, deltaY: 100, clientX: 110, clientY: 220 });
    expect(zoom).toHaveBeenLastCalledWith({ x: 100, y: 200 }, 1 / (1 + config.zoom_step));
  });

  it('uses relative Safari gesture scales without double-counting wheel events and removes listeners', () => {
    const { zoom, dispatch, cleanup } = setup();
    expect(dispatch('gesturestart', { scale: 1 }).defaultPrevented).toBe(true);
    dispatch('gesturechange', { scale: 1.2, clientX: 110, clientY: 220 });
    dispatch('gesturechange', { scale: 1.5, clientX: 110, clientY: 220 });
    expect(zoom.mock.calls).toEqual([[{ x: 100, y: 200 }, 1.2], [{ x: 100, y: 200 }, 1.25]]);
    dispatch('wheel', { ctrlKey: true, deltaY: -100, clientX: 110, clientY: 220 });
    expect(zoom).toHaveBeenCalledTimes(2);
    dispatch('gestureend', {});
    dispatch('wheel', { ctrlKey: true, deltaY: -100, clientX: 110, clientY: 220 });
    expect(zoom).toHaveBeenCalledTimes(3);
    cleanup();
    dispatch('wheel', { ctrlKey: true, deltaY: -100 });
    dispatch('gesturestart', { scale: 1 });
    dispatch('gesturechange', { scale: 2 });
    expect(zoom).toHaveBeenCalledTimes(3);
  });

  it('does not apply Safari gesture zoom a second time during touchscreen pinch', () => {
    const { zoom, dispatch } = setup();
    dispatch('touchstart', { touches: [{}, {}] });
    dispatch('gesturestart', { scale: 1 });
    dispatch('gesturechange', { scale: 2, clientX: 110, clientY: 220 });
    dispatch('wheel', { ctrlKey: true, deltaY: -100, clientX: 110, clientY: 220 });
    expect(zoom).not.toHaveBeenCalled();
    dispatch('gestureend', {});
    dispatch('touchend', { touches: [] });
    dispatch('gesturestart', { scale: 1 });
    dispatch('gesturechange', { scale: 2, clientX: 110, clientY: 220 });
    expect(zoom).toHaveBeenCalledExactlyOnceWith({ x: 100, y: 200 }, 2);
  });

  it('keeps the pointer world coordinates fixed, clamps zoom, and rejects invalid scales', () => {
    const current = { zoom: 1, panX: -100, panY: 50 };
    const pointer = { x: 200, y: 300 };
    for (const factor of [0.5, 2, 100, 0.001]) {
      const next = zoomCanvasAtPoint(current, pointer, factor, config);
      expect(next.zoom).toBeGreaterThanOrEqual(config.minimum_zoom);
      expect(next.zoom).toBeLessThanOrEqual(config.maximum_zoom);
      expect((pointer.x - next.panX) / next.zoom).toBeCloseTo(300);
      expect((pointer.y - next.panY) / next.zoom).toBeCloseTo(250);
    }
    for (const factor of [0, -1, NaN, Infinity]) expect(zoomCanvasAtPoint(current, pointer, factor, config)).toBe(current);
  });

  it('centers the default artboard in desktop and mobile drawing areas without enlarging it', () => {
    for (const size of [{ width: 1440, height: 850 }, { width: 320, height: 500 }]) {
      const viewport = initialArtboardViewport(board, size, config);
      expect((board.x + board.width / 2) * viewport.zoom + viewport.panX).toBe(size.width / 2);
      expect((board.y + board.height / 2) * viewport.zoom + viewport.panY).toBe(size.height / 2);
      expect(board.width * viewport.zoom).toBeLessThanOrEqual(size.width - config.fit_padding_pixels * 2);
      expect(viewport.zoom).toBeLessThanOrEqual(config.default_zoom);
    }
    expect(catalog.application.canvas.frontend.export.frame.stroke_width).toBe(1);
  });
});

describe('fitRectViewport', () => {
  const size = { width: 780, height: 700 };
  const insets = { left: 64, top: 56, right: 16, bottom: 64 };

  it('centres the picture in the visible area and keeps the padding around it', () => {
    const rect = { x: 900, y: 700, width: 1024, height: 1024 };
    const view = fitRectViewport(rect, size, config, insets)!;
    const availableHeight = size.height - insets.top - insets.bottom - config.fit_padding_pixels * 2;
    expect(view.zoom).toBeCloseTo(availableHeight / rect.height, 6);
    const screenCenterX = view.panX + (rect.x + rect.width / 2) * view.zoom;
    const screenCenterY = view.panY + (rect.y + rect.height / 2) * view.zoom;
    expect(screenCenterX).toBeCloseTo(insets.left + (size.width - insets.left - insets.right) / 2, 6);
    expect(screenCenterY).toBeCloseTo(insets.top + (size.height - insets.top - insets.bottom) / 2, 6);
  });

  it('never enlarges a small picture past the default zoom and rejects an unmeasured area', () => {
    const view = fitRectViewport({ x: 0, y: 0, width: 50, height: 50 }, size, config)!;
    expect(view.zoom).toBe(config.default_zoom);
    expect(fitRectViewport({ x: 0, y: 0, width: 50, height: 50 }, { width: 0, height: 0 }, config)).toBeNull();
    expect(fitRectViewport({ x: 0, y: 0, width: 0, height: 50 }, size, config)).toBeNull();
  });

  it('tells an untouched auto-fit viewport from one the user moved', () => {
    const applied = { zoom: 0.5, panX: 10, panY: 20 };
    expect(viewportUnchanged({ ...applied }, applied)).toBe(true);
    expect(viewportUnchanged({ ...applied, panX: 60 }, applied)).toBe(false);
    expect(viewportUnchanged({ ...applied, zoom: 0.8 }, applied)).toBe(false);
    expect(viewportUnchanged(undefined, applied)).toBe(false);
  });
});

describe('recenterArtboardViewport', () => {
  const config = { default_zoom: 1, minimum_zoom: 0.1, maximum_zoom: 3, fit_padding_pixels: 40 } as never;
  const board = { x: 100, y: 100, width: 400, height: 200 };
  it('keeps the zoom and centres the artboard when it fits', () => {
    const view = recenterArtboardViewport(board, { width: 1000, height: 800 }, { zoom: 1.5, panX: 9, panY: 9 }, config);
    expect(view?.zoom).toBe(1.5);
    expect(view?.panX).toBe(500 - 300 * 1.5);
    expect(view?.panY).toBe(400 - 200 * 1.5);
  });
  it('shrinks to fit when the artboard is too large', () => {
    const view = recenterArtboardViewport(board, { width: 280, height: 800 }, { zoom: 2, panX: 0, panY: 0 }, config);
    expect(view?.zoom).toBeCloseTo(0.5);
  });
  it('returns null for an empty area', () => {
    expect(recenterArtboardViewport(board, { width: 0, height: 0 }, { zoom: 1, panX: 0, panY: 0 }, config)).toBeNull();
  });
});

describe('zoom shortcuts (V2)', () => {
  const size = { width: 1000, height: 700 };
  const start = { zoom: 1, panX: 40, panY: 30 };

  it('fit fills the area inside the padding and centres the artboard, and the button and Ctrl+0 share it', () => {
    const view = fitArtboardViewport(board, size, config);
    expect(view).not.toBeNull();
    const pad = config.fit_padding_pixels;
    expect(view!.zoom).toBeCloseTo(Math.min((size.width - pad * 2) / board.width, (size.height - pad * 2) / board.height, config.maximum_zoom));
    expect(view!.panX + (board.x + board.width / 2) * view!.zoom).toBeCloseTo(size.width / 2);
    expect(view!.panY + (board.y + board.height / 2) * view!.zoom).toBeCloseTo(size.height / 2);
    expect(fitArtboardViewport(board, size, config)).toEqual(fitViewportToRect(board, size, config));
  });

  it('fit shrinks a big frame and clamps to the zoom range', () => {
    const huge = fitViewportToRect({ x: 0, y: 0, width: 1e6, height: 1e6 }, size, config);
    expect(huge!.zoom).toBe(config.minimum_zoom);
    const tiny = fitViewportToRect({ x: 0, y: 0, width: 1, height: 1 }, size, config);
    expect(tiny!.zoom).toBe(config.maximum_zoom);
    expect(fitViewportToRect({ x: 0, y: 0, width: 0, height: 5 }, size, config)).toBeNull();
    expect(fitViewportToRect({ x: 0, y: 0, width: 5, height: 5 }, { width: 0, height: 0 }, config)).toBeNull();
  });

  it('Ctrl+= and Ctrl+- move the zoom by one Schema step around the middle of the area', () => {
    const centreBefore = { x: (size.width / 2 - start.panX) / start.zoom, y: (size.height / 2 - start.panY) / start.zoom };
    const bigger = zoomStepViewport(start, size, 1, config);
    expect(bigger.zoom).toBeCloseTo(start.zoom + config.zoom_step);
    expect((size.width / 2 - bigger.panX) / bigger.zoom).toBeCloseTo(centreBefore.x);
    expect((size.height / 2 - bigger.panY) / bigger.zoom).toBeCloseTo(centreBefore.y);
    expect(zoomStepViewport(start, size, -1, config).zoom).toBeCloseTo(start.zoom - config.zoom_step);
  });

  it('stops at the ends of the zoom range', () => {
    expect(zoomStepViewport({ ...start, zoom: config.maximum_zoom }, size, 1, config).zoom).toBe(config.maximum_zoom);
    expect(zoomStepViewport({ ...start, zoom: config.minimum_zoom }, size, -1, config).zoom).toBe(config.minimum_zoom);
  });

  it('Ctrl+1 is 100 percent around the middle', () => {
    const actual = zoomAroundCenter({ zoom: 0.4, panX: 10, panY: 20 }, size, 1, config);
    expect(actual.zoom).toBe(1);
    expect((size.width / 2 - actual.panX) / 1).toBeCloseTo((size.width / 2 - 10) / 0.4);
  });
});

describe('回到画布 fits all visible content (#307)', () => {
  const config = {
    default_zoom: 1, minimum_zoom: 0.1, maximum_zoom: 3, fit_padding_pixels: 40,
  } as ProductSchema['canvas']['frontend']['viewport'];
  const board = { x: 100, y: 100, width: 400, height: 200 };
  const size = { width: 1000, height: 800 };
  const insets = { left: 100, top: 50, right: 20, bottom: 10 };

  it('fits the union of visible, effective layers, not the artboard', () => {
    const content = { x: -600, y: 1200, width: 3000, height: 500 };
    const view = fitContentOrArtboardViewport(content, board, size, config, insets)!;
    const clearX = Math.max(insets.left, insets.right);
    const clearY = Math.max(insets.top, insets.bottom);
    expect(view.zoom).toBeCloseTo(Math.min(
      config.maximum_zoom,
      (size.width - (config.fit_padding_pixels + clearX) * 2) / content.width,
      (size.height - (config.fit_padding_pixels + clearY) * 2) / content.height,
    ));
    expect(view.panX + (content.x + content.width / 2) * view.zoom).toBeCloseTo(size.width / 2);
    expect(view.panY + (content.y + content.height / 2) * view.zoom).toBeCloseTo(size.height / 2);
  });

  it('returns to the default artboard view when there is no content', () => {
    expect(fitContentOrArtboardViewport(null, board, size, config, insets))
      .toEqual(initialArtboardViewport(board, size, config, insets));
  });

  it('is stable on repeated clicks and keeps the Schema zoom range', () => {
    const content = { x: 0, y: 0, width: 1, height: 1 };
    const first = fitContentOrArtboardViewport(content, board, size, config, insets);
    expect(fitContentOrArtboardViewport(content, board, size, config, insets)).toEqual(first);
    expect(first?.zoom).toBe(config.maximum_zoom);
    const huge = fitContentOrArtboardViewport({ x: 0, y: 0, width: 1e7, height: 1e7 }, board, size, config, insets);
    expect(huge?.zoom).toBe(config.minimum_zoom);
  });

  it('uses every visible layer and skips hidden and transparent blank layers', () => {
    const node = (id: string, x: number, extra: Record<string, unknown> = {}) => ({
      id, type: 'image' as const, imageUrl: `/a/${id}`, taskId: '', x, y: 0, width: 100, height: 100, zIndex: 0, ...extra,
    });
    const canvas = catalog.application.canvas as unknown as Pick<ProductSchema['canvas'], 'frontend'>;
    expect(visibleContentBounds([
      node('a', 0),
      node('b', 1000),
      node('hidden', 5000, { visible: false }),
      node('blank', 9000, { blank: true, imageUrl: '', fillColor: 'transparent' }),
    ], canvas)).toEqual({ x: 0, y: 0, width: 1100, height: 100 });
    expect(visibleContentBounds([
      node('a', 0),
      node('filled', 200, { blank: true, imageUrl: '', fillColor: '#fff' }),
    ], canvas)).toEqual({ x: 0, y: 0, width: 300, height: 100 });
    expect(visibleContentBounds([], canvas)).toBeNull();
  });

  it('keeps horizontal, vertical and negative-width lines as content, with their stroke', () => {
    const canvas = catalog.application.canvas as unknown as Pick<ProductSchema['canvas'], 'frontend'>;
    const line = (id: string, x: number, y: number, width: number, height: number, extra: Record<string, unknown> = {}) => ({
      id, type: 'shape' as const, shapeType: 'line' as const, imageUrl: '', taskId: '', x, y, width, height, zIndex: 0, ...extra,
    });
    expect(visibleContentBounds([line('horizontal', 100, 200, 300, 0, { strokeWidth: 2 })], canvas))
      .toEqual({ x: 99, y: 199, width: 302, height: 2 });
    expect(visibleContentBounds([line('vertical', 100, 200, 0, 300, { strokeWidth: 4 })], canvas))
      .toEqual({ x: 98, y: 198, width: 4, height: 304 });
    expect(visibleContentBounds([line('negative', 500, 300, -400, 0, { strokeWidth: 2 })], canvas))
      .toEqual({ x: 99, y: 299, width: 402, height: 2 });
    const rotated = visibleContentBounds([line('rotated', 0, 0, 100, 0, { rotation: 90, strokeWidth: 2 })], canvas)!;
    expect(rotated.x).toBeCloseTo(-1);
    expect(rotated.y).toBeCloseTo(-1);
    expect(rotated.width).toBeCloseTo(2);
    expect(rotated.height).toBeCloseTo(102);
  });

  it('includes an arrow head and ignores non-finite, invisible and zero-stroke shapes', () => {
    const canvas = catalog.application.canvas as unknown as Pick<ProductSchema['canvas'], 'frontend'>;
    const arrow = {
      id: 'arrow', type: 'shape' as const, shapeType: 'arrow' as const, imageUrl: '', taskId: '',
      x: 0, y: 0, width: 100, height: 0, zIndex: 0, strokeWidth: 2,
    };
    expect(visibleContentBounds([arrow], canvas)).toEqual({ x: -12, y: -12, width: 124, height: 24 });
    const image = {
      id: 'image', type: 'image' as const, imageUrl: '/a', taskId: '', x: 1000, y: 1000, width: 100, height: 100, zIndex: 0,
    };
    expect(visibleContentBounds([
      image,
      { ...image, id: 'nan-x', x: Number.NaN },
      { ...image, id: 'nan-y', y: Number.NaN },
      { ...image, id: 'nan-width', width: Number.NaN },
      { ...image, id: 'nan-height', height: Number.POSITIVE_INFINITY },
      { ...image, id: 'nan-rotation', rotation: Number.NaN },
      { ...image, id: 'hidden', visible: false },
      { ...image, id: 'transparent', opacity: 0 },
      { ...arrow, id: 'zero-stroke', strokeWidth: 0 },
    ], canvas)).toEqual({ x: 1000, y: 1000, width: 100, height: 100 });
  });
});
