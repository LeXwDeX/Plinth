import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { stageFitInsets } from '@/lib/workspace/tool-rail-layout';
import { canvasArtboard } from './artboard';
import {
  artboardCentred, fitArtboardViewport, fitViewportToRect, initialArtboardViewport, recenterArtboardViewport,
} from './viewport';

const config = catalog.application.canvas.frontend.viewport;
const wide = canvasArtboard(catalog.application.canvas.frontend.artboard, { width: 3840, height: 2160 });
const square = canvasArtboard(catalog.application.canvas.frontend.artboard, { width: 2048, height: 2048 });
const size = { width: 780, height: 696 };

/** The frame on screen for a viewport. */
const onScreen = (board: { x: number; y: number; width: number; height: number }, view: { zoom: number; panX: number; panY: number }) => ({
  left: board.x * view.zoom + view.panX,
  top: board.y * view.zoom + view.panY,
  right: (board.x + board.width) * view.zoom + view.panX,
  bottom: (board.y + board.height) * view.zoom + view.panY,
});

describe('fitting the dashed frame clears the floating controls and stays centred (#246, #282)', () => {
  for (const railExpanded of [false, true]) {
    const insets = stageFitInsets(railExpanded);
    it(`适应屏幕 / Ctrl+0 with the rail ${railExpanded ? 'expanded' : 'collapsed'}`, () => {
      for (const board of [wide, square]) {
        const view = fitArtboardViewport(board, size, config, insets)!;
        const frame = onScreen(board, view);
        expect(frame.left).toBeGreaterThanOrEqual(insets.left - 1e-9);
        expect(frame.right).toBeLessThanOrEqual(size.width - insets.left + 1e-9);
        expect(frame.top).toBeGreaterThanOrEqual(insets.top - 1e-9);
        expect(frame.bottom).toBeLessThanOrEqual(size.height - insets.bottom + 1e-9);
        expect(artboardCentred(board, size, view)).toBe(true);
      }
    });
  }

  it('the 16:9 frame no longer slides under the rail, as it did without insets', () => {
    const insets = stageFitInsets(false);
    const before = onScreen(wide, fitArtboardViewport(wide, size, config)!);
    expect(before.left).toBeLessThan(insets.left);
    const after = onScreen(wide, fitArtboardViewport(wide, size, config, insets)!);
    expect(after.left).toBeGreaterThanOrEqual(insets.left + config.fit_padding_pixels - 1e-9);
  });

  it('opening, 回到画布 and zoom to selection use the same clearance', () => {
    const insets = stageFitInsets(true);
    const opened = initialArtboardViewport(wide, size, config, insets);
    expect(onScreen(wide, opened).left).toBeGreaterThanOrEqual(insets.left - 1e-9);
    expect(artboardCentred(wide, size, opened)).toBe(true);
    const zoomedIn = { zoom: 1, panX: 0, panY: 0 };
    const recentred = recenterArtboardViewport(wide, size, zoomedIn, config, insets)!;
    expect(onScreen(wide, recentred).left).toBeGreaterThanOrEqual(insets.left - 1e-9);
    expect(artboardCentred(wide, size, recentred)).toBe(true);
    const selection = fitViewportToRect({ x: 0, y: 0, width: 1600, height: 400 }, size, config, insets)!;
    expect(onScreen({ x: 0, y: 0, width: 1600, height: 400 }, selection).left).toBeGreaterThanOrEqual(insets.left - 1e-9);
  });

  it('without insets every fit behaves as before', () => {
    expect(fitArtboardViewport(wide, size, config)).toEqual(fitViewportToRect(wide, size, config));
    expect(initialArtboardViewport(square, size, config)).toEqual(initialArtboardViewport(square, size, config, { left: 0, top: 0, right: 0, bottom: 0 }));
  });
});
