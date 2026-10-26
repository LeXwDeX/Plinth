import { useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';
import { useImageSize } from '@/hooks/use-image-size';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { referenceTurnOutput } from '@/lib/workspace/canvas-size';

/**
 * #296: the canvas delivery with the size an edit of the composer's one picture sends. It applies when the 2D stage
 * is in use, the artboard is not the turn's source and the composer carries exactly one picture (an upload or the
 * pinned board picture), like the server, which follows a single reference only outside a canvas turn. The picture
 * is loaded once to read its real pixels; until then, and otherwise, `reference` is null.
 */
export function useReferenceSize(delivery: CanvasDelivery, pictures: readonly string[], image: boolean): CanvasDelivery {
  const { schema } = useProductSchema();
  const single = image && !delivery.source && pictures.length === 1 ? safePreviewURL(pictures[0]) : '';
  const pixels = useImageSize(single);
  const { offering, values, parameterStates } = delivery.generation;
  const tables = schema.canvas.frontend.artboard;
  return useMemo(() => {
    const turn = single && pixels ? referenceTurnOutput(offering, values, parameterStates, pixels, tables) : null;
    return { ...delivery, reference: turn && pixels ? { pixels, turn } : null };
  }, [delivery, offering, parameterStates, pixels, single, tables, values]);
}
