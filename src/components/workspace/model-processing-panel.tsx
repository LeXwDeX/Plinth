'use client';

import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import { SchemaParameters, schemaParameterValues } from '@/components/form/schema-parameters';
import { useModelProcessingCapabilities } from '@/hooks/use-model-processing-capabilities';
import { useProcessingPlanResume } from '@/hooks/use-processing-plan-resume';
import { offeringFixedValues } from '@/lib/agent-offerings';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { unavailableLabel } from '@/lib/unavailable';
import { faceBudgetDefaults, prerequisiteRefusal, processingPrerequisite, processingSourceLabel, processingSourceSteps, actionParameter, actionValue, panelFields, sourceRefusals, type ProcessingSourceAsset, type ProcessingSourceTask } from '@/lib/workspace/processing-source';
import { partsKeyOf } from '@/lib/workspace/model-parts';
import { processingUnavailableNotice } from '@/lib/workspace/processing-refusals';
import { offeringQuoteText } from '@/lib/workspace/processing-cost';
import { inputRuleViolations, missingRequiredParameters, uploadAssetInputs } from '@/lib/workspace/processing-inputs';
import { uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import { continuePastCheckpoint, failedCheckpointStep, planHoldsModel, reconcileProcessingPlan, unreconciledStep, retryableFailedStep, stepCreditSuffix, type ProcessingPlan } from '@/lib/workspace/processing-plan';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';
import { Action } from '@/components/ui/button';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Surface } from '@/components/ui/surface';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { ProcessingCostSummary } from '@/components/workspace/processing-cost-summary';
import { ProcessingStepService } from '@/components/workspace/processing-step-service';

function randomKey() {
  const words = new Uint32Array(4);
  globalThis.crypto.getRandomValues(words);
  return Array.from(words, (word) => word.toString(16).padStart(8, '0')).join('');
}

/** Wraps a control in the hover hint that says why it is disabled (#94); without a reason the control stands alone. */
function ReasonHint({ name, reason, className, children }: { name: string; reason?: string; className?: string; children: ReactNode }) {
  return reason ? <HintTooltip name={name} reason={reason} className={className}>{children}</HintTooltip> : <>{children}</>;
}

/** A step's form values and action are kept per service, so switching never sends one Offering's inputs to another. */
function formKey(capability: string, offeringID: string): string {
  return `${capability}\u0000${offeringID}`;
}

/**
 * 加工：在右侧栏的「加工」标签里，和对话、舞台同时可用，不是弹窗。
 * 选好步骤和参数后提交一个处理计划，进度显示在同一页。每一步默认用服务端列表（Schema 的 priority 在前）里
 * 第一个可用且接受当前来源的供给项；同一步有多个兼容供给项时可以改选（#321），参数、报价和提交都跟随所选项；确认按钮固定在面板底部。
 */
export function ModelProcessingPanel({ selection }: { selection: CurrentObject }) {
  const fieldPrefix = useId();
  const { schema } = useProductSchema();
  const reasonText = (name: string) => schemaText(schema, `ui.unavailable.reason.processing.${name}`);
  const notice = (reason: string, detail: string, testId?: string, className?: string) => (
    <UnavailableNotice label={unavailableLabel(schema, reason)} detail={detail} testId={testId} className={className} />);
  const sessionID = useChatStore((state) => state.activeSessionId);
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const [selected, setSelected] = useState<string[]>([]);
  const [values, setValues] = useState<Record<string, Record<string, unknown>>>({});
  const [actions, setActions] = useState<Record<string, string>>({});
  // The service the person picked per capability; absent means the step's default.
  const [chosen, setChosen] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const lastSubmit = useRef<{ body: string; key: string } | null>(null);
  // 同一张风格图重试时复用已上传的输入资产，计划请求体与幂等键保持不变。
  const uploadedInputs = useRef(new Map<string, string>());
  const { available: availableCapabilities, offerings, error: offeringsError, refresh: refreshOfferings } = useModelProcessingCapabilities();
  const { data: source, error: sourceError } = useSWR<ProcessingSourceTask>(['processing-source', selection.taskId], async () => {
    const route = schema.runtime.http.routes.task_item.replace('{taskID}', encodeURIComponent(selection.taskId));
    const response = await fetch(route);
    if (!response.ok) throw new Error(`task ${response.status}`);
    const raw: unknown = await response.json();
    if (!raw || typeof raw !== 'object') throw new Error('invalid task');
    const task = raw as Record<string, unknown>;
    const projected = projectNativeTaskDetail(raw, schema.task_states, schema.runtime.frontend.percentage_multiplier);
    if (!projected) throw new Error('invalid task');
    return { id: String(task.id ?? ''), session_id: String(task.session_id ?? ''), capability: String(task.capability ?? ''),
      provider_id: String(task.provider_id ?? ''), offering_id: projected.offeringId, state: projected.state, assets: projected.downloadables,
      created_at: typeof task.created_at === 'string' ? task.created_at : undefined,
      upstream_job_created_at: typeof task.upstream_job_created_at === 'string' ? task.upstream_job_created_at : undefined,
      upstream_job_gone: task.upstream_job_gone === true };
  }, { revalidateOnFocus: false });
  const tracked = source?.assets.find((candidate) => candidate.id === selection.assetId && candidate.kind === 'model') ?? null;
  // The triangles the stage measured on this model (every mesh instance in its scene), so a step over its face limit
  // brings in Remesh first (#93). Unknown until the stage has loaded the model; the server measures again either way.
  const measuredTriangles = useStudioStore((state) => (state.stageParts && state.stageParts.key === partsKeyOf(selection)
    ? state.stageParts.parts.reduce((total, part) => total + part.triangles, 0) : undefined));
  const asset = useMemo<ProcessingSourceAsset | null>(() => (tracked && measuredTriangles
    ? { ...tracked, triangles: measuredTriangles } : tracked), [tracked, measuredTriangles]);
  const ownedSession = Boolean(sessionID && source?.session_id === sessionID);
  const plansRoute = sessionID ? `${schema.runtime.http.routes.processing_plans}?session_id=${encodeURIComponent(sessionID)}` : null;
  const { data: plans, mutate: reload } = useSWR<ProcessingPlan[]>(ownedSession ? plansRoute : null, async (route: string) => {
    const response = await fetch(route);
    if (!response.ok) throw new Error(`plans ${response.status}`);
    const body: { plans?: ProcessingPlan[] } = await response.json();
    return body.plans ?? [];
  }, { refreshInterval: 2000, revalidateOnFocus: false });
  const currentPlan = plans?.find((plan) => plan.source_task_id === selection.taskId && plan.source_asset_id === selection.assetId);
  // A plan that only waits for the person to confirm a step does not hold the model (#326 P): they may start
  // another plan instead of continuing it.
  const activePlan = planHoldsModel(currentPlan);
  // Continuing a paused step is shared with the 格式 tab (consent grant, revision checks, request body).
  const resume = useProcessingPlanResume(currentPlan, { onResumed: reload });
  const working = busy || resume.busy;
  useEffect(() => {
    for (const plan of plans ?? []) for (const step of plan.steps) if (step.task_id) {
      registerTask({ taskId: step.task_id, sessionId: sessionID ?? undefined, capability: step.capability,
        progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
    }
  }, [plans, registerTask, schema.runtime.frontend.percentage_multiplier, sessionID]);
  const steps = useMemo(() => source && asset
    ? processingSourceSteps(availableCapabilities, selected, offerings, source, asset, chosen)
    : [], [source, asset, availableCapabilities, selected, offerings, chosen]);
  // 每步按 Schema 声明的可填参数组装提交输入，并按 input_rules 在提交前提示违例。
  // A face-reducing step before steps with a face limit starts at the most faces the tightest one takes (Remesh before
  // UV, also with Retexture in between, #93).
  const budgets = useMemo(() => Object.fromEntries(steps.map((step, index) => [step.capability,
    faceBudgetDefaults(step.offering, steps.slice(index + 1).map((next) => next.offering), values[formKey(step.capability, step.offering.id)] ?? {})])), [steps, values]);
  const stepInputs = useMemo(() => Object.fromEntries(steps.map((step) => {
    const parameters = panelFields(step.offering);
    const defaults = { ...Object.fromEntries(parameters.filter((parameter) => parameter.default !== undefined).map((parameter) => [parameter.name, parameter.default])), ...budgets[step.capability] };
    const key = formKey(step.capability, step.offering.id);
    const input = { ...offeringFixedValues(step.offering), ...schemaParameterValues(parameters, { ...defaults, ...values[key] }) };
    const action = actionParameter(step.offering);
    if (action && actions[key]) input[action.name] = actionValue(action, actions[key]);
    return [step.capability, input];
  })), [steps, values, actions, budgets]);
  // Required inputs still empty (a reference image, say): confirm stays disabled and the step names them (#321).
  const missingFields = useMemo(() => Object.fromEntries(steps.map((step) => {
    const parameters = panelFields(step.offering);
    const defaults = { ...Object.fromEntries(parameters.filter((parameter) => parameter.default !== undefined).map((parameter) => [parameter.name, parameter.default])), ...budgets[step.capability] };
    return [step.capability, missingRequiredParameters(parameters, { ...defaults, ...values[formKey(step.capability, step.offering.id)] })];
  })), [steps, values, budgets]);
  const ruleViolations = useMemo(() => Object.fromEntries(steps.map((step) => [
    step.capability, inputRuleViolations(step.offering, stepInputs[step.capability] ?? {}),
  ])), [steps, stepInputs]);
  const valid = ownedSession && asset && steps.length > 0 && steps.length === selected.length && !working && !activePlan
    && steps.every((step) => step.route && step.offering.available && (!actionParameter(step.offering) || Boolean(actions[formKey(step.capability, step.offering.id)]))
      && (ruleViolations[step.capability] ?? []).length === 0 && (missingFields[step.capability] ?? []).length === 0);
  const missingSummary = steps.filter((step) => (missingFields[step.capability] ?? []).length > 0).map((step) =>
    `${schema.capabilities.find((item) => item.id === step.capability)?.label_cn ?? step.capability}: ${(missingFields[step.capability] ?? []).map((field) => field.label).join(' / ')}`);

  // Why confirm is greyed, in a few words (#94); a request under way is not a reason.
  const submitReason = working || valid ? undefined
    : !ownedSession ? reasonText('session_mismatch')
      : !asset ? reasonText('asset_unavailable')
        : activePlan ? reasonText('plan_running')
          : selected.length === 0 ? reasonText('no_steps')
            : steps.length !== selected.length || steps.some((step) => !step.route || !step.offering.available) ? reasonText('step_unrunnable')
              : steps.some((step) => actionParameter(step.offering) && !actions[formKey(step.capability, step.offering.id)]) ? reasonText('action_missing')
                : steps.some((step) => (ruleViolations[step.capability] ?? []).length > 0) ? reasonText('input_rule')
                  : reasonText('required_missing');
  const lockReason = activePlan && !working ? reasonText('plan_running') : undefined;

  // Choosing a step the current model cannot feed directly (animation needs a rigged
  // source) also chooses the step that produces what it needs, but only one that can run on
  // the model itself; otherwise the step stays alone and says why (#321 E).
  const withStep = useCallback((current: string[], capabilityID: string) => {
    if (current.includes(capabilityID)) return current;
    const next = [...current, capabilityID];
    const prerequisite = source && asset ? processingPrerequisite(capabilityID, availableCapabilities, offerings, source, asset) : null;
    if (prerequisite && !next.includes(prerequisite)) next.push(prerequisite);
    return next;
  }, [asset, availableCapabilities, offerings, source]);
  const toggle = (capabilityID: string) => setSelected((current) => (current.includes(capabilityID)
    ? current.filter((id) => id !== capabilityID)
    : withStep(current, capabilityID)));
  // A stage toolbar button opens this panel with its step chosen, through the same rule as the
  // checkbox; it waits for the source so the prerequisite can be worked out.
  const processPreset = useStudioStore((state) => state.processPreset);
  useEffect(() => {
    if (!processPreset || !offerings || (!source && !sourceError)) return;
    if (availableCapabilities.some((capability) => capability.id === processPreset)) {
      setSelected((current) => withStep(current, processPreset));
    }
    useStudioStore.getState().clearProcessPreset();
  }, [availableCapabilities, offerings, processPreset, source, sourceError, withStep]);

  const submit = async () => {
    if (!valid || !sessionID) return;
    setBusy(true); setError('');
    try {
      const currentOfferings = await refreshOfferings();
      if (steps.some((step) => !currentOfferings?.[step.capability]?.some((offering) =>
        offering.id === step.offering.id && offering.available && offering.revision === step.offering.revision))) {
        throw new Error(schemaText(schema, 'ui.workspace.processing.plan_offering_changed'));
      }
      const uploadInput = async (blob: Blob, filename: string) => (await uploadWorkspaceInputAssetResult(schema, blob, filename)).id;
      const planSteps = [];
      for (const step of steps) {
        // 图片参数在面板里是 data URI；计划只接受平台输入资产 ID。
        const input = await uploadAssetInputs(step.offering, stepInputs[step.capability] ?? {}, uploadInput, uploadedInputs.current);
        planSteps.push({ offering_id: step.offering.id, expected_offering_revision: step.offering.revision, input });
      }
      const body = JSON.stringify({ session_id: sessionID, source_task_id: selection.taskId, source_asset_id: selection.assetId, steps: planSteps });
      const key = lastSubmit.current?.body === body ? lastSubmit.current.key : randomKey();
      lastSubmit.current = { body, key };
      const response = await fetch(schema.runtime.http.routes.processing_plans, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body });
      if (!response.ok) { const problem: { detail?: string } = await response.json().catch(() => ({})); throw new Error(problem.detail || `HTTP ${response.status}`); }
      await reload();
    } catch (cause) { setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.processing.submit_failed')); }
    finally { setBusy(false); }
  };

  const failedStep = retryableFailedStep(currentPlan);
  const needsReconciliation = unreconciledStep(currentPlan);
  // #251: a failed checkpoint pauses the plan before the next step. The person
  // can continue (the check is recorded as waived) or let the Agent revise it.
  const checkpointStep = failedCheckpointStep(currentPlan);
  // 只对本面板核对出的那个原任务提示“已关联原任务”，它结束后或换成别的步骤任务时不再显示。
  const [linkedTaskID, setLinkedTaskID] = useState('');
  const reconcile = async () => {
    if (!currentPlan || !needsReconciliation || working) return;
    setBusy(true); setError('');
    try {
      const updated = await reconcileProcessingPlan(schema, currentPlan);
      setLinkedTaskID(updated.steps.find((step) => step.state === 'RUNNING' && step.task_id)?.task_id ?? '');
      await reload((existing) => existing?.map((plan) => plan.id === updated.id ? updated : plan), { revalidate: false });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.processing.plan_reconcile_undetermined'));
      // A stale version may mean another tab already resolved this step.
      void reload().catch(() => {});
    } finally { setBusy(false); }
  };
  const failedOffering = failedStep
    ? offerings?.[failedStep.capability]?.find((offering) => offering.id === failedStep.offering_id && offering.available)
    : undefined;

  const continueCheckpoint = async () => {
    if (!currentPlan || !checkpointStep || working) return;
    setBusy(true); setError('');
    try {
      const updated = await continuePastCheckpoint(schema, currentPlan);
      await reload((existing) => existing?.map((plan) => plan.id === updated.id ? updated : plan), { revalidate: false });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.processing.plan_continue_checkpoint_failed'));
      // A stale version may mean another tab already continued this plan.
      void reload().catch(() => {});
    } finally { setBusy(false); }
  };

  const retry = async () => {
    if (!currentPlan || currentPlan.state !== 'FAILED' || !failedStep || !failedOffering) return;
    setBusy(true); setError('');
    try {
      const currentOfferings = await refreshOfferings();
      if (!currentOfferings?.[failedStep.capability]?.some((offering) =>
        offering.id === failedOffering.id && offering.available && offering.revision === failedOffering.revision)) {
        throw new Error(schemaText(schema, 'ui.workspace.processing.plan_offering_changed'));
      }
      const route = schema.runtime.http.routes.processing_plan_retry.replace('{planID}', encodeURIComponent(currentPlan.id));
      const response = await fetch(route, { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ expected_plan_version: currentPlan.version, expected_offering_revision: failedOffering.revision }) });
      if (!response.ok) { const problem: { detail?: string } = await response.json().catch(() => ({})); throw new Error(problem.detail || `HTTP ${response.status}`); }
      await reload();
    } catch (cause) { setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.processing.submit_failed')); }
    finally { setBusy(false); }
  };

  return <div className="flex min-h-0 flex-1 flex-col" data-testid="model-processing-panel">
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3 text-sm">
        {(sourceError || offeringsError) && <p role="alert" className="text-destructive">
          {schemaText(schema, 'ui.workspace.processing.load_failed')}
        </p>}
        {source && <Surface variant="inset" radius="xl" padding="md">
          <p className="text-muted-foreground">{schemaText(schema, 'ui.workspace.processing.source')}</p>
          <p className="break-all" data-testid="processing-source-label" data-asset-id={asset?.id ?? ''}>{processingSourceLabel(source, asset)}</p>
          {!ownedSession && notice(reasonText('session_mismatch'), schemaText(schema, 'ui.workspace.processing.session_mismatch'), 'processing-session-mismatch')}
          {!asset && notice(reasonText('asset_unavailable'), schemaText(schema, 'ui.workspace.processing.asset_unavailable'), 'processing-asset-unavailable')}
        </Surface>}
        {currentPlan && <Surface variant="inset" radius="xl" padding="md">
          <p className="font-medium">{schemaText(schema, `ui.workspace.processing.plan_${
            currentPlan.state === 'SUCCEEDED' ? 'done' : currentPlan.state === 'FAILED' ? 'failed'
              : currentPlan.state === 'PAUSED' ? 'paused' : 'running'
          }`)}</p>
          <ol className="mt-2 space-y-1">
            {currentPlan.steps.map((step, index) => <li key={`${step.offering_id}-${index}`}
              className="flex flex-col gap-0.5" data-testid={`processing-plan-step-${index}`} data-state={step.state}>
              <div className="flex justify-between gap-2">
                <span>{index + 1}. {schema.capabilities.find((item) => item.id === step.capability)?.label_cn ?? step.capability}</span>
                <span>{schemaText(schema, `ui.workspace.processing.plan_step_${
                  step.state === 'SUCCEEDED' ? 'done' : step.state === 'FAILED' ? 'failed' : step.state === 'PAUSED' ? 'paused'
                    : step.state === 'RUNNING' ? 'running' : step.state === 'SKIPPED' ? 'skipped'
                      : step.queued_until ? 'queued' : 'pending'
                }`)}</span>
              </div>
              {step.checkpoint ? <div className="text-xs text-muted-foreground" data-testid={`processing-plan-checkpoint-${index}`}>
                <span>{schemaText(schema, 'ui.workspace.processing.plan_checkpoint')}: {
                  schemaText(schema, `ui.workspace.processing.plan_checkpoint_state.${step.checkpoint.state}`) || step.checkpoint.state
                }</span>
                {(step.checkpoint.results ?? []).length > 0 ? <ul className="pl-3">
                  {step.checkpoint.results!.map((result) => {
                    const label = schemaText(schema, `ui.chat.review.check_names.${result.check}`) || result.check;
                    const status = schemaText(schema, `ui.chat.review.check_${result.status}`).replace('{label}', label)
                      || `${label}: ${result.status}`;
                    return <li key={result.check} data-testid={`processing-plan-checkpoint-result-${result.check}`}>
                      {status}{result.actual !== undefined && result.actual !== null
                        ? ` · ${formatSchemaText(schemaText(schema, 'ui.workspace.processing.plan_checkpoint_actual'), { actual: String(result.actual) })}` : ''}
                    </li>;
                  })}
                </ul> : null}
                {step.checkpoint.resolution ? <p>{formatSchemaText(schemaText(schema, 'ui.workspace.processing.plan_checkpoint_resolution'), { resolution: step.checkpoint.resolution })}</p> : null}
              </div> : null}
              {step.revision_operation ? <p className="text-xs text-muted-foreground" data-testid={`processing-plan-revision-${index}`}>
                {formatSchemaText(schemaText(schema, 'ui.workspace.processing.plan_revision'), {
                  operation: schemaText(schema, `ui.workspace.processing.plan_revision_op.${step.revision_operation}`) || step.revision_operation,
                  contingency: step.contingency_id ? ` (${step.contingency_id})` : '',
                  reason: step.revision_reason ?? '',
                })}
              </p> : null}
            </li>)}
          </ol>
          {currentPlan.failure && <div className="mt-2" data-testid="processing-plan-failure">
            {notice(reasonText(currentPlan.failure) || schemaText(schema, 'ui.workspace.processing.unavailable_reason.unavailable'), schemaText(schema, `ui.workspace.processing.${currentPlan.failure}`))}
          </div>}
          {linkedTaskID && currentPlan.state === 'RUNNING' && currentPlan.steps.some((step) => step.state === 'RUNNING' && step.task_id === linkedTaskID)
            && !currentPlan.failure && <p role="status" className="mt-2 text-muted-foreground">
              {schemaText(schema, 'ui.workspace.processing.plan_reconcile_linked')}
            </p>}
          {needsReconciliation && <ReasonHint name={schemaText(schema, 'ui.workspace.processing.plan_reconcile_action')} className="mt-3"
            reason={!working && !schema.runtime.http.routes.processing_plan_reconcile ? reasonText('reconcile_unavailable') : undefined}>
            <Action type="button"
              loading={working} disabled={working || !schema.runtime.http.routes.processing_plan_reconcile} onClick={() => void reconcile()}>
              {schemaText(schema, 'ui.workspace.processing.plan_reconcile_action')}
            </Action>
          </ReasonHint>}
          {currentPlan.state === 'FAILED' && currentPlan.steps.some((step) => step.state === 'FAILED' && step.retryable)
            && <ReasonHint name={schemaText(schema, 'ui.workspace.processing.plan_retry')} className="mt-3"
              reason={!working && !failedOffering ? schemaText(schema, 'ui.workspace.processing.unavailable_reason.unavailable') : undefined}>
              <Action type="button"
                loading={working} disabled={!failedOffering} onClick={() => void retry()}>
                {schemaText(schema, 'ui.workspace.processing.plan_retry')}{stepCreditSuffix(schema, failedOffering)}
              </Action>
            </ReasonHint>}
          {resume.step && <ReasonHint name={resume.label} className="mt-3"
            reason={!working && !resume.available ? schemaText(schema, 'ui.workspace.processing.unavailable_reason.unavailable') : undefined}>
            <Action type="button"
              loading={working} disabled={!resume.available} onClick={() => { setError(''); void resume.resume(); }}>
              {resume.label}
            </Action>
          </ReasonHint>}
          {currentPlan.state === 'PAUSED' && !activePlan && <p className="mt-2 text-muted-foreground" data-testid="processing-plan-alternative">
            {schemaText(schema, 'ui.workspace.processing.plan_waiting_alternative')}
          </p>}
          {checkpointStep && <ReasonHint name={schemaText(schema, 'ui.workspace.processing.plan_continue_checkpoint')} className="mt-3"
            reason={!working && !schema.runtime.http.routes.processing_plan_resume ? reasonText('unavailable') : undefined}>
            <Action type="button"
              loading={working} disabled={working || !schema.runtime.http.routes.processing_plan_resume} onClick={() => void continueCheckpoint()}>
              {schemaText(schema, 'ui.workspace.processing.plan_continue_checkpoint')}
            </Action>
          </ReasonHint>}
          {checkpointStep && <p className="mt-2 text-xs text-muted-foreground" data-testid="processing-plan-checkpoint-note">
            {schemaText(schema, 'ui.workspace.processing.plan_continue_checkpoint_note')}
          </p>}
        </Surface>}
        <h3 className="font-medium">{schemaText(schema, 'ui.workspace.processing.plan_steps')}</h3>
        {availableCapabilities.map((capability) => {
          const step = steps.find((entry) => entry.capability === capability.id);
          const offering = step?.offering;
          const parameters = offering ? panelFields(offering) : [];
          const action = offering ? actionParameter(offering) : undefined;
          const key = offering ? formKey(capability.id, offering.id) : capability.id;
          return <Surface key={capability.id} variant="inset" radius="xl" padding="md">
            <Field id={`${fieldPrefix}-${capability.id}-selected`} label={capability.label_cn} layout="inline">
              {(control) => <ReasonHint name={capability.label_cn} reason={lockReason}>
                <input {...control} type="checkbox" checked={selected.includes(capability.id)}
                  onChange={() => toggle(capability.id)} disabled={working || Boolean(activePlan)} />
              </ReasonHint>}
            </Field>
            {step && <div className="mt-3 space-y-3">
              <ProcessingStepService step={step} fieldId={`${fieldPrefix}-${capability.id}-offering`}
                disabled={working || Boolean(activePlan)} disabledReason={lockReason}
                onChoose={(offeringID) => setChosen((current) => ({ ...current, [capability.id]: offeringID }))} />
              {step.route ? <p className="text-muted-foreground" data-testid={`processing-route-${capability.id}`}>
                {schemaText(schema, 'ui.workspace.processing.route')}: {
                  step.route.sourceRole === 'source_task'
                    ? formatSchemaText(schemaText(schema, 'ui.workspace.processing.task_route'), { provider: step.offering.provider_name })
                    : 'taskExpired' in step.route && step.route.taskExpired
                      ? formatSchemaText(schemaText(schema, 'ui.workspace.processing.asset_route_expired'), { provider: step.offering.provider_name })
                      : schemaText(schema, 'ui.workspace.processing.asset_route')
                }
              </p> : <UnavailableNotice testId={`processing-refused-${capability.id}`}
                {...processingUnavailableNotice(schema, step.blocked || !source || !asset ? { blocked: step.blocked, step: capability.id } : {
                  step: capability.id, size: asset.size,
                  refusals: sourceRefusals(offerings?.[capability.id] ?? [], source, asset),
                  // #326: the step that would come first cannot run on the model either: say why and what works.
                  prerequisite: prerequisiteRefusal(capability.id, availableCapabilities, offerings, source, asset),
                })} />}
              <p data-testid={`processing-quote-${capability.id}`}>{offering ? offeringQuoteText(schema, offering) : schemaText(schema, 'ui.workspace.harness.credits_unknown')}</p>
              {offering && <ReasonHint name={capability.label_cn} reason={lockReason} className="flex w-full flex-col"><SchemaParameters parameters={parameters}
                values={{
                  ...Object.fromEntries(parameters.filter((parameter) => parameter.default !== undefined)
                    .map((parameter) => [parameter.name, parameter.default])),
                  ...budgets[capability.id],
                  ...values[key],
                }}
                onChange={(name, value) => setValues((current) => ({
                  ...current, [key]: { ...current[key], [name]: value },
                }))}
                disabled={working || Boolean(activePlan)} /></ReasonHint>}
              {(ruleViolations[capability.id] ?? []).filter((names) => names.some((name) => parameters.some((parameter) => parameter.name === name))).map((names) => <div key={names.join('|')}>
                {notice(reasonText('input_rule'), `${schemaText(schema, 'ui.workspace.processing.input_rule_violation')}: ${
                  names.map((name) => parameters.find((parameter) => parameter.name === name)?.label ?? name).join(' / ')}`, 'processing-input-rule')}
              </div>)}
              {(missingFields[capability.id] ?? []).length > 0 && <div>
                {notice(reasonText('required_missing'), formatSchemaText(schemaText(schema, 'ui.workspace.processing.required_missing'), {
                  fields: (missingFields[capability.id] ?? []).map((field) => field.label).join(' / ') }), `processing-missing-${capability.id}`)}
              </div>}
              {action && <Field id={`${fieldPrefix}-${capability.id}-action`} label={action.label} required>
                {(control) => <OptionSelect {...control} required
                  value={actions[key] ?? ''}
                  placeholder={schemaText(schema, 'ui.workspace.processing.choose_action')}
                  options={(action.options ?? []).map((option) => ({ value: option.value, label: option.label, group: option.group }))}
                  onValueChange={(value) => setActions((current) => ({ ...current, [key]: value }))}
                  disabled={working || Boolean(activePlan)} />}
              </Field>}
            </div>}
          </Surface>;
        })}
        {selected.length > 0 && steps.some((step) => !step.route)
          && notice(reasonText('plan_incompatible'), schemaText(schema, 'ui.workspace.processing.plan_incompatible'), 'processing-plan-incompatible')}
        {(error || resume.error) && <p role="alert" className="text-destructive">{error || resume.error}</p>}
      </div>
      <footer className="border-t border-border p-3">
        <ProcessingCostSummary steps={steps} />
        {missingSummary.length > 0 && <div className="mb-3 text-xs">
          {notice(reasonText('required_missing'), formatSchemaText(schemaText(schema, 'ui.workspace.processing.required_missing'), { fields: missingSummary.join(' · ') }), 'processing-missing')}
        </div>}
        <ReasonHint name={schemaText(schema, 'ui.workspace.processing.plan_confirm')} reason={submitReason} className="w-full">
          <Action type="button" size="lg" className="w-full"
            loading={working} disabled={!valid} onClick={() => void submit()}>
            {working ? schemaText(schema, 'ui.workspace.processing.submitting') : schemaText(schema, 'ui.workspace.processing.plan_confirm')}
          </Action>
        </ReasonHint>
      </footer>
  </div>;
}
