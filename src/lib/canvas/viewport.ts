import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasViewport, CanvasNode } from '@/store/canvas-store';
import type { CanvasArtboard } from './artboard';
import { isTransparentBlank } from './initial-layer';
import { nodeBounds, unionBounds } from './multi-drag';

type ViewportConfig = CanvasProductSchema['frontend']['viewport'];
type Point = { x: number; y: number };

export function zoomCanvasAtPoint(current: CanvasViewport, pointer: Point, factor: number, config: ViewportConfig): CanvasViewport {
  if (!Number.isFinite(factor) || factor <= 0) return current;
  const zoom = Math.min(config.maximum_zoom, Math.max(config.minimum_zoom, current.zoom * factor));
  return {
    zoom,
    panX: pointer.x - ((pointer.x - current.panX) / current.zoom) * zoom,
    panY: pointer.y - ((pointer.y - current.panY) / current.zoom) * zoom,
  };
}

export function initialArtboardViewport(
  board: CanvasArtboard,
  size: { width: number; height: number },
  config: ViewportConfig,
  insets: ViewportInsets = NO_INSETS,
): CanvasViewport {
  const clear = centredClearance(insets);
  const zoom = Math.max(config.minimum_zoom, Math.min(
    config.maximum_zoom, config.default_zoom,
    (size.width - (config.fit_padding_pixels + clear.x) * 2) / board.width,
    (size.height - (config.fit_padding_pixels + clear.y) * 2) / board.height,
  ));
  return {
    zoom,
    panX: size.width / 2 - (board.x + board.width / 2) * zoom,
    panY: size.height / 2 - (board.y + board.height / 2) * zoom,
  };
}

export interface WheelGestureHandlers {
  /** Zoom by `factor` around a point in element coordinates. */
  zoom: (pointer: Point, factor: number) => void;
  /** Move the content by this many screen pixels (positive moves it right / down). */
  pan: (dx: number, dy: number) => void;
}

const WHEEL_DELTA_LINE = 1;
const WHEEL_DELTA_PAGE = 2;

/** Wheel deltas in pixels whatever the device reports (pixels, lines or pages). */
function wheelPixels(event: WheelEvent, config: ViewportConfig, pageHeight: number): { x: number; y: number } {
  const unit = event.deltaMode === WHEEL_DELTA_LINE ? config.wheel_line_height_pixels
    : event.deltaMode === WHEEL_DELTA_PAGE ? pageHeight : 1;
  return { x: (event.deltaX || 0) * unit, y: (event.deltaY || 0) * unit };
}

/**
 * What a wheel event does on the canvas (Photoshop's way): Ctrl or Alt plus the wheel zooms around
 * the pointer (a trackpad pinch arrives as Ctrl+wheel), Shift plus the wheel pans sideways, and the plain
 * wheel or a two-finger swipe pans in both directions. Safari's gesture events are handled separately.
 */
export function classifyWheel(
  event: Pick<WheelEvent, 'ctrlKey' | 'altKey' | 'shiftKey' | 'deltaX' | 'deltaY' | 'deltaMode'>,
  config: ViewportConfig,
  pageHeight = 0,
): { kind: 'zoom'; factor: number } | { kind: 'pan'; dx: number; dy: number } | null {
  const delta = wheelPixels(event as WheelEvent, config, pageHeight);
  if (event.ctrlKey || event.altKey) {
    const amount = delta.y || delta.x;
    if (amount === 0) return null;
    return { kind: 'zoom', factor: Math.pow(1 + config.zoom_step, -amount / 100) };
  }
  if (delta.x === 0 && delta.y === 0) return null;
  // Browsers turn Shift+wheel into a sideways delta already; either axis then drives the horizontal pan.
  if (event.shiftKey) {
    const amount = delta.x || delta.y;
    return { kind: 'pan', dx: -amount, dy: 0 };
  }
  return { kind: 'pan', dx: -delta.x, dy: -delta.y };
}

// WebKit emits cumulative gesture scales; Chromium emits ctrl-wheel deltas.
export function bindCanvasZoomGestures(element: HTMLElement, config: ViewportConfig, handlers: WheelGestureHandlers): () => void {
  let gestureScale: number | null = null;
  let touchPinch = false;
  const touch = (event: TouchEvent) => { touchPinch = event.touches.length > 1; };
  const point = (event: { clientX: number; clientY: number }) => {
    const rect = element.getBoundingClientRect();
    return { x: event.clientX - rect.left, y: event.clientY - rect.top };
  };
  const wheel = (event: WheelEvent) => {
    // Scrolling inside the canvas never reaches the page.
    event.preventDefault();
    if (touchPinch || gestureScale !== null) return;
    const action = classifyWheel(event, config, element.clientHeight || 0);
    if (!action) return;
    if (action.kind === 'zoom') handlers.zoom(point(event), action.factor);
    else handlers.pan(action.dx, action.dy);
  };
  const gesture = (event: Event) => {
    event.preventDefault();
    const value = event as Event & { scale: number; clientX: number; clientY: number };
    if (event.type === 'gestureend') {
      gestureScale = null;
      return;
    }
    if (!Number.isFinite(value.scale) || value.scale <= 0) return;
    if (event.type === 'gesturestart') {
      gestureScale = value.scale;
    } else if (gestureScale !== null) {
      // Touchscreen pinch is already handled by the Stage touch pipeline.
      if (!touchPinch) handlers.zoom(point(value), value.scale / gestureScale);
      gestureScale = value.scale;
    }
  };
  element.addEventListener('wheel', wheel, { passive: false });
  for (const type of ['touchstart', 'touchend', 'touchcancel'] as const) {
    element.addEventListener(type, touch, { capture: true, passive: true });
  }
  for (const type of ['gesturestart', 'gesturechange', 'gestureend']) {
    element.addEventListener(type, gesture, { passive: false });
  }
  return () => {
    element.removeEventListener('wheel', wheel);
    for (const type of ['touchstart', 'touchend', 'touchcancel'] as const) element.removeEventListener(type, touch, true);
    for (const type of ['gesturestart', 'gesturechange', 'gestureend']) element.removeEventListener(type, gesture);
  };
}

export type ViewportInsets = { left: number; top: number; right: number; bottom: number };
export type FitRect = { x: number; y: number; width: number; height: number };

const NO_INSETS: ViewportInsets = { left: 0, top: 0, right: 0, bottom: 0 };

/**
 * The clearance a picture centred on the drawing area keeps on each side so no floating control covers it:
 * the wider of the two opposite strips, on both sides. The dashed frame stays centred on the whole drawing
 * area (#282) and still clears the tool rail, the option bar and the zoom bar (#246).
 */
function centredClearance(insets: ViewportInsets): { x: number; y: number } {
  return { x: Math.max(insets.left, insets.right, 0), y: Math.max(insets.top, insets.bottom, 0) };
}

/**
 * Fit a rectangle into the visible part of the drawing area and centre it there.
 * `insets` is the strip covered by floating controls (tool rail, option bar, zoom
 * bar); `config.fit_padding_pixels` is the breathing room around the picture.
 * A picture smaller than the area is never enlarged beyond the Schema's default zoom.
 */
export function fitRectViewport(
  rect: FitRect,
  size: { width: number; height: number },
  config: ViewportConfig,
  insets: ViewportInsets = { left: 0, top: 0, right: 0, bottom: 0 },
): CanvasViewport | null {
  if (rect.width <= 0 || rect.height <= 0 || size.width <= 0 || size.height <= 0) return null;
  const pad = config.fit_padding_pixels;
  const availableWidth = size.width - insets.left - insets.right - pad * 2;
  const availableHeight = size.height - insets.top - insets.bottom - pad * 2;
  if (availableWidth <= 0 || availableHeight <= 0) return null;
  const zoom = Math.max(config.minimum_zoom, Math.min(
    config.maximum_zoom, config.default_zoom,
    availableWidth / rect.width,
    availableHeight / rect.height,
  ));
  const centerX = insets.left + (size.width - insets.left - insets.right) / 2;
  const centerY = insets.top + (size.height - insets.top - insets.bottom) / 2;
  return {
    zoom,
    panX: centerX - (rect.x + rect.width / 2) * zoom,
    panY: centerY - (rect.y + rect.height / 2) * zoom,
  };
}

/** True when the stored viewport is still the one the auto-fit applied (the user has not moved it). */
export function viewportUnchanged(current: CanvasViewport | undefined, applied: CanvasViewport | null): boolean {
  if (!current || !applied) return false;
  return Math.abs(current.zoom - applied.zoom) < 1e-6
    && Math.abs(current.panX - applied.panX) < 0.5
    && Math.abs(current.panY - applied.panY) < 0.5;
}

/**
 * "Back to the canvas": centre the dashed artboard in the visible area. The zoom stays
 * unless the artboard no longer fits inside the padding; then it shrinks to fit.
 */
export function recenterArtboardViewport(
  board: CanvasArtboard,
  size: { width: number; height: number },
  current: CanvasViewport,
  config: ViewportConfig,
  insets: ViewportInsets = NO_INSETS,
): CanvasViewport | null {
  if (board.width <= 0 || board.height <= 0 || size.width <= 0 || size.height <= 0) return null;
  const clear = centredClearance(insets);
  const availableWidth = size.width - (config.fit_padding_pixels + clear.x) * 2;
  const availableHeight = size.height - (config.fit_padding_pixels + clear.y) * 2;
  if (availableWidth <= 0 || availableHeight <= 0) return null;
  const fitZoom = Math.min(availableWidth / board.width, availableHeight / board.height);
  const zoom = current.zoom <= fitZoom ? current.zoom : Math.max(config.minimum_zoom, fitZoom);
  return {
    zoom,
    panX: size.width / 2 - (board.x + board.width / 2) * zoom,
    panY: size.height / 2 - (board.y + board.height / 2) * zoom,
  };
}

/**
 * The one "fit" rule: the rectangle fills the drawing area inside the Schema's padding, centred, at
 * whatever zoom that takes within the Schema's zoom range (a small frame is enlarged, a big one shrunk).
 * The toolbar's fit button, Ctrl+0 and "zoom to selection" all come through here. With `insets` the
 * rectangle also clears the floating controls on every side (#246) while staying centred (#282).
 */
export function fitViewportToRect(
  rect: FitRect,
  size: { width: number; height: number },
  config: ViewportConfig,
  insets: ViewportInsets = NO_INSETS,
): CanvasViewport | null {
  if (rect.width <= 0 || rect.height <= 0 || size.width <= 0 || size.height <= 0) return null;
  const clear = centredClearance(insets);
  const availableWidth = size.width - (config.fit_padding_pixels + clear.x) * 2;
  const availableHeight = size.height - (config.fit_padding_pixels + clear.y) * 2;
  if (availableWidth <= 0 || availableHeight <= 0) return null;
  const zoom = Math.max(config.minimum_zoom, Math.min(config.maximum_zoom, availableWidth / rect.width, availableHeight / rect.height));
  return {
    zoom,
    panX: size.width / 2 - (rect.x + rect.width / 2) * zoom,
    panY: size.height / 2 - (rect.y + rect.height / 2) * zoom,
  };
}

/**
 * The union of the layers that draw something, for "back to the canvas"; null when none do.
 * Vector lines and arrows can be one axis thin (a horizontal line has no height), so their visible
 * stroke and arrow head extend the geometric box; the stroke default and arrow length come from the
 * same canvas Schema the board and exporter use.
 */
export function visibleContentBounds(nodes: readonly CanvasNode[], canvas: Pick<CanvasProductSchema, 'frontend'>): FitRect | null {
  const transparentValue = canvas.frontend.export.transparent_background_value;
  const defaultStroke = canvas.frontend.style.default_stroke_width;
  const arrowHead = canvas.frontend.export.arrow.head_length;
  const bounds: FitRect[] = [];
  for (const node of nodes) {
    if (node.visible === false || node.type === 'frame') continue;
    if (typeof node.opacity === 'number' && (!Number.isFinite(node.opacity) || node.opacity <= 0)) continue;
    if (![node.x, node.y, node.width, node.height].every(Number.isFinite)) continue;
    const rotation = node.rotation ?? 0;
    if (!Number.isFinite(rotation)) continue;
    if (node.width === 0 && node.height === 0) continue;
    if (isTransparentBlank(node, transparentValue)) continue;
    if (node.type === 'image' && !node.blank && !node.imageUrl) continue;
    const box = nodeBounds({ ...node, rotation });
    if (![box.x, box.y, box.width, box.height].every(Number.isFinite)) continue;
    let pad = 0;
    if (node.type === 'shape' && (node.shapeType === 'line' || node.shapeType === 'arrow')) {
      const stroke = typeof node.strokeWidth === 'number' && Number.isFinite(node.strokeWidth) && node.strokeWidth >= 0
        ? node.strokeWidth
        : defaultStroke;
      if (stroke <= 0) continue;
      pad = stroke / 2;
      if (node.shapeType === 'arrow') pad = Math.max(pad, arrowHead + stroke);
    }
    const padded = { x: box.x - pad, y: box.y - pad, width: box.width + pad * 2, height: box.height + pad * 2 };
    if (![padded.x, padded.y, padded.width, padded.height].every(Number.isFinite) || padded.width <= 0 || padded.height <= 0) continue;
    bounds.push(padded);
  }
  if (bounds.length === 0) return null;
  const union = unionBounds(bounds);
  return union.width > 0 && union.height > 0 ? { x: union.x, y: union.y, width: union.width, height: union.height } : null;
}

/**
 * "Back to the canvas": show all visible content, or the default artboard view when there is none.
 * The content path is the one fit rule (padding, insets, zoom limits); the empty path is the view a
 * canvas starts with. Same input gives the same viewport, so repeated clicks are stable.
 */
export function fitContentOrArtboardViewport(
  content: FitRect | null,
  board: CanvasArtboard,
  size: { width: number; height: number },
  config: ViewportConfig,
  insets: ViewportInsets = NO_INSETS,
): CanvasViewport | null {
  return (content ? fitViewportToRect(content, size, config, insets) : null)
    ?? (board.width > 0 && board.height > 0 ? initialArtboardViewport(board, size, config, insets) : null);
}

/** Fit the dashed artboard (V2: the fit button and Ctrl+0). */
export function fitArtboardViewport(
  board: CanvasArtboard,
  size: { width: number; height: number },
  config: ViewportConfig,
  insets: ViewportInsets = NO_INSETS,
): CanvasViewport | null {
  return fitViewportToRect(board, size, config, insets);
}

/** Set the zoom and keep the point at the middle of the drawing area where it is. */
export function zoomAroundCenter(
  current: CanvasViewport,
  size: { width: number; height: number },
  zoom: number,
  config: ViewportConfig,
): CanvasViewport {
  return zoomCanvasAtPoint(current, { x: size.width / 2, y: size.height / 2 }, zoom / current.zoom, config);
}

/** Ctrl+= / Ctrl+-: one Schema zoom step in or out, around the middle of the drawing area. */
export function zoomStepViewport(
  current: CanvasViewport,
  size: { width: number; height: number },
  direction: 1 | -1,
  config: ViewportConfig,
): CanvasViewport {
  const factor = 10 ** config.decimal_places;
  const stepped = Math.round((current.zoom + direction * config.zoom_step) * factor) / factor;
  return zoomAroundCenter(current, size, Math.min(config.maximum_zoom, Math.max(config.minimum_zoom, stepped)), config);
}

/** Within this many screen pixels of the middle of the drawing area the frame counts as centred (#282). */
const CENTRED_TOLERANCE_PIXELS = 1;

/** Whether the dashed frame's centre is at the middle of the drawing area (#282). */
export function artboardCentred(board: CanvasArtboard, size: { width: number; height: number }, current: CanvasViewport): boolean {
  const screenX = (board.x + board.width / 2) * current.zoom + current.panX;
  const screenY = (board.y + board.height / 2) * current.zoom + current.panY;
  return Math.abs(screenX - size.width / 2) <= CENTRED_TOLERANCE_PIXELS
    && Math.abs(screenY - size.height / 2) <= CENTRED_TOLERANCE_PIXELS;
}

/**
 * #282: zooming (wheel, pinch, the zoom tool) keeps a centred frame centred: it zooms around the middle of
 * the drawing area, so the frame's centre stays put before, during and after the zoom. Once the person has
 * panned the frame off-centre, the zoom follows the pointer as before (Photoshop's way); 适应屏幕 and
 * 回到画布 centre it again.
 */
export function zoomKeepingFrame(
  current: CanvasViewport,
  pointer: Point,
  factor: number,
  config: ViewportConfig,
  board: CanvasArtboard | null | undefined,
  size: { width: number; height: number },
): CanvasViewport {
  const anchor = board && size.width > 0 && size.height > 0 && artboardCentred(board, size, current)
    ? { x: size.width / 2, y: size.height / 2 }
    : pointer;
  return zoomCanvasAtPoint(current, anchor, factor, config);
}

/**
 * #282: when the drawing area changes size (window, chat column, panels), a centred frame stays centred: the
 * view moves by half the change. A frame the person panned elsewhere stays where it is on screen.
 */
export function viewportAfterResize(
  current: CanvasViewport,
  before: { width: number; height: number },
  after: { width: number; height: number },
  board: CanvasArtboard | null | undefined,
): CanvasViewport | null {
  if (!board || before.width <= 0 || before.height <= 0 || !artboardCentred(board, before, current)) return null;
  if (before.width === after.width && before.height === after.height) return null;
  return { ...current, panX: current.panX + (after.width - before.width) / 2, panY: current.panY + (after.height - before.height) / 2 };
}
