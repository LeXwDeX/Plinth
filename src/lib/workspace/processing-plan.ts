import { offeringShowsCredits, type OfferingWire } from '@/lib/agent-offerings';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { formatNativePrice, parseNativeQuote, type NativeQuote } from '@/lib/pricing';
import { withRequestReference } from '@/lib/problem-reference';

/**
 * The processing plan as the server's list and item routes return it, and the one way to continue a paused
 * step of it. The 加工 tab and the 格式 tab both resume a plan through `resumeProcessingPlan`, so the consent
 * grant, the revision checks and the request body exist once.
 */
export interface ProcessingPlanStep {
  capability: string;
  offering_id: string;
  revision: string;
  estimated_credits?: number;
  native_quote?: NativeQuote | null;
  state: string;
  task_id?: string;
  failure?: string;
  retryable?: boolean;
  resumable?: boolean;
  submission_uncertain?: boolean;
  prepared_input_id?: string;
  prepared_input_hash?: string;
  plan_step_id?: string;
  idempotency_key?: string;
}

export interface ProcessingPlan {
  id: string;
  source_task_id: string;
  source_asset_id: string;
  state: string;
  version: number;
  steps: ProcessingPlanStep[];
  failure?: string;
}

/** The paused step a person may continue; none while the plan is not paused or its pause cannot be continued. */
export function pausedResumableStep(plan: ProcessingPlan | null | undefined): ProcessingPlanStep | undefined {
  return plan?.state === 'PAUSED' ? plan.steps.find((step) => step.state === 'PAUSED' && step.resumable) : undefined;
}

/** The failed step a person may retry; none while the plan has not failed. */
export function retryableFailedStep(plan: ProcessingPlan | null | undefined): ProcessingPlanStep | undefined {
  return plan?.state === 'FAILED' ? plan.steps.find((step) => step.state === 'FAILED' && step.retryable) : undefined;
}

/**
 * Pauses that only wait for the person to confirm one step at its price: nothing was submitted and no approved
 * grant holds the step (the server freezes a step again on the saved file before asking, #326).
 */
const WAITING_FOR_PERSON = new Set(['plan_consent_required', 'plan_resume_saved_file']);

/**
 * Whether a plan holds its model in the 加工 panel, so no other plan can be started there: while it is queued
 * or running, and while it is paused with paid work possibly outstanding. A plan that only waits for the person
 * to confirm a step holds nothing (#326 P): they may continue it or start another plan instead. A paused plan the
 * server judged unable to continue is shown FAILED and holds nothing either.
 */
export function planHoldsModel(plan: ProcessingPlan | null | undefined): boolean {
  if (!plan) return false;
  if (plan.state === 'PENDING' || plan.state === 'RUNNING') return true;
  if (plan.state !== 'PAUSED') return false;
  return !plan.steps.some((step) => step.state === 'PAUSED' && WAITING_FOR_PERSON.has(step.failure ?? ''));
}

/** A locked submission can only be resolved through the read-only reconcile endpoint. */
export function unreconciledStep(plan: ProcessingPlan | null | undefined): ProcessingPlanStep | undefined {
  return plan?.state === 'PAUSED' ? plan.steps.find((step) => step.state === 'PAUSED' && step.failure === 'plan_submit_unreconciled') : undefined;
}

/** Reconciliation never prepares, approves or submits a Task. The returned plan is the persisted result. */
export async function reconcileProcessingPlan(schema: ProductSchema, plan: ProcessingPlan, fetcher: typeof fetch = (input, init) => fetch(input, init)): Promise<ProcessingPlan> {
  const route = schema.runtime.http.routes.processing_plan_reconcile;
  const undetermined = () => new Error(schemaText(schema, 'ui.workspace.processing.plan_reconcile_undetermined'));
  if (!route) throw undetermined();
  let response: Response;
  try {
    response = await fetcher(route.replace('{planID}', encodeURIComponent(plan.id)), {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ expected_plan_version: plan.version }),
    });
  } catch { throw undetermined(); }
  if (!response.ok) {
    const body: { detail?: string } = await response.json().catch(() => ({}));
    throw typeof body?.detail === 'string' && body.detail ? new Error(withRequestReference(body.detail, body)) : undetermined();
  }
  const updated: unknown = await response.json().catch(() => undefined);
  if (!updated || typeof updated !== 'object' || (updated as ProcessingPlan).id !== plan.id
    || typeof (updated as ProcessingPlan).version !== 'number' || !Array.isArray((updated as ProcessingPlan).steps)) throw undetermined();
  return updated as ProcessingPlan;
}

/** The earlier submission may have been accepted upstream: continuing reconciles it instead of re-checking the Offering. */
export function isSubmissionUnknown(step: ProcessingPlanStep): boolean {
  return step.submission_uncertain === true || step.failure === 'plan_submit_unknown';
}

/** A step that waits for the person's consent to this one paid step before it may run. */
export function needsStepConsent(step: ProcessingPlanStep): boolean {
  return step.failure === 'plan_consent_required';
}

/**
 * The consent grant made for one step and the plan version it was made against. It is kept between the two
 * clicks (prepare, then confirm) and dropped when the plan or the step changed under it.
 */
export interface PendingStepGrant {
  requestId: string;
  planVersion: number;
  preparedInputId: string;
  planStepId: string;
  approvedByPolicy?: boolean;
}

/**
 * What a retry, resume or confirm button names for the step's credits: the current Offering estimate (a
 * resume or retry is priced again against it), or, when `step` is given, the price that step froze (the
 * consent being confirmed approves exactly that input, #324); nothing for a provider that does not bill in
 * credits.
 */
export function stepCreditSuffix(schema: ProductSchema, offering: OfferingWire | undefined, step?: ProcessingPlanStep): string {
  if (offering?.price_mode === 'native') {
    const quote = parseNativeQuote(offering.default_native_quote);
    return ` · ${quote ? `${schemaText(schema, 'ui.pricing.default_estimate').replace('{amount}', formatNativePrice(quote.total_amount, quote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown'))}` : schemaText(schema, 'ui.pricing.unknown')}`;
  }
  return offering && !offeringShowsCredits(offering)
    ? ''
    : ` · ${(offering ? step?.estimated_credits : undefined) ?? offering?.estimated_credits ?? schemaText(schema, 'ui.workspace.processing.plan_credits_unknown')}`;
}

/** The words on the resume button for a paused step; `confirming` is true once its consent grant exists. */
export function resumeButtonLabel(schema: ProductSchema, step: ProcessingPlanStep, confirming: boolean, offering: OfferingWire | undefined): string {
  if (isSubmissionUnknown(step)) return schemaText(schema, 'ui.workspace.processing.plan_reconcile');
  const hasNativeQuote = Object.hasOwn(step, 'native_quote');
  const quote = parseNativeQuote(step.native_quote);
  const suffix = hasNativeQuote
    ? ` · ${quote
      ? schemaText(schema, 'ui.pricing.frozen_estimate').replace('{amount}', formatNativePrice(quote.total_amount, quote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown'))
      : schemaText(schema, 'ui.pricing.unknown')}`
    : stepCreditSuffix(schema, offering, needsStepConsent(step) && confirming ? step : undefined);
  // A step that waits for consent names its price on both clicks, so the confirmation shows what it approves (#324).
  if (needsStepConsent(step)) {
    return `${schemaText(schema, confirming ? 'ui.workspace.processing.plan_confirm_step' : 'ui.workspace.processing.plan_prepare_step')}${suffix}`;
  }
  return `${schemaText(schema, 'ui.workspace.processing.plan_resume')}${suffix}`;
}

/** The pending consent grant no longer matches the plan or the step: the person has to start the confirmation again. */
export class StepChangedError extends Error {
  constructor() {
    super('processing step changed');
    this.name = 'StepChangedError';
  }
}

export interface ResumeRequest {
  schema: ProductSchema;
  plan: ProcessingPlan;
  /** The step to continue (`pausedResumableStep(plan)`). */
  step: ProcessingPlanStep;
  /** The step's Offering as the person saw it, when it can run now; not needed to reconcile an unknown submission. */
  offering: OfferingWire | undefined;
  pendingGrant: PendingStepGrant | null;
  /** Reads the Offerings again, so a resume confirms the cost against what the server offers now. */
  refreshOfferings: () => Promise<Record<string, OfferingWire[]> | undefined>;
  /** Called as soon as a consent grant exists, so the caller keeps it even when a later request fails. */
  onGrant?: (grant: PendingStepGrant) => void;
  fetcher?: typeof fetch;
}

export type ResumeOutcome =
  /** A consent grant was made but not yet approved: the person confirms with a second click. */
  | { kind: 'confirm'; grant: PendingStepGrant }
  | { kind: 'resumed' };

async function problem(response: Response): Promise<Error> {
  const body: { detail?: string } = await response.json().catch(() => ({}));
  return new Error(withRequestReference(body.detail || `HTTP ${response.status}`, body));
}

/**
 * Continues a paused plan step. A step that waits for consent first gets a task grant (the policy may approve
 * it at once; otherwise this returns `confirm` and the next call approves it with the grant kept in
 * `pendingGrant`). Before the plan is resumed, the Offering is read again and must still be available at the
 * revision the person saw, so a delayed click cannot authorise another cost. Throws an Error whose message is
 * the server's reason; `StepChangedError` means the pending grant must be dropped.
 */
export async function resumeProcessingPlan(request: ResumeRequest): Promise<ResumeOutcome> {
  const { schema, plan, step, offering, refreshOfferings } = request;
  const fetcher = request.fetcher ?? ((input: RequestInfo | URL, init?: RequestInit) => fetch(input, init));
  const routes = schema.runtime.http.routes;
  const consentStep = needsStepConsent(step);
  const submissionUnknown = isSubmissionUnknown(step);
  let grant = request.pendingGrant;
  if (consentStep && (!step.prepared_input_id || !step.plan_step_id)) throw new Error('prepared step missing');
  if (consentStep && !grant) {
    const response = await fetcher(routes.task_grants, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prepared_input_id: step.prepared_input_id, plan_id: plan.id, plan_step_id: step.plan_step_id }),
    });
    if (!response.ok) throw await problem(response);
    const created: { request_id: string; status?: string; approved_by_policy?: boolean } = await response.json();
    const approvedByPolicy = created.approved_by_policy === true && created.status === 'approved';
    grant = {
      requestId: created.request_id,
      planVersion: plan.version,
      preparedInputId: step.prepared_input_id!,
      planStepId: step.plan_step_id!,
      ...(approvedByPolicy ? { approvedByPolicy: true } : {}),
    };
    request.onGrant?.(grant);
    // A step the approval mode already approved needs no second click; otherwise the person confirms next.
    if (!approvedByPolicy) return { kind: 'confirm', grant };
  }
  if (consentStep && grant) {
    if (grant.planVersion !== plan.version || grant.preparedInputId !== step.prepared_input_id || grant.planStepId !== step.plan_step_id) {
      throw new StepChangedError();
    }
    if (!grant.approvedByPolicy) {
      const decisionRoute = routes.task_grant_decision.replace('{requestID}', encodeURIComponent(grant.requestId));
      const decision = await fetcher(decisionRoute, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ decision: 'approve' }),
      });
      if (!decision.ok) throw await problem(decision);
    }
  }
  if (!submissionUnknown) {
    const current = await refreshOfferings();
    if (!current?.[step.capability]?.some((candidate) =>
      candidate.id === offering?.id && candidate.available && candidate.revision === offering?.revision)) {
      throw new Error(schemaText(schema, 'ui.workspace.processing.plan_offering_changed'));
    }
  }
  const route = routes.processing_plan_resume.replace('{planID}', encodeURIComponent(plan.id));
  const response = await fetcher(route, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      expected_plan_version: plan.version,
      expected_offering_revision: offering?.revision ?? step.revision,
      authorization_request_id: consentStep ? grant?.requestId : undefined,
    }),
  });
  if (!response.ok) throw await problem(response);
  return { kind: 'resumed' };
}

/** The route of one plan (`GET`), for a caller that knows only the plan's id. */
export function processingPlanRoute(schema: ProductSchema, planId: string): string {
  return schema.runtime.http.routes.processing_plan_item.replace('{planID}', encodeURIComponent(planId));
}

/** Reads one plan by id; the item route returns the plan itself. */
export async function fetchProcessingPlan(schema: ProductSchema, planId: string, fetcher: typeof fetch = (input, init) => fetch(input, init)): Promise<ProcessingPlan> {
  const response = await fetcher(processingPlanRoute(schema, planId));
  if (!response.ok) throw new Error(`plan ${response.status}`);
  const plan: unknown = await response.json();
  if (plan === null || typeof plan !== 'object' || typeof (plan as { id?: unknown }).id !== 'string' || !Array.isArray((plan as { steps?: unknown }).steps)) {
    throw new Error('invalid plan');
  }
  return plan as ProcessingPlan;
}
