/**
 * The dashed frame as the board last published it (its rectangle and the pixel size the chosen model
 * takes), and the one function that turns it into a picture. The board component publishes; actions that
 * live elsewhere (the result detail's 转 3D, the board's own 发送到 3D) read it at click time, so none of
 * them keeps a second copy of the frame geometry or a rasteriser of its own.
 */
import type { ProductSchema } from '@/lib/product-schema';
import { useCanvasStore } from '@/store/canvas-store';
import type { CanvasArtboard } from './artboard';
import { exportArtboardToBlob } from './export-canvas';
import { flushPendingEdits } from './pending-edits';

export interface BoardDelivery {
  artboard: CanvasArtboard;
  /** The canvas pixel size (#246); null only for a delivery published without one. */
  pixels: { width: number; height: number } | null;
}

const deliveries = new Map<string, BoardDelivery>();

/** Called by the mounted board; `null` when it unmounts or has no frame. */
export function publishBoardDelivery(sessionId: string, delivery: BoardDelivery | null): void {
  if (delivery) deliveries.set(sessionId, delivery);
  else deliveries.delete(sessionId);
}

export function boardDeliveryOf(sessionId: string): BoardDelivery | null {
  return deliveries.get(sessionId) ?? null;
}

/**
 * Everything visible inside the frame, rasterised at the delivery size (exportArtboardToBlob). Brush
 * strokes still on their way to the server are part of the picture, so they are flushed first.
 */
export async function rasteriseSessionBoard(schema: ProductSchema, sessionId: string, delivery: BoardDelivery | null = boardDeliveryOf(sessionId)): Promise<Blob> {
  if (!delivery) throw new Error('canvas artboard unavailable');
  await flushPendingEdits(sessionId);
  return exportArtboardToBlob(useCanvasStore.getState().getNodes(sessionId), schema.canvas, delivery.artboard, delivery.pixels);
}

/**
 * The picture handed to an image-to-3D Offering: the canvas pixels (#246: the frame is the canvas pixel
 * size). A delivery published without pixels falls back to the Schema's to_3d_long_edge_pixels on the long
 * edge, keeping the frame's ratio.
 */
export function boardPixelsFor3D(schema: ProductSchema, delivery: BoardDelivery): { width: number; height: number } {
  if (delivery.pixels) return delivery.pixels;
  const longEdge = schema.canvas.frontend.to_3d_long_edge_pixels;
  const scale = longEdge / Math.max(delivery.artboard.width, delivery.artboard.height);
  return {
    width: Math.max(1, Math.round(delivery.artboard.width * scale)),
    height: Math.max(1, Math.round(delivery.artboard.height * scale)),
  };
}
