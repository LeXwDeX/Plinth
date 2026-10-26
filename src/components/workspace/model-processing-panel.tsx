'use client';

import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import { SchemaParameters, schemaParameterValues } from '@/components/form/schema-parameters';
import { useModelProcessingCapabilities } from '@/hooks/use-model-processing-capabilities';
import { useProcessingPlanResume } from '@/hooks/use-processing-plan-resume';
import { offeringFixedValues } from '@/lib/agent-offerings';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { prerequisiteRefusal, processingPrerequisite, processingSourceLabel, processingSourceSteps, actionParameter, panelFields, sourceRefusals, type ProcessingSourceTask } from '@/lib/workspace/processing-source';
import { prerequisiteRefusalText, sourceRefusalText } from '@/lib/workspace/processing-refusals';
import { offeringQuoteText } from '@/lib/workspace/processing-cost';
import { inputRuleViolations, missingRequiredParameters, uploadAssetInputs } from '@/lib/workspace/processing-inputs';
import { uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import { planHoldsModel, reconcileProcessingPlan, unreconciledStep, retryableFailedStep, stepCreditSuffix, type ProcessingPlan } from '@/lib/workspace/processing-plan';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';
import { Action } from '@/components/ui/button';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { Surface } from '@/components/ui/surface';
import { ProcessingCostSummary } from '@/components/workspace/processing-cost-summary';
import { ProcessingStepService } from '@/components/workspace/processing-step-service';

function randomKey() {
  const words = new Uint32Array(4);
  globalThis.crypto.getRandomValues(words);
  return Array.from(words, (word) => word.toString(16).padStart(8, '0')).join('');
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
  const asset = source?.assets.find((candidate) => candidate.id === selection.assetId && candidate.kind === 'model') ?? null;
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
  const stepInputs = useMemo(() => Object.fromEntries(steps.map((step) => {
    const parameters = panelFields(step.offering);
    const defaults = Object.fromEntries(parameters.filter((parameter) => parameter.default !== undefined).map((parameter) => [parameter.name, parameter.default]));
    const key = formKey(step.capability, step.offering.id);
    const input = { ...offeringFixedValues(step.offering), ...schemaParameterValues(parameters, { ...defaults, ...values[key] }) };
    const action = actionParameter(step.offering);
    if (action && actions[key]) input[action.name] = Number(actions[key]);
    return [step.capability, input];
  })), [steps, values, actions]);
  // Required inputs still empty (a reference image, say): confirm stays disabled and the step names them (#321).
  const missingFields = useMemo(() => Object.fromEntries(steps.map((step) => {
    const parameters = panelFields(step.offering);
    const defaults = Object.fromEntries(parameters.filter((parameter) => parameter.default !== undefined).map((parameter) => [parameter.name, parameter.default]));
    return [step.capability, missingRequiredParameters(parameters, { ...defaults, ...values[formKey(step.capability, step.offering.id)] })];
  })), [steps, values]);
  const ruleViolations = useMemo(() => Object.fromEntries(steps.map((step) => [
    step.capability, inputRuleViolations(step.offering, stepInputs[step.capability] ?? {}),
  ])), [steps, stepInputs]);
  const valid = ownedSession && asset && steps.length > 0 && steps.length === selected.length && !working && !activePlan
    && steps.every((step) => step.route && step.offering.available && (!actionParameter(step.offering) || Boolean(actions[formKey(step.capability, step.offering.id)]))
      && (ruleViolations[step.capability] ?? []).length === 0 && (missingFields[step.capability] ?? []).length === 0);
  const missingSummary = steps.filter((step) => (missingFields[step.capability] ?? []).length > 0).map((step) =>
    `${schema.capabilities.find((item) => item.id === step.capability)?.label_cn ?? step.capability}: ${(missingFields[step.capability] ?? []).map((field) => field.label).join(' / ')}`);

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
          {!ownedSession && <p className="text-warning">{schemaText(schema, 'ui.workspace.processing.session_mismatch')}</p>}
          {!asset && <p className="text-warning">{schemaText(schema, 'ui.workspace.processing.asset_unavailable')}</p>}
        </Surface>}
        {currentPlan && <Surface variant="inset" radius="xl" padding="md">
          <p className="font-medium">{schemaText(schema, `ui.workspace.processing.plan_${
            currentPlan.state === 'SUCCEEDED' ? 'done' : currentPlan.state === 'FAILED' ? 'failed'
              : currentPlan.state === 'PAUSED' ? 'paused' : 'running'
          }`)}</p>
          <ol className="mt-2 space-y-1">
            {currentPlan.steps.map((step, index) => <li key={`${step.offering_id}-${index}`} className="flex justify-between gap-2">
              <span>{index + 1}. {schema.capabilities.find((item) => item.id === step.capability)?.label_cn ?? step.capability}</span>
              <span>{schemaText(schema, `ui.workspace.processing.plan_step_${
                step.state === 'SUCCEEDED' ? 'done' : step.state === 'FAILED' ? 'failed' : step.state === 'PAUSED' ? 'paused'
                  : step.state === 'RUNNING' ? 'running' : 'pending'
              }`)}</span>
            </li>)}
          </ol>
          {currentPlan.failure && <p role="alert" className="mt-2 text-warning">
            {schemaText(schema, `ui.workspace.processing.${currentPlan.failure}`)}
          </p>}
          {linkedTaskID && currentPlan.state === 'RUNNING' && currentPlan.steps.some((step) => step.state === 'RUNNING' && step.task_id === linkedTaskID)
            && !currentPlan.failure && <p role="status" className="mt-2 text-muted-foreground">
              {schemaText(schema, 'ui.workspace.processing.plan_reconcile_linked')}
            </p>}
          {needsReconciliation && <Action type="button" className="mt-3"
            loading={working} disabled={working || !schema.runtime.http.routes.processing_plan_reconcile} onClick={() => void reconcile()}>
            {schemaText(schema, 'ui.workspace.processing.plan_reconcile_action')}
          </Action>}
          {currentPlan.state === 'FAILED' && currentPlan.steps.some((step) => step.state === 'FAILED' && step.retryable)
            && <Action type="button" className="mt-3"
              loading={working} disabled={!failedOffering} onClick={() => void retry()}>
              {schemaText(schema, 'ui.workspace.processing.plan_retry')}{stepCreditSuffix(schema, failedOffering)}
            </Action>}
          {resume.step && <Action type="button" className="mt-3"
            loading={working} disabled={!resume.available} onClick={() => { setError(''); void resume.resume(); }}>
            {resume.label}
          </Action>}
          {currentPlan.state === 'PAUSED' && !activePlan && <p className="mt-2 text-muted-foreground" data-testid="processing-plan-alternative">
            {schemaText(schema, 'ui.workspace.processing.plan_waiting_alternative')}
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
              {(control) => <input {...control} type="checkbox" checked={selected.includes(capability.id)}
                onChange={() => toggle(capability.id)} disabled={working || Boolean(activePlan)} />}
            </Field>
            {step && <div className="mt-3 space-y-3">
              <ProcessingStepService step={step} fieldId={`${fieldPrefix}-${capability.id}-offering`}
                disabled={working || Boolean(activePlan)}
                onChoose={(offeringID) => setChosen((current) => ({ ...current, [capability.id]: offeringID }))} />
              <p className="text-muted-foreground" data-testid={`processing-route-${capability.id}`}>
                {schemaText(schema, 'ui.workspace.processing.route')}: {
                  step.route?.sourceRole === 'source_task'
                    ? schemaText(schema, 'ui.workspace.processing.task_route')
                    : step.route && 'taskExpired' in step.route && step.route.taskExpired
                      ? formatSchemaText(schemaText(schema, 'ui.workspace.processing.asset_route_expired'), { provider: step.offering.provider_name })
                      : step.route ? schemaText(schema, 'ui.workspace.processing.asset_route')
                        : schemaText(schema, 'ui.workspace.processing.unavailable')
                }
              </p>
              {!step.route && source && asset && <p role="status" className="text-warning" data-testid={`processing-refused-${capability.id}`}>
                {step.blocked
                  ? schemaText(schema, 'ui.workspace.processing.blocked_by_previous')
                  : [sourceRefusalText(schema, sourceRefusals(offerings?.[capability.id] ?? [], source, asset), asset.size),
                    // #326: the step that would come first cannot run on the model either: say why and what works.
                    prerequisiteRefusalText(schema, prerequisiteRefusal(capability.id, availableCapabilities, offerings, source, asset), capability.id)]
                    .filter(Boolean).join('')}
              </p>}
              <p data-testid={`processing-quote-${capability.id}`}>{offering ? offeringQuoteText(schema, offering) : schemaText(schema, 'ui.workspace.harness.credits_unknown')}</p>
              {offering && <SchemaParameters parameters={parameters}
                values={{
                  ...Object.fromEntries(parameters.filter((parameter) => parameter.default !== undefined)
                    .map((parameter) => [parameter.name, parameter.default])),
                  ...values[key],
                }}
                onChange={(name, value) => setValues((current) => ({
                  ...current, [key]: { ...current[key], [name]: value },
                }))}
                disabled={working || Boolean(activePlan)} />}
              {(ruleViolations[capability.id] ?? []).filter((names) => names.some((name) => parameters.some((parameter) => parameter.name === name))).map((names) => <p key={names.join('|')} role="alert" className="text-warning">
                {schemaText(schema, 'ui.workspace.processing.input_rule_violation')}: {
                  names.map((name) => parameters.find((parameter) => parameter.name === name)?.label ?? name).join(' / ')}
              </p>)}
              {(missingFields[capability.id] ?? []).length > 0 && <p className="text-warning" data-testid={`processing-missing-${capability.id}`}>
                {formatSchemaText(schemaText(schema, 'ui.workspace.processing.required_missing'), {
                  fields: (missingFields[capability.id] ?? []).map((field) => field.label).join(' / ') })}
              </p>}
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
        {selected.length > 0 && steps.some((step) => !step.route) && <p role="alert" className="text-warning">
          {schemaText(schema, 'ui.workspace.processing.plan_incompatible')}
        </p>}
        {(error || resume.error) && <p role="alert" className="text-destructive">{error || resume.error}</p>}
      </div>
      <footer className="border-t border-border p-3">
        <ProcessingCostSummary steps={steps} />
        {missingSummary.length > 0 && <p className="mb-3 text-xs text-warning" data-testid="processing-missing">
          {formatSchemaText(schemaText(schema, 'ui.workspace.processing.required_missing'), { fields: missingSummary.join(' · ') })}
        </p>}
        <Action type="button" size="lg" className="w-full"
          loading={working} disabled={!valid} onClick={() => void submit()}>
          {working ? schemaText(schema, 'ui.workspace.processing.submitting') : schemaText(schema, 'ui.workspace.processing.plan_confirm')}
        </Action>
      </footer>
  </div>;
}
