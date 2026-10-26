'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from '@/hooks/use-auth';
import { useSessionHarness } from '@/hooks/use-session-harness';
import type { HarnessSelection, HarnessView } from '@/lib/harness';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';

export function harnessSelectionLabel(schema: ProductSchema, selection: HarnessSelection, text: (key: string) => string) {
  if (!selection.operation_id) return text('ui.harness.status_free');
  return schema.chat.harness?.operations.find(operation => operation.id === selection.operation_id)?.label ?? text('ui.harness.selection_unavailable');
}

export function harnessTurnDiffers(view: HarnessView) {
  return Boolean(view.turn && (
    view.turn.selection.revision !== view.selection.revision
    || view.turn.selection.digest !== view.selection.digest
  ));
}

/** Human-readable selection only. The turn binding is authoritative for a turn already in progress. */
export function HarnessSummary({ sessionId }: { sessionId: string | null }) {
  const { schema, text } = useProductSchema();
  const { user } = useAuth();
  const { view, error, busy } = useSessionHarness(sessionId, user?.id ?? '');
  if (!schema.chat.harness || !sessionId || !user) return null;
  if (!view) return <p role={error ? 'alert' : 'status'} className="text-[11px] leading-4 text-muted-foreground">{text(error ? 'ui.harness.load_failed' : 'ui.harness.loading')}</p>;
  const label = harnessSelectionLabel(schema, view.selection, text);
  const differs = harnessTurnDiffers(view);
  return <div data-testid="harness-summary" aria-busy={busy} className="space-y-1 text-[11px] leading-4 text-muted-foreground">
    <p>{formatSchemaText(text('ui.harness.current_summary'), { operation: label })}</p>
    {differs && view.turn ? <><p>{formatSchemaText(text('ui.harness.turn_summary'), { operation: harnessSelectionLabel(schema, view.turn.selection, text) })}</p><p>{text('ui.harness.next_turn')}</p></> : null}
    {view.status === 'needs_input' || view.status === 'unavailable' ? <p>{text(`ui.harness.status_${view.status}`)}</p> : null}
  </div>;
}
