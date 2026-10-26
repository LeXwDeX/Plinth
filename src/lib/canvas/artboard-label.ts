import type { CanvasArtboard } from './artboard';

export interface ArtboardLabelStyle {
  font_size_pixels: number;
  offset_pixels: number;
  padding_pixels: number;
}

/** The label's plate and text in world units: screen-sized at any zoom, inset in the frame's top-left corner (#282). */
export interface ArtboardLabelBox {
  plate: { x: number; y: number; width: number; height: number };
  text: { x: number; y: number; scale: number };
}

export function artboardLabelBox(
  board: CanvasArtboard,
  zoom: number,
  textWidthPixels: number,
  style: ArtboardLabelStyle,
): ArtboardLabelBox {
  const unit = 1 / zoom;
  const x = board.x + style.offset_pixels * unit;
  const y = board.y + style.offset_pixels * unit;
  return {
    plate: {
      x,
      y,
      width: (textWidthPixels + style.padding_pixels * 2) * unit,
      height: (style.font_size_pixels + style.padding_pixels * 2) * unit,
    },
    text: { x: x + style.padding_pixels * unit, y: y + style.padding_pixels * unit, scale: unit },
  };
}

let measuring: CanvasRenderingContext2D | null | undefined;

/** Screen width of the label in Konva's default font, which the label is drawn in. */
export function labelTextWidth(text: string, fontSizePixels: number): number {
  if (measuring === undefined) {
    measuring = typeof document === 'undefined' ? null : document.createElement('canvas').getContext('2d');
  }
  if (!measuring) return text.length * fontSizePixels;
  measuring.font = `${fontSizePixels}px Arial`;
  return measuring.measureText(text).width;
}
