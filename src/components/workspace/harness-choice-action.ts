import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import { ChoiceActionError } from '@/lib/chat/choice-actions';
import type { ChoiceAction } from '@/lib/chat/choice-cards';
import type { HarnessSelectionInput } from '@/lib/harness';
import type { ProductSchema } from '@/lib/product-schema';
import { harnessAutoSelectOffering, harnessClearSelection, harnessOperationSelection, harnessSelectionInput } from './harness-panel';

/** The session Harness as an option's action uses it (#374): the saved selection now, and the save the 功能 panel uses. */
export interface ChoiceActionHarness {
  /** The saved selection of the loaded Harness view; null while it is not loaded. */
  selection: HarnessSelectionInput | null;
  /**
   * useSessionHarness().save: PUT /api/v1/chat/sessions/{id}/harness with the loaded revision as `expected_revision`
   * (a stale one is a 409 conflict) and the result published to every view of the session.
   */
  save: (selection: HarnessSelectionInput) => Promise<unknown>;
}

/**
 * Saves the function selection an option's action asks for (#374), with the panel's own helpers, so the saved selection
 * is the one the panel would save for the same click.
 *
 * - clear_operation: harnessClearSelection keeps the enabled operations and every other setting; nothing is saved when
 *   no function is selected.
 * - switch_operation: harnessOperationSelection fixes the function (and adds it to the enabled operations). The server
 *   refuses a function without a service, so the service is the one harnessAutoSelectOffering adopts: the only available
 *   one. With several or none the person has to choose in the 功能 panel (the rejection says so); once they saved that
 *   selection, answering the card needs no save at all.
 *
 * Rejects with a ChoiceActionError, or the save's own error (HarnessBrowserError, 409 on a revision conflict).
 */
export async function applyChoiceAction(
  action: ChoiceAction,
  deps: {
    schema: ProductSchema;
    harness: ChoiceActionHarness;
    /** The public Offering list of a capability; the page's own request by default. */
    offerings?: (capability: string) => Promise<OfferingWire[]>;
  },
): Promise<void> {
  const saved = deps.harness.selection;
  if (!saved) throw new ChoiceActionError('failed');
  if (action.type === 'clear_operation') {
    if (saved.operation_id) await deps.harness.save(harnessClearSelection(saved));
    return;
  }
  const declaration = deps.schema.chat.harness;
  const operation = declaration?.operations.find((item) => item.id === action.operationId);
  if (!declaration || !operation || operation.unavailable_reason) throw new ChoiceActionError('unavailable');
  if (saved.operation_id === operation.id && saved.offering_id && saved.offering_revision) return;
  const draft = harnessOperationSelection(saved, operation.id, declaration.default_enabled_operations, declaration.default_allow_prerequisites);
  const offerings = await (deps.offerings ?? ((capability) => fetchCapabilityOfferings(deps.schema, capability)))(operation.capability);
  const selected = harnessAutoSelectOffering(draft, operation, offerings, false, deps.schema);
  const offering = offerings.find((item) => item.id === selected.offering_id && item.revision === selected.offering_revision);
  if (!offering) {
    const available = offerings.some((item) => item.available && item.capability === operation.capability
      && (!operation.model_ids?.length || operation.model_ids.includes(item.model_id)));
    throw new ChoiceActionError(available ? 'service_choose' : 'service_none');
  }
  await deps.harness.save(harnessSelectionInput(selected, offering));
}
