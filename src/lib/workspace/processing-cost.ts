import { NO_CREDITS, offeringShowsCredits, type OfferingWire } from '@/lib/agent-offerings';
import { formatNativePrice, parseNativeQuote } from '@/lib/pricing';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { creditRange } from '@/lib/workspace/processing-inputs';

export type ProcessingCostLine =
  | { kind: 'empty' }
  /** Steps are chosen but none of them can run on this model, so nothing is priced (#324). */
  | { kind: 'none' }
  | { kind: 'unknown' }
  /** Every chosen step's provider bills without credits (credits_metered). */
  | { kind: 'unmetered' }
  | { kind: 'total'; credits: number }
  | { kind: 'range'; low: number; high: number };

/**
 * The footer of the processing panel. With no step chosen there is nothing to
 * price, so it says to choose one instead of reporting a total of zero.
 */
export function processingCostLine(stepCount: number, creditsKnown: boolean, credits: number, maximumCredits: number, meteredCount = stepCount): ProcessingCostLine {
  if (stepCount === 0) return { kind: 'empty' };
  if (meteredCount === 0) return { kind: 'unmetered' };
  if (!creditsKnown) return { kind: 'unknown' };
  return maximumCredits > credits ? { kind: 'range', low: credits, high: maximumCredits } : { kind: 'total', credits };
}

/**
 * The footer for the chosen steps (#324). Only a step that can run on the model is priced: one that has no
 * input path (its route is null, e.g. retexture on a model above every service's size limit) or waits on
 * such a step costs nothing. Each step is priced at its Offering's quote, which already includes the
 * extras of fixed values (Tripo's complete exports), and up to its maximum when optional extras remain.
 */
type PricedStep = { offering: OfferingWire; route: unknown; blocked?: boolean };

function stepRuns(step: PricedStep): boolean {
  return step.route !== null && step.route !== undefined && !step.blocked;
}

export function processingStepsCostLine(steps: readonly PricedStep[]): ProcessingCostLine {
  if (steps.length === 0) return { kind: 'empty' };
  const runnable = steps.filter(stepRuns);
  if (runnable.length === 0) return { kind: 'none' };
  // Only steps whose provider bills in credits (Schema credits_metered) count toward the total.
  const metered = runnable.filter((step) => offeringShowsCredits(step.offering));
  const creditsKnown = metered.every((step) => step.offering.estimated_credits !== undefined);
  const credits = metered.reduce((sum, step) => sum + (step.offering.estimated_credits ?? 0), 0);
  const maximum = metered.reduce((sum, step) => sum + (creditRange(step.offering)?.high ?? 0), 0);
  return processingCostLine(runnable.length, creditsKnown, credits, maximum, metered.length);
}

/**
 * The processing footer when Offerings of both pricing kinds are chosen (#335): the steps priced in their
 * provider's own unit (`price_mode: native`, e.g. Hyper3D) are listed one by one, and the credit line
 * totals the rest (e.g. Meshy). Both parts count only the steps that can run on the model (#324), so a
 * native step without a route or behind a blocked step is neither listed nor hides the credit total.
 * `credits` is null when steps can run but all of them are native-priced.
 */
export function processingStepsCost<Step extends PricedStep>(steps: readonly Step[]): { native: Step[]; credits: ProcessingCostLine | null } {
  if (steps.length === 0) return { native: [], credits: { kind: 'empty' } };
  const runnable = steps.filter(stepRuns);
  if (runnable.length === 0) return { native: [], credits: { kind: 'none' } };
  const native = runnable.filter((step) => step.offering.price_mode === 'native');
  const others = runnable.filter((step) => step.offering.price_mode !== 'native');
  return { native, credits: others.length > 0 ? processingStepsCostLine(others) : null };
}

/**
 * One Offering's Schema quote in words: the native default estimate, its credits (a range
 * when optional extras can add more), "/" for a provider that does not bill in credits, or
 * that no price is declared. The step line and each service choice of the processing panel
 * read this, so a choice states the same price the step then shows (#321).
 */
export function offeringQuoteText(schema: ProductSchema, offering: OfferingWire): string {
  if (offering.price_mode === 'native') {
    const quote = parseNativeQuote(offering.default_native_quote);
    const amount = quote ? formatNativePrice(quote.total_amount, quote.unit_label) : null;
    return quote
      ? formatSchemaText(schemaText(schema, 'ui.pricing.default_estimate'), { amount: amount ?? schemaText(schema, 'ui.pricing.unknown') })
      : schemaText(schema, 'ui.pricing.unknown');
  }
  if (!offeringShowsCredits(offering)) return `${schemaText(schema, 'ui.workspace.harness.credits')}: ${NO_CREDITS}`;
  const range = creditRange(offering);
  if (!range) return schemaText(schema, 'ui.workspace.harness.credits_unknown');
  return `${schemaText(schema, 'ui.workspace.harness.credits')}: ${range.high > range.low ? `${range.low}–${range.high}` : range.low}`;
}
