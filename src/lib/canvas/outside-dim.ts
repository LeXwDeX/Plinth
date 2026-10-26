/** X5: the veil outside the dashed frame, as up to four rectangles covering the visible world area minus the frame. */
export interface DimRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export function outsideDimRects(visible: DimRect, frame: DimRect): DimRect[] {
  // Only the part of the frame inside the visible area matters.
  const left = Math.max(frame.x, visible.x);
  const top = Math.max(frame.y, visible.y);
  const right = Math.min(frame.x + frame.width, visible.x + visible.width);
  const bottom = Math.min(frame.y + frame.height, visible.y + visible.height);
  if (right <= left || bottom <= top) return [{ ...visible }];
  const rects: DimRect[] = [
    // Above and below span the whole visible width; left and right fill the band between them.
    { x: visible.x, y: visible.y, width: visible.width, height: top - visible.y },
    { x: visible.x, y: bottom, width: visible.width, height: visible.y + visible.height - bottom },
    { x: visible.x, y: top, width: left - visible.x, height: bottom - top },
    { x: right, y: top, width: visible.x + visible.width - right, height: bottom - top },
  ];
  return rects.filter((rect) => rect.width > 0 && rect.height > 0);
}

/** The world rectangle the drawing area shows, grown by `margin` world units. */
export function visibleWorld(viewport: { zoom: number; panX: number; panY: number }, size: { width: number; height: number }, margin = 0): DimRect {
  return {
    x: -viewport.panX / viewport.zoom - margin,
    y: -viewport.panY / viewport.zoom - margin,
    width: size.width / viewport.zoom + margin * 2,
    height: size.height / viewport.zoom + margin * 2,
  };
}
