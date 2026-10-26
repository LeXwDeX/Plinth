'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { NO_CREDITS, type OfferingWire } from '@/lib/agent-offerings';
import { schemaCountMessage, schemaText } from '@/lib/product-schema';
import { offeringQuoteText, processingStepsCost } from '@/lib/workspace/processing-cost';
import { cn } from '@/lib/utils';

export interface ProcessingCostStep {
  capability: string;
  offering: OfferingWire;
  route: unknown;
  blocked?: boolean;
}

/**
 * The price at the foot of the 加工 panel (#324, #335). Steps priced in their provider's own unit are
 * listed with their default estimate; the credit-priced steps are totalled below them. Only the steps
 * that can run on the model count in either part.
 */
export function ProcessingCostSummary({ steps }: { steps: readonly ProcessingCostStep[] }) {
  const { schema } = useProductSchema();
  const { native, credits } = processingStepsCost(steps);
  const stepLabel = (capability: string) => schema.capabilities.find((item) => item.id === capability)?.label_cn ?? capability;
  return <>
    {native.length > 0 && <div className="mb-3 text-xs text-muted-foreground" data-testid="processing-cost-native">
      <p className="mb-1 font-medium">{schemaText(schema, 'ui.pricing.totals_heading')}</p>
      {native.map((step) => <p key={step.capability}>{stepLabel(step.capability)}: {offeringQuoteText(schema, step.offering)}</p>)}
    </div>}
    {credits && <p className={cn('mb-3', (credits.kind === 'empty' || credits.kind === 'none') && 'text-xs text-muted-foreground')} data-testid="processing-cost" data-kind={credits.kind}>{credits.kind === 'empty'
      ? schemaText(schema, 'ui.studio.processing.cost_empty')
      : credits.kind === 'none'
        ? schemaText(schema, 'ui.studio.processing.cost_none')
        : credits.kind === 'unmetered'
          ? `${schemaText(schema, 'ui.workspace.harness.credits')}: ${NO_CREDITS}`
          : credits.kind === 'unknown'
            ? schemaText(schema, 'ui.workspace.harness.credits_unknown')
            : credits.kind === 'range'
              ? schemaCountMessage(schema, 'ui.studio.processing.cost_range', credits.high, { low: credits.low, high: credits.high })
              : schemaCountMessage(schema, 'ui.studio.processing.cost_total', credits.credits, { credits: credits.credits })}</p>}
    {credits?.kind === 'range' && <p className="mb-3 text-muted-foreground">
      {schemaText(schema, 'ui.workspace.processing.plan_cost_extra')}
    </p>}
  </>;
}
