import { useMemo, useState } from 'react';
import { useActiveAgentScope } from '@/hooks/use-session-agent-scope';
import { pickerDisabledReason, scopeAllowsDomain } from '@/lib/chat/agent-scope';
import { parameterDisabled, parameterVisible } from '@/lib/product-schema';
import { useProductSchema } from '@/components/product-schema-provider';
import { useSessionAgentParams } from '@/hooks/use-session-agent-params';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import type { AgentParameterState } from '@/lib/agent-params';
import type { OfferingWire } from '@/lib/agent-offerings';
import {
  buildGenerationParams,
  effectiveOfferingValues,
  estimateCredits,
  staleSizeValues,
  offeringForTurn,
  type CreditEstimate,
  type GenerationParamsModel,
} from '@/lib/workspace/generation-params';
import { useWorkspaceStore, type WorkspaceDomain } from '@/store/workspace-store';

export interface GenerationParams {
  domain: WorkspaceDomain;
  modelName: string;
  ready: boolean;
  allowed: boolean;
  restrictedReason: string;
  failed: boolean;
  saveError: string | null;
  offerings: OfferingWire[];
  offering?: OfferingWire;
  selectOffering: (id: string) => void;
  model: GenerationParamsModel;
  values: Record<string, unknown>;
  parameterStates?: Record<string, AgentParameterState>;
  setValue: (name: string, value: unknown) => void;
  flush: () => Promise<void>;
  estimate: CreditEstimate;
  nextTurn: Pick<GenerationParams, 'offering' | 'model' | 'values' | 'parameterStates' | 'estimate' | 'setValue' | 'flush'>;
}

function useOfferingParams(sessionId: string | null, offering: OfferingWire | undefined) {
  const model = useMemo(() => buildGenerationParams(offering ? [offering] : []), [offering]);
  const params = useSessionAgentParams(offering ? sessionId : null, model.paramSchemas, offering?.id);
  const values = useMemo(() => {
    const entered = params.enteredValues;
    const effective = effectiveOfferingValues(offering, entered);
    return effectiveOfferingValues(offering, { ...effective, ...staleSizeValues(model.sizeParams, entered) });
  }, [params.enteredValues, offering, model]);
  return {
    ...params,
    model,
    offering,
    values,
    setValue: (name: string, value: unknown) => {
      const parameter = model.paramSchemas.find((param) => param.name === name);
      if (offering?.available && parameter && parameterVisible(parameter, values) && !parameterDisabled(parameter, values)) {
        params.setValue(name, value);
      }
    },
    flush: async () => {
      if (!offering?.available || params.loading || params.error || !params.ownerId) {
        throw new Error('Offering parameters are not ready');
      }
      await params.flush();
    },
    estimate: estimateCredits(offering?.available ? offering : undefined, values),
  };
}

/** Each domain/capability reads its exact Offering. Opening the panel never writes defaults. */
export function useGenerationParams(domain: WorkspaceDomain, sessionId: string | null, hasReference: boolean): GenerationParams {
  const { schema } = useProductSchema();
  const scope = useActiveAgentScope();
  const allowed = scopeAllowsDomain(schema, scope, domain);
  const selectedModelId = useWorkspaceStore((state) => state.modelByDomain[domain]);
  const { offeringsByCapability, error } = useWorkspaceOfferings(schema, domain);
  const offerings = useMemo(
    () => Object.values(offeringsByCapability ?? {}).flat().filter((item) => item.model_id === selectedModelId),
    [offeringsByCapability, selectedModelId],
  );
  const context = `${sessionId}:${schema.revision}:${domain}:${selectedModelId}:${hasReference}`;
  const [choice, setChoice] = useState({ context, id: '' });
  const available = offerings.filter((item) => item.available);
  const inspectedOffering = choice.context === context ? offerings.find((item) => item.id === choice.id) : undefined;
  const offering = inspectedOffering ?? offeringForTurn(available, hasReference) ?? offerings[0];
  const executionOffering = offeringForTurn(available, hasReference);
  const display = useOfferingParams(sessionId, offering);
  const execution = useOfferingParams(sessionId, executionOffering);
  const nextTurn = offering?.id === executionOffering?.id ? display : execution;
  return {
    ...display,
    domain,
    allowed,
    restrictedReason: allowed ? '' : pickerDisabledReason(schema, scope),
    modelName: offering?.model_name ?? '',
    ready: offeringsByCapability !== undefined && !display.loading,
    failed: Boolean(error || display.error),
    saveError: display.saveError,
    offerings,
    offering,
    selectOffering: (id: string) => setChoice({ context, id }),
    nextTurn,
  };
}
