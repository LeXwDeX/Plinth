'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { Field } from '@/components/ui/field';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { OptionSelect } from '@/components/ui/option-select';
import { schemaText } from '@/lib/product-schema';
import { offeringQuoteText } from '@/lib/workspace/processing-cost';
import type { ProcessingSourceStep } from '@/lib/workspace/processing-source';

/**
 * The service a processing step runs on (#321). With one compatible Offering it is only named,
 * in gray, as before; with several the person picks one, each option naming its provider, model
 * and Schema quote. The default is the first compatible Offering in the server's order.
 */
export function ProcessingStepService({ step, fieldId, disabled, disabledReason, onChoose }: {
  step: ProcessingSourceStep;
  fieldId: string;
  disabled: boolean;
  /** Why the picker is locked, a few words, shown in its hover hint (#94). */
  disabledReason?: string;
  onChoose: (offeringID: string) => void;
}) {
  const { schema } = useProductSchema();
  const name = (offering: ProcessingSourceStep['offering']) => `${offering.provider_name} · ${offering.model_name}`;
  if (step.choices.length < 2) {
    return <p className="text-muted-foreground" data-testid={`processing-provider-${step.capability}`}>{name(step.offering)}</p>;
  }
  return <div data-testid={`processing-offering-${step.capability}`}>
    <Field id={fieldId} label={schemaText(schema, 'ui.workspace.processing.offering')}>
      {(control) => {
        const select = <OptionSelect {...control} value={step.offering.id} disabled={disabled}
          options={step.choices.map((offering) => ({ value: offering.id, label: `${name(offering)} · ${offeringQuoteText(schema, offering)}` }))}
          onValueChange={onChoose} />;
        return disabledReason ? <HintTooltip name={schemaText(schema, 'ui.workspace.processing.offering')} reason={disabledReason} className="w-full">{select}</HintTooltip> : select;
      }}
    </Field>
  </div>;
}
