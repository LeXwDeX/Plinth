'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';
import { samePixels, type CanvasPixels } from '@/lib/canvas/artboard';
import { schemaMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { canvasPixelsForRatio, type CanvasTurnOutput } from '@/lib/workspace/canvas-size';
import { cn } from '@/lib/utils';
import { applyCanvasResize } from '@/lib/canvas/apply-canvas-resize';
import { unavailableLabel } from '@/lib/unavailable';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';

const NOTICE_CLASS = 'flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-reminder/40 bg-reminder/10 px-2 py-1 text-[11px] leading-4 text-foreground';

function AdoptButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      data-testid="canvas-size-adopt"
      className="shrink-0 rounded text-primary underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-ring"
      onClick={onClick}
    >
      {label}
    </button>
  );
}

/** The "model has no size of the canvas ratio" line: icon and short reason inline, the full sentence in the hint (#94). */
function SizeUnavailable({ schema, detail }: { schema: ProductSchema; detail: string }) {
  return <UnavailableNotice testId="canvas-size-unavailable" label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.canvas.size_unsupported'))} detail={detail} />;
}

/** The size a canvas turn sends, as the person reads it: pixels, or a ratio with the pixels the Schema maps it to. */
function turnOutputText(schema: ProductSchema, turn: CanvasTurnOutput, sizeText: (size: CanvasPixels) => string): string {
  if (!turn.pixels) return turn.value;
  if (!turn.ratioOnly) return sizeText(turn.pixels);
  return schemaMessage(schema, 'ui.canvas.size.ratio_output', { ratio: turn.value, pixels: sizeText(turn.pixels) });
}

/**
 * #296: the composer carries one picture and the artboard is not the turn's source, so an edit of that picture
 * outputs the legal size closest to it (the server's reference_size rule), not the default the 参数 panel shows.
 * Shown only when that size differs from the default; a size the person or the Agent chose wins and is not
 * mentioned (delivery.reference is then null). Nothing is offered to click: choosing a size in the panel is the
 * way to send another one.
 */
export function ReferenceSizeNotice({ delivery, className }: { delivery: CanvasDelivery | null | undefined; className?: string }) {
  const { schema } = useProductSchema();
  const reference = delivery?.reference;
  if (!delivery || delivery.turn || !reference || !reference.turn.changed) return null;
  const sizeText = (size: CanvasPixels) => schemaMessage(schema, 'ui.canvas.size.pixels', { width: size.width, height: size.height });
  const kind = reference.turn.nearest ? 'follows_reference_nearest' : 'follows_reference';
  const text = schemaMessage(schema, `ui.canvas.size.${kind}`, { reference: sizeText(reference.pixels), output: turnOutputText(schema, reference.turn, sizeText) });
  // #94: the model cannot keep the reference's ratio, so that is said as "unavailable" with the sizes in the hint.
  return (
    <div role={reference.turn.nearest ? undefined : 'status'} data-testid="reference-size-notice" data-kind={kind} className={cn(NOTICE_CLASS, className)}>
      {reference.turn.nearest
        ? <UnavailableNotice testId="reference-size-unavailable" label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.canvas.reference_ratio_unsupported'))} detail={text} />
        : <span className="min-w-0 [overflow-wrap:anywhere]">{text}</span>}
    </div>
  );
}

/**
 * #653: the canvas is a ratio and the model decides the output size of a canvas turn. When the artboard is the next
 * turn's source the notice says the size really sent (`delivery.turn`): the model's highest quality at the canvas
 * ratio, which may cost more. When the model has no size of that ratio (`nearest`), the notice says so whether or not
 * the artboard is the source, with the nearest ratio it will use and the offer to make the canvas that ratio; the
 * other way out is another model. Switching models never changes the canvas.
 */
export function CanvasSizeNotice({ sessionId, delivery, className }: { sessionId: string; delivery: CanvasDelivery; className?: string }) {
  const { schema } = useProductSchema();
  const sizeText = (size: CanvasPixels) => schemaMessage(schema, 'ui.canvas.size.pixels', { width: size.width, height: size.height });
  const { plan, pixels, known, source } = delivery;
  if (!known || !plan || (!plan.nearest && !source)) return null;
  const output = turnOutputText(schema, plan, sizeText);
  if (!plan.nearest) {
    const text = schemaMessage(schema, 'ui.canvas.size.follows', { ratio: plan.ratio, output });
    return (
      <div role="status" data-testid="canvas-size-notice" data-kind="follows" className={cn(NOTICE_CLASS, className)}>
        <span className="min-w-0 [overflow-wrap:anywhere]">{text}</span>
      </div>
    );
  }
  const kind = source ? 'follows_nearest' : 'ratio_missing';
  const text = schemaMessage(schema, `ui.canvas.size.${kind}`, { ratio: plan.ratio, nearest: plan.outputRatio, output });
  const adopt = canvasPixelsForRatio(plan.outputRatio, pixels, schema.canvas.frontend.artboard, schema.canvas.limits);
  return (
    <div data-testid="canvas-size-notice" data-kind={kind} className={cn(NOTICE_CLASS, className)}>
      <SizeUnavailable schema={schema} detail={text} />
      {adopt && !samePixels(adopt, pixels) ? (
        <AdoptButton label={schemaMessage(schema, 'ui.canvas.size.adopt', { ratio: plan.outputRatio })} onClick={() => applyCanvasResize(schema, sessionId, adopt)} />
      ) : null}
    </div>
  );
}
