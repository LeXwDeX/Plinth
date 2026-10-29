'use client';

import { useEffect, useLayoutEffect } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { BITMAP_LIMIT_TOAST_ID, bitmapCapacity, bitmapLimitMessage } from '@/lib/canvas/bitmap-limit';
import { configureCanvasBitmapLimit, useCanvasStore } from '@/store/canvas-store';

/**
 * #149: hands the canvas store the Schema's picture limit and tells the person, once per refusal, that the canvas is
 * full and what to do about it (delete old picture layers or start a new canvas). Mounted where the board is.
 */
export function useBitmapLimitNotice(): void {
  const { schema } = useProductSchema();
  const limit = schema.canvas.limits.maximum_bitmap_nodes;
  useLayoutEffect(() => { configureCanvasBitmapLimit(limit); }, [limit]);
  const hit = useCanvasStore((state) => state.bitmapLimitHit);
  useEffect(() => {
    if (!hit) return;
    const capacity = bitmapCapacity(schema, useCanvasStore.getState().getNodes(hit.sessionId));
    toast.error(bitmapLimitMessage(schema, 'reached', capacity), { id: BITMAP_LIMIT_TOAST_ID });
  }, [hit, schema]);
}
