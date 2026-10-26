/** Large view: items, stepping, and zoom and pan arithmetic. */

export interface LightboxItem {
  key: string;
  /** Display URL, already through the platform asset route. */
  url: string;
  label: string;
  /** What the "download" link points at; empty when there is none. */
  downloadUrl?: string;
  /** Set when this picture is one candidate of a result: the large view then offers "选用". */
  pick?: { taskId: string; assetId: string };
}

export interface LightboxState {
  items: LightboxItem[];
  index: number;
}

export const ZOOM_MINIMUM = 1;
export const ZOOM_MAXIMUM = 8;
export const ZOOM_STEP = 1.25;

export function stepIndex(index: number, delta: number, length: number): number {
  if (length <= 0) return 0;
  return ((index + delta) % length + length) % length;
}

export function clampZoom(zoom: number): number {
  if (!Number.isFinite(zoom)) return ZOOM_MINIMUM;
  return Math.min(ZOOM_MAXIMUM, Math.max(ZOOM_MINIMUM, zoom));
}

export interface View { zoom: number; x: number; y: number }

/**
 * Zoom around a point of the window (pointer coordinates relative to the centre
 * of the image box): the picture point under the pointer stays under it.
 */
export function zoomAt(view: View, factor: number, pointer: { x: number; y: number }): View {
  const zoom = clampZoom(view.zoom * factor);
  if (zoom === view.zoom) return view;
  const ratio = zoom / view.zoom;
  const next = {
    zoom,
    x: pointer.x - (pointer.x - view.x) * ratio,
    y: pointer.y - (pointer.y - view.y) * ratio,
  };
  return zoom === ZOOM_MINIMUM ? { zoom, x: 0, y: 0 } : next;
}

/** The picture may be dragged until its edge reaches the middle of the window, no further. */
export function clampPan(view: View, box: { width: number; height: number }): View {
  const limitX = (box.width * view.zoom) / 2;
  const limitY = (box.height * view.zoom) / 2;
  return {
    ...view,
    x: Math.min(limitX, Math.max(-limitX, view.x)),
    y: Math.min(limitY, Math.max(-limitY, view.y)),
  };
}
