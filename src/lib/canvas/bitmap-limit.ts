import { schemaMessage, type ProductSchema } from '@/lib/product-schema';

/**
 * #149: a canvas holds a limited number of picture layers (`canvas.limits.maximum_bitmap_nodes`). The server
 * counts every picture layer, hidden ones too, and refuses a save above the limit. This is the browser's side
 * of the same rule, read from the same Schema field, so each entry that adds a picture can say so before the
 * server has to.
 */
/** One toast id, so a burst of refused adds (a result list, repeated paste, a screenshot) shows a single notice. */
export const BITMAP_LIMIT_TOAST_ID = 'canvas-bitmap-limit';

export interface BitmapCapacity {
  used: number;
  limit: number;
  remaining: number;
  /** No further picture fits. */
  full: boolean;
  /** Still room, but no more than `canvas.limits.bitmap_warning_remaining` slots. */
  near: boolean;
}

export function bitmapCapacity(schema: ProductSchema, nodes: readonly { type: string }[] | undefined): BitmapCapacity {
  const { maximum_bitmap_nodes: limit, bitmap_warning_remaining: warning } = schema.canvas.limits;
  const used = (nodes ?? []).filter((node) => node.type === 'image').length;
  const remaining = Math.max(0, limit - used);
  return { used, limit, remaining, full: remaining === 0, near: remaining > 0 && remaining <= warning };
}

/** The words for a canvas at (`reached`) or close to (`near`) its picture limit; both name the limit and what to do. */
export function bitmapLimitMessage(schema: ProductSchema, kind: 'reached' | 'near', capacity: BitmapCapacity): string {
  return schemaMessage(schema, `ui.workspace.canvas.bitmap_limit_${kind}`, {
    used: capacity.used, limit: capacity.limit, remaining: capacity.remaining,
  });
}

/** A screenshot (or other add) was refused because the canvas already holds its limit of pictures. */
export class CanvasBitmapLimitError extends Error {
  readonly used: number;
  readonly limit: number;

  constructor(used: number, limit: number) {
    super('canvas picture limit reached');
    this.name = 'CanvasBitmapLimitError';
    this.used = used;
    this.limit = limit;
  }
}
