/**
 * #246: what the canvas area shows under every layer, so its pixel extent and its background are visible.
 * The board exports the frame over the Schema's artboard background (#271); while that is transparent the
 * frame shows a checkerboard (what will export as alpha 0), otherwise it is filled with that colour.
 */
import type { CanvasProductSchema } from '@/lib/product-schema';

export type ArtboardBackdrop =
  | { kind: 'checker'; cell: number; light: string; dark: string; opacity: number }
  | { kind: 'fill'; color: string };

export function artboardBackdrop(canvas: Pick<CanvasProductSchema, 'frontend'>): ArtboardBackdrop {
  const exporting = canvas.frontend.export;
  const background = exporting.artboard_background_color;
  if (!background || background === exporting.transparent_background_value) {
    const pattern = canvas.frontend.artboard.transparency;
    return { kind: 'checker', cell: pattern.cell_pixels, light: pattern.light_color, dark: pattern.dark_color, opacity: pattern.opacity };
  }
  return { kind: 'fill', color: background };
}

/** One 2x2-cell tile of the checkerboard (repeated as a fill pattern at screen scale). */
export function checkerTile(backdrop: Extract<ArtboardBackdrop, { kind: 'checker' }>): HTMLCanvasElement | null {
  if (typeof document === 'undefined') return null;
  const cell = Math.max(1, Math.round(backdrop.cell));
  const tile = document.createElement('canvas');
  tile.width = cell * 2;
  tile.height = cell * 2;
  const context = tile.getContext('2d');
  if (!context) return null;
  context.fillStyle = backdrop.light;
  context.fillRect(0, 0, cell * 2, cell * 2);
  context.fillStyle = backdrop.dark;
  context.fillRect(cell, 0, cell, cell);
  context.fillRect(0, cell, cell, cell);
  return tile;
}
