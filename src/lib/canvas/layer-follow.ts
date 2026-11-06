import type { CanvasNode, CanvasViewport } from '@/store/canvas-store';
import type { CanvasProductSchema } from '@/lib/product-schema';
import { nodeBounds } from './multi-drag';
import { fitRectViewport, type ViewportInsets } from './viewport';

type Context = {
  node: CanvasNode | undefined;
  viewport: CanvasViewport;
  size: { width: number; height: number };
  config: CanvasProductSchema['frontend']['viewport'];
  insets: ViewportInsets;
};

export function requestLayerFollow(sessionId: string, nodeId: string): void {
  window.dispatchEvent(new CustomEvent('canvas:layer-follow', { detail: { sessionId, nodeId } }));
}

export function bindLayerFollow(sessionId: string, getContext: (id: string) => Context | null, onSelect: (view: CanvasViewport | null) => void): () => void {
  const listener = (event: Event) => {
    const detail = (event as CustomEvent<{ sessionId: string; nodeId: string }>).detail;
    if (!detail || detail.sessionId !== sessionId) return;
    const context = getContext(detail.nodeId);
    if (!context?.node) return;
    const { node, viewport, size, config, insets } = context;
    const bounds = nodeBounds(node);
    if (![bounds.x, bounds.y, bounds.width, bounds.height, viewport.zoom, viewport.panX, viewport.panY].every(Number.isFinite)
      || (bounds.width === 0 && bounds.height === 0) || viewport.zoom <= 0 || size.width <= 0 || size.height <= 0) return;
    const rect = { ...bounds, width: Math.max(bounds.width, 1), height: Math.max(bounds.height, 1) };
    const visible = rect.x * viewport.zoom + viewport.panX >= insets.left
      && rect.y * viewport.zoom + viewport.panY >= insets.top
      && (rect.x + rect.width) * viewport.zoom + viewport.panX <= size.width - insets.right
      && (rect.y + rect.height) * viewport.zoom + viewport.panY <= size.height - insets.bottom;
    onSelect(visible ? null : fitRectViewport(rect, size, config, insets));
  };
  window.addEventListener('canvas:layer-follow', listener);
  return () => window.removeEventListener('canvas:layer-follow', listener);
}
