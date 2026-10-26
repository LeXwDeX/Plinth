import { useCallback, useState } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useModelProcessingCapabilities } from '@/hooks/use-model-processing-capabilities';
import { schemaText } from '@/lib/product-schema';
import {
  isSubmissionUnknown, pausedResumableStep, resumeButtonLabel, resumeProcessingPlan, StepChangedError,
  type PendingStepGrant, type ProcessingPlan,
} from '@/lib/workspace/processing-plan';

interface Options {
  /** Runs after the plan was resumed, to read the plan (and whatever shows it) again. */
  onResumed: () => Promise<unknown> | void;
  /** Runs after a resume failed, so the caller can read the plan again: its version may have moved on. */
  onFailed?: () => void;
}

/**
 * Continuing a paused processing plan, for every panel that offers it (the 加工 tab and the 格式 tab). It owns
 * the pending consent grant, the busy flag and the error text; the grant, revision checks and request bodies are
 * `resumeProcessingPlan`'s. `step` is undefined while there is nothing to continue; `available` is false while
 * the step's Offering cannot run now (an unknown submission only needs reconciling, so it does not need one).
 */
export function useProcessingPlanResume(plan: ProcessingPlan | null | undefined, { onResumed, onFailed }: Options) {
  const { schema } = useProductSchema();
  const { offerings, refresh } = useModelProcessingCapabilities();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pendingGrant, setPendingGrant] = useState<PendingStepGrant | null>(null);
  const step = pausedResumableStep(plan);
  const submissionUnknown = step ? isSubmissionUnknown(step) : false;
  // A resume is confirmed at the Offering's current cost; without an available Offering it cannot continue.
  const offering = step ? offerings?.[step.capability]?.find((candidate) => candidate.id === step.offering_id && candidate.available) : undefined;
  const available = step !== undefined && (submissionUnknown || offering !== undefined);
  const label = step ? resumeButtonLabel(schema, step, pendingGrant !== null, offering) : '';

  const resume = useCallback(async () => {
    if (!plan || !step || !available || busy) return;
    setBusy(true);
    setError('');
    try {
      const outcome = await resumeProcessingPlan({ schema, plan, step, offering, pendingGrant, refreshOfferings: refresh, onGrant: setPendingGrant });
      if (outcome.kind === 'resumed') {
        await onResumed();
        setPendingGrant(null);
      }
    } catch (cause) {
      if (cause instanceof StepChangedError) setPendingGrant(null);
      setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.processing.submit_failed'));
      onFailed?.();
    } finally {
      setBusy(false);
    }
  }, [available, busy, offering, onFailed, onResumed, pendingGrant, plan, refresh, schema, step]);

  return { step, offering, submissionUnknown, available, label, busy, error, confirming: pendingGrant !== null, resume };
}
