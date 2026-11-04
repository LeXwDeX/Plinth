'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';
import { clampCanvasPixels, samePixels, type CanvasPixels } from '@/lib/canvas/artboard';
import { schemaMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { CanvasTurnOutput } from '@/lib/workspace/canvas-size';
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

/** The "model cannot output the canvas size" line: icon and short reason inline, the full sentence in the hint (#94). */
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
 * #246: the 2D model's output size differs from the canvas size. Switching models never resizes the
 * canvas, so the difference is said here, with the one action that resolves it: set the model to the
 * canvas size when it offers it, otherwise make the canvas the size the model produces.
 * #284: when the artboard is the next turn's source, the server sets the output size from the canvas (#272),
 * so the notice says that and the size really sent (`delivery.turn`), never the remembered setting. It is
 * shown only when that setting differs or the model cannot produce the canvas size; the nearest legal size
 * comes with the offer to make the canvas that size.
 */
export function CanvasSizeNotice({ sessionId, delivery, className }: { sessionId: string; delivery: CanvasDelivery; className?: string }) {
  const { schema } = useProductSchema();
  const sizeText = (size: CanvasPixels) => schemaMessage(schema, 'ui.canvas.size.pixels', { width: size.width, height: size.height });
  const { fit, pixels, known, turn } = delivery;
  if (!known) return null;
  const canvas = sizeText(pixels);
  if (turn) {
    if (!turn.nearest && fit.kind !== 'align' && fit.kind !== 'conflict') return null;
    const output = turnOutputText(schema, turn, sizeText);
    const nearest = turn.nearest ? clampCanvasPixels(turn.pixels, schema.canvas.limits) : null;
    // Resizing the canvas to the size it already has does nothing, so it is not offered.
    const adoptTurn = nearest && !samePixels(nearest, pixels) ? nearest : null;
    const kind = turn.nearest ? 'follows_nearest' : 'follows';
    const text = schemaMessage(schema, `ui.canvas.size.${kind}`, { canvas, output });
    return (
      <div role={turn.nearest ? undefined : 'status'} data-testid="canvas-size-notice" data-kind={kind} className={cn(NOTICE_CLASS, className)}>
        {turn.nearest ? <SizeUnavailable schema={schema} detail={text} /> : <span className="min-w-0 [overflow-wrap:anywhere]">{text}</span>}
        {adoptTurn ? <AdoptButton label={schemaMessage(schema, 'ui.canvas.size.adopt', { output: sizeText(adoptTurn) })} onClick={() => applyCanvasResize(schema, sessionId, adoptTurn)} /> : null}
      </div>
    );
  }
  if (fit.kind !== 'align' && fit.kind !== 'conflict') return null;
  const conflictSize = fit.kind === 'conflict' ? clampCanvasPixels(fit.output, schema.canvas.limits) : null;
  const adopt = conflictSize && !samePixels(conflictSize, pixels) ? conflictSize : null;
  const message = fit.kind === 'align'
    ? schemaMessage(schema, 'ui.canvas.size.mismatch', { output: fit.output ? sizeText(fit.output) : '', canvas })
    : fit.output
      ? schemaMessage(schema, 'ui.canvas.size.conflict', { canvas, output: sizeText(fit.output) })
      : schemaMessage(schema, 'ui.canvas.size.conflict_unknown', { canvas });
  return (
    <div role={fit.kind === 'conflict' ? undefined : 'status'} data-testid="canvas-size-notice" data-kind={fit.kind} className={cn(NOTICE_CLASS, className)}>
      {fit.kind === 'conflict' ? <SizeUnavailable schema={schema} detail={message} /> : <span className="min-w-0 [overflow-wrap:anywhere]">{message}</span>}
      {fit.kind === 'align' ? (
        <button
          type="button"
          data-testid="canvas-size-align"
          className="shrink-0 rounded text-primary underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-ring"
          onClick={() => { for (const [name, value] of Object.entries(fit.settings)) delivery.generation.setValue(name, value); }}
        >
          {schemaText(schema, 'ui.canvas.size.align')}
        </button>
      ) : adopt ? (
        <AdoptButton label={schemaMessage(schema, 'ui.canvas.size.adopt', { output: sizeText(adopt) })} onClick={() => applyCanvasResize(schema, sessionId, adopt)} />
      ) : null}
    </div>
  );
}
