'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { ChoiceChips } from '@/components/ui/choice-chips';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';
import { applyCanvasResize } from '@/lib/canvas/apply-canvas-resize';
import { samePixels } from '@/lib/canvas/artboard';
import { schemaText } from '@/lib/product-schema';
import { canvasPixelsForRatio, canvasRatioOptions } from '@/lib/workspace/canvas-size';

/**
 * #653: the canvas is a ratio, picked from the Schema's common ratios (artboard.ratio_pixels); its pixels are only
 * editing precision and are neither shown nor picked. A pick resizes the canvas around the chosen anchor as one undo
 * step (applyCanvasResize), keeping its long edge. A canvas of an uncommon ratio (made before #653) checks no chip.
 */
export function CanvasRatioPicker({ sessionId, delivery, disabled = false }: { sessionId: string; delivery: CanvasDelivery; disabled?: boolean }) {
  const { schema } = useProductSchema();
  const artboard = schema.canvas.frontend.artboard;
  const label = schemaText(schema, 'ui.canvas.size.ratio_label');
  const options = canvasRatioOptions(artboard).map((value) => ({ value, label: value }));
  const choose = (ratio: string) => {
    const pixels = canvasPixelsForRatio(ratio, artboard, schema.canvas.limits);
    if (pixels && !samePixels(pixels, delivery.pixels)) applyCanvasResize(schema, sessionId, pixels);
  };
  return (
    <div className="flex flex-col gap-1.5" data-testid="canvas-ratio-picker" data-ratio={delivery.ratio.label}>
      <span className="text-xs font-medium">{label}</span>
      <ChoiceChips label={label} options={options} current={delivery.ratio.label} disabled={disabled || !delivery.known} onChange={choose} />
      <p className="text-[11px] leading-4 text-muted-foreground">{schemaText(schema, 'ui.canvas.size.ratio_hint')}</p>
    </div>
  );
}
