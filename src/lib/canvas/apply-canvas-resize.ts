import { toast } from 'sonner';

import { schemaCountMessage, type ProductSchema } from '@/lib/product-schema';
import { useCanvasStore, type CanvasSize } from '@/store/canvas-store';
import { isResizeAnchor, type ResizeAnchor } from './canvas-resize';
import { localBitmapNodeIds } from './pending-edits';

/** The anchor a resize keeps content at: the viewer's pick when the Schema offers it, else the Schema default. */
export function resizeAnchorFor(
  artboard: ProductSchema['canvas']['frontend']['artboard'],
  override: ResizeAnchor | null,
): ResizeAnchor {
  if (override && artboard.resize.anchors.includes(override)) return override;
  return isResizeAnchor(artboard.resize.default_anchor) ? artboard.resize.default_anchor : 'center';
}

/**
 * Every resize the person asks for (a size picked in the 参数 panel, adopting the model's output) goes
 * through here (#246): content keeps its pixel size and its place relative to the anchor, and when a shrink
 * leaves layers partly outside the canvas the person is told that they are kept and how to get them back.
 */
export function applyCanvasResize(schema: ProductSchema, sessionId: string, size: CanvasSize): void {
  const store = useCanvasStore.getState();
  const artboard = schema.canvas.frontend.artboard;
  const clipped = store.resizeCanvas(sessionId, size, {
    anchor: resizeAnchorFor(artboard, store.resizeAnchorOverride),
    center: { x: artboard.center_x, y: artboard.center_y },
    // A stroke not uploaded yet (idle wait, upload in flight or failed) keeps its layer as content.
    painted: localBitmapNodeIds(sessionId),
  });
  if (clipped.length > 0) toast.info(schemaCountMessage(schema, 'ui.canvas.size.clipped', clipped.length, { count: clipped.length }));
}
