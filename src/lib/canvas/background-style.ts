import type { ProductSchema } from '@/lib/product-schema';

export type CanvasBackground = ProductSchema['canvas']['frontend']['backgrounds'][number];

/**
 * The CSS that paints a Schema canvas background: its colour, and its dots or grid lines every `cellSize` pixels.
 * The board uses the Schema cell size; the background menu draws the same pattern smaller, so every choice in the
 * menu looks like what it puts behind the canvas (#86: four plain dark squares could not be told apart).
 */
export function canvasBackgroundStyle(background: CanvasBackground, cellSize = background.cell_size): Record<string, string> {
  const style: Record<string, string> = { backgroundColor: background.background_color };
  if (background.pattern === 'dots') {
    style.backgroundImage = `radial-gradient(circle, ${background.pattern_color} ${background.pattern_width}px, transparent ${background.pattern_width}px)`;
    style.backgroundSize = `${cellSize}px ${cellSize}px`;
  }
  if (background.pattern === 'grid') {
    style.backgroundImage = `linear-gradient(${background.pattern_color} 1px, transparent 1px), linear-gradient(90deg, ${background.pattern_color} 1px, transparent 1px)`;
    style.backgroundSize = `${cellSize}px ${cellSize}px`;
  }
  return style;
}
