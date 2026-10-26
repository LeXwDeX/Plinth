/** Where a picture dropped on the canvas lands: converted from the pointer to board coordinates. */

export interface DropViewport {
  zoom: number;
  panX: number;
  panY: number;
}

/** Client (viewport) pixels → board coordinates, given the canvas element's rectangle. */
export function dropPointToWorld(
  client: { x: number; y: number },
  rect: { left: number; top: number },
  viewport: DropViewport,
): { x: number; y: number } {
  return {
    x: (client.x - rect.left - viewport.panX) / viewport.zoom,
    y: (client.y - rect.top - viewport.panY) / viewport.zoom,
  };
}

/**
 * The new node's rectangle: centred on the drop point, at its natural size but shrunk to fit
 * inside `limit` (the artboard) when it is larger; never enlarged.
 */
export function dropNodeRect(
  world: { x: number; y: number },
  natural: { width: number; height: number },
  limit?: { width: number; height: number } | null,
): { x: number; y: number; width: number; height: number } {
  let { width, height } = natural;
  if (limit && width > 0 && height > 0) {
    const fit = Math.min(1, limit.width / width, limit.height / height);
    width *= fit;
    height *= fit;
  }
  width = Math.round(width);
  height = Math.round(height);
  return { x: Math.round(world.x - width / 2), y: Math.round(world.y - height / 2), width, height };
}
