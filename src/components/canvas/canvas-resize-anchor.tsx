'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { resizeAnchorFor } from '@/lib/canvas/apply-canvas-resize';
import { RESIZE_ANCHORS, type ResizeAnchor } from '@/lib/canvas/canvas-resize';
import { schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import { useCanvasStore } from '@/store/canvas-store';

/**
 * #246: the 3x3 anchor of a canvas resize (Photoshop's canvas-size anchor). Content keeps its pixel size and
 * its place relative to the chosen anchor; the Schema declares which anchors are offered and the default.
 */
export function CanvasResizeAnchor({ disabled = false }: { disabled?: boolean }) {
  const { schema } = useProductSchema();
  const override = useCanvasStore((state) => state.resizeAnchorOverride);
  const setAnchor = useCanvasStore((state) => state.setResizeAnchor);
  const artboard = schema.canvas.frontend.artboard;
  const current = resizeAnchorFor(artboard, override);
  const label = schemaText(schema, 'ui.canvas.size.anchor_label');
  return (
    <div className="flex items-center gap-2" data-testid="canvas-resize-anchor" data-anchor={current}>
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <div role="radiogroup" aria-label={label} className="grid grid-cols-3 gap-0.5">
        {RESIZE_ANCHORS.map((anchor: ResizeAnchor) => {
          const offered = artboard.resize.anchors.includes(anchor);
          const name = schemaText(schema, `ui.canvas.size.anchor.${anchor}`);
          return (
            <HintTooltip key={anchor} name={name}>
              <button
                type="button"
                role="radio"
                aria-checked={anchor === current}
                aria-label={name}
                disabled={disabled || !offered}
                data-anchor={anchor}
                onClick={() => setAnchor(anchor)}
                className={cn(
                  'block size-3.5 rounded-sm border border-border focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-30',
                  anchor === current ? 'bg-primary border-primary' : 'bg-muted hover:bg-muted-foreground/30',
                )}
              />
            </HintTooltip>
          );
        })}
      </div>
    </div>
  );
}
