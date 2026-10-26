import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import {
  fetchProcessingPlan, isSubmissionUnknown, needsStepConsent, pausedResumableStep, planHoldsModel, processingPlanRoute, resumeButtonLabel,
  resumeProcessingPlan, reconcileProcessingPlan, unreconciledStep, retryableFailedStep, StepChangedError, stepCreditSuffix,
  type PendingStepGrant, type ProcessingPlan, type ProcessingPlanStep,
} from './processing-plan';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

const offering = (overrides: Partial<OfferingWire> = {}) => ({
  id: 'meshy/retexture', revision: 'r1', available: true, estimated_credits: 10, credits_metered: true, provider_name: 'Meshy', ...overrides,
}) as unknown as OfferingWire;

const step = (overrides: Partial<ProcessingPlanStep> = {}): ProcessingPlanStep => ({
  capability: 'retexture_model', offering_id: 'meshy/retexture', revision: 'r1', estimated_credits: 10, state: 'PAUSED', resumable: true,
  failure: 'plan_task_canceled', prepared_input_id: 'pi1', prepared_input_hash: 'h1', plan_step_id: 'ps1', ...overrides,
});

const plan = (overrides: Partial<ProcessingPlan> = {}, steps: ProcessingPlanStep[] = [step()]): ProcessingPlan => ({
  id: 'plan 1', source_task_id: 't1', source_asset_id: 'a1', state: 'PAUSED', version: 3, steps, failure: 'plan_task_canceled', ...overrides,
});

interface Call { url: string; method: string; body: unknown }

/** A fetch that records each request and answers from `answers`, keyed by a method ("GET") or by the end of a URL. */
function recorder(answers: Record<string, { ok?: boolean; status?: number; json?: unknown }> = {}) {
  const calls: Call[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const key = Object.keys(answers).find((candidate) => candidate === method || url.endsWith(candidate));
    const answer = key ? answers[key] : { ok: true, json: {} };
    return {
      ok: answer.ok ?? true,
      status: answer.status ?? (answer.ok === false ? 400 : 200),
      json: async () => answer.json ?? {},
    } as Response;
  });
  return { calls, fetcher: fetcher as unknown as typeof fetch };
}

const offeringsNow = (list: OfferingWire[] = [offering()]) => vi.fn(async () => ({ retexture_model: list }));

describe('read-only reconciliation', () => {
  const locked = () => plan({ failure: 'plan_submit_unreconciled' }, [step({ failure: 'plan_submit_unreconciled', resumable: false })]);
  it('offers reconciliation only for the paused unreconciled step', () => {
    expect(unreconciledStep(locked())).toBeDefined();
    expect(pausedResumableStep(locked())).toBeUndefined();
    expect(unreconciledStep(plan())).toBeUndefined();
    expect(unreconciledStep({ ...locked(), state: 'RUNNING' })).toBeUndefined();
  });
  it.each(['linked', 'cleared'])('returns the persisted %s result with one request and no Task or grant submission', async (kind) => {
    const result = kind === 'linked' ? plan({ state: 'RUNNING', version: 4 }, [step({ task_id: 'landed', state: 'RUNNING' })])
      : plan({ failure: 'plan_reconciled_absent', version: 4 }, [step({ failure: 'plan_reconciled_absent' })]);
    const api = recorder({ '/reconcile': { json: result } });
    await expect(reconcileProcessingPlan(schema, locked(), api.fetcher)).resolves.toEqual(result);
    expect(api.calls).toEqual([{ url: schema.runtime.http.routes.processing_plan_reconcile!.replace('{planID}', 'plan%201'), method: 'POST', body: { expected_plan_version: 3 } }]);
  });
  it('returns actionable Schema error text when the kernel cannot determine the result', async () => {
    const api = recorder({ '/reconcile': { ok: false, status: 409, json: { detail: messages['ui.workspace.processing.plan_reconcile_undetermined'] } } });
    await expect(reconcileProcessingPlan(schema, locked(), api.fetcher)).rejects.toThrow(messages['ui.workspace.processing.plan_reconcile_undetermined']);
    expect(api.calls).toHaveLength(1);
  });
  it('uses actionable Schema text for a network error, rather than exposing the fetch error', async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error('network connection closed'));
    await expect(reconcileProcessingPlan(schema, locked(), fetcher)).rejects.toThrow(messages['ui.workspace.processing.plan_reconcile_undetermined']);
  });
  it('rejects an invalid result instead of overwriting the plan cache', async () => {
    const api = recorder({ '/reconcile': { json: { id: 'other-plan', version: 4, steps: [] } } });
    await expect(reconcileProcessingPlan(schema, locked(), api.fetcher)).rejects.toThrow(messages['ui.workspace.processing.plan_reconcile_undetermined']);
  });
  it('requires a refreshed current offering before resuming a cleared step', async () => {
    const cleared = plan({ failure: 'plan_reconciled_absent' }, [step({ failure: 'plan_reconciled_absent', submission_uncertain: false })]);
    const api = recorder();
    const refresh = offeringsNow([]);
    await expect(resumeProcessingPlan({ schema, plan: cleared, step: cleared.steps[0], offering: offering(), pendingGrant: null, refreshOfferings: refresh, fetcher: api.fetcher }))
      .rejects.toThrow(messages['ui.workspace.processing.plan_offering_changed']);
    expect(refresh).toHaveBeenCalledOnce();
    expect(api.calls).toHaveLength(0);
  });
});

describe('which step of a plan can be continued', () => {
  it('is the paused step that the server marks resumable, and only while the plan is paused', () => {
    expect(pausedResumableStep(plan())?.plan_step_id).toBe('ps1');
    expect(pausedResumableStep(plan({ state: 'RUNNING' }))).toBeUndefined();
    expect(pausedResumableStep(plan({}, [step({ resumable: false })]))).toBeUndefined();
    expect(pausedResumableStep(plan({}, [step({ state: 'SUCCEEDED' })]))).toBeUndefined();
    expect(pausedResumableStep(undefined)).toBeUndefined();
    expect(pausedResumableStep(null)).toBeUndefined();
  });

  it('is the failed step marked retryable, and only while the plan has failed', () => {
    const failed = plan({ state: 'FAILED' }, [step({ state: 'FAILED', retryable: true })]);
    expect(retryableFailedStep(failed)?.plan_step_id).toBe('ps1');
    expect(retryableFailedStep(plan({ state: 'FAILED' }, [step({ state: 'FAILED' })]))).toBeUndefined();
    expect(retryableFailedStep(plan())).toBeUndefined();
  });

  it('knows a step that waits for consent and one whose submission may have been accepted', () => {
    expect(needsStepConsent(step({ failure: 'plan_consent_required' }))).toBe(true);
    expect(needsStepConsent(step())).toBe(false);
    expect(isSubmissionUnknown(step({ failure: 'plan_submit_unknown' }))).toBe(true);
    expect(isSubmissionUnknown(step({ submission_uncertain: true }))).toBe(true);
    expect(isSubmissionUnknown(step())).toBe(false);
  });
});

// #326 P: a plan that only waits for the person to confirm a step has no paid work outstanding, so it does not
// keep the 加工 panel from starting another plan; one that may have a submission outstanding still does.
describe('which plan holds its model in the 加工 panel', () => {
  it('holds while queued, running or paused with paid work possibly outstanding', () => {
    expect(planHoldsModel(plan({ state: 'PENDING' }, [step({ state: 'PENDING', resumable: false, failure: undefined })]))).toBe(true);
    expect(planHoldsModel(plan({ state: 'RUNNING' }, [step({ state: 'RUNNING', resumable: false, failure: undefined })]))).toBe(true);
    expect(planHoldsModel(plan())).toBe(true);
    expect(planHoldsModel(plan({ failure: 'plan_submit_unknown' }, [step({ failure: 'plan_submit_unknown', submission_uncertain: true })]))).toBe(true);
    expect(planHoldsModel(plan({ failure: 'plan_submit_unreconciled' }, [step({ failure: 'plan_submit_unreconciled', resumable: false })]))).toBe(true);
  });
  it('does not hold while it only waits for the person to confirm a step, or once it is over', () => {
    expect(planHoldsModel(plan({ failure: 'plan_consent_required' }, [step({ failure: 'plan_consent_required' })]))).toBe(false);
    expect(planHoldsModel(plan({ failure: 'plan_resume_saved_file' }, [step({ failure: 'plan_resume_saved_file' })]))).toBe(false);
    expect(planHoldsModel(plan({ state: 'FAILED', failure: 'plan_resume_source_expired' },
      [step({ state: 'FAILED', failure: 'plan_resume_source_expired', resumable: false })]))).toBe(false);
    expect(planHoldsModel(plan({ state: 'SUCCEEDED' }, [step({ state: 'SUCCEEDED' })]))).toBe(false);
    expect(planHoldsModel(undefined)).toBe(false);
  });
});

describe('the words on the resume button', () => {
  it('names the credits of the Offering for a plain resume, and the unknown-cost text without one', () => {
    expect(resumeButtonLabel(schema, step(), false, offering())).toBe(`${messages['ui.workspace.processing.plan_resume']} · 10`);
    expect(resumeButtonLabel(schema, step(), false, undefined)).toBe(
      `${messages['ui.workspace.processing.plan_resume']} · ${messages['ui.workspace.processing.plan_credits_unknown']}`,
    );
    expect(stepCreditSuffix(schema, offering())).toBe(' · 10');
    // A provider that does not bill in credits names none.
    expect(stepCreditSuffix(schema, offering({ credits_metered: false }))).toBe('');
    expect(resumeButtonLabel(schema, step(), false, offering({ credits_metered: false }))).toBe(messages['ui.workspace.processing.plan_resume']);
    expect(resumeButtonLabel(schema, step({ native_quote: null }), false, offering())).toBe(
      `${messages['ui.workspace.processing.plan_resume']} · ${messages['ui.pricing.unknown']}`,
    );
  });

  it('asks to prepare the step first and to confirm it second when the step waits for consent', () => {
    const consent = step({ failure: 'plan_consent_required' });
    expect(resumeButtonLabel(schema, consent, false, offering())).toBe(`${messages['ui.workspace.processing.plan_prepare_step']} · 10`);
    expect(resumeButtonLabel(schema, consent, true, offering())).toBe(`${messages['ui.workspace.processing.plan_confirm_step']} · 10`);
  });

  // #324 (from #187 note 98985): the confirmation step showed no price next to 确认本步骤并执行; the amount was
  // only in the estimate before the plan was submitted. Both clicks name the price the step froze.
  it('names the frozen price of the step on both consent clicks', () => {
    const consent = step({ failure: 'plan_consent_required', estimated_credits: 90 });
    const tripo = offering({ estimated_credits: 90 });
    expect(resumeButtonLabel(schema, consent, false, tripo)).toBe(`${messages['ui.workspace.processing.plan_prepare_step']} · 90`);
    // The confirmation approves the input the step froze; preparing it prices it against the current Offering.
    expect(resumeButtonLabel(schema, { ...consent, estimated_credits: 95 }, true, tripo)).toBe(`${messages['ui.workspace.processing.plan_confirm_step']} · 95`);
    expect(resumeButtonLabel(schema, { ...consent, estimated_credits: 95 }, false, tripo)).toBe(`${messages['ui.workspace.processing.plan_prepare_step']} · 90`);
    expect(resumeButtonLabel(schema, consent, true, tripo)).toBe(`${messages['ui.workspace.processing.plan_confirm_step']} · 90`);
    expect(resumeButtonLabel(schema, consent, true, offering({ credits_metered: false }))).toBe(messages['ui.workspace.processing.plan_confirm_step']);
  });

  it('prefers the price the step froze over the Offering estimate', () => {
    expect(stepCreditSuffix(schema, offering({ estimated_credits: 40 }), step({ estimated_credits: 90 }))).toBe(' · 90');
    expect(stepCreditSuffix(schema, offering({ estimated_credits: 40 }))).toBe(' · 40');
  });

  it('continues a step frozen again on the saved file at the current price, with no consent grant yet (#326)', () => {
    const refrozen = step({ failure: 'plan_resume_saved_file', estimated_credits: 10 });
    expect(needsStepConsent(refrozen)).toBe(false);
    expect(resumeButtonLabel(schema, refrozen, false, offering({ estimated_credits: 15 }))).toBe(`${messages['ui.workspace.processing.plan_resume']} · 15`);
  });

  it('asks to check the last submission when its result is unknown', () => {
    expect(resumeButtonLabel(schema, step({ failure: 'plan_submit_unknown' }), false, undefined)).toBe(messages['ui.workspace.processing.plan_reconcile']);
  });
});

describe('where a plan is read', () => {
  it('uses the item route of the Schema with the id escaped', () => {
    expect(processingPlanRoute(schema, 'plan 1')).toBe(schema.runtime.http.routes.processing_plan_item.replace('{planID}', 'plan%201'));
    expect(processingPlanRoute(schema, 'plan 1')).not.toContain('{planID}');
  });

  it('returns the plan the server sent, and rejects an answer that is not a plan', async () => {
    const body = plan();
    const good = recorder({ 'GET': { json: body } });
    await expect(fetchProcessingPlan(schema, 'plan 1', good.fetcher)).resolves.toMatchObject({ id: 'plan 1', version: 3 });
    expect(good.calls[0].url).toBe(processingPlanRoute(schema, 'plan 1'));
    const missing = recorder({ 'GET': { ok: false, status: 404 } });
    await expect(fetchProcessingPlan(schema, 'plan 1', missing.fetcher)).rejects.toThrow('plan 404');
    const strange = recorder({ 'GET': { json: { id: 7 } } });
    await expect(fetchProcessingPlan(schema, 'plan 1', strange.fetcher)).rejects.toThrow('invalid plan');
  });
});

describe('continuing a paused step', () => {
  const resumeUrl = schema.runtime.http.routes.processing_plan_resume.replace('{planID}', 'plan%201');
  const base = (overrides: Partial<Parameters<typeof resumeProcessingPlan>[0]> = {}) => ({
    schema, plan: plan(), step: step(), offering: offering(), pendingGrant: null, refreshOfferings: offeringsNow(), ...overrides,
  });

  it('resumes a plain step against the plan version and the Offering revision the person saw', async () => {
    const { calls, fetcher } = recorder();
    const refreshOfferings = offeringsNow();
    await expect(resumeProcessingPlan(base({ fetcher, refreshOfferings }))).resolves.toEqual({ kind: 'resumed' });
    expect(refreshOfferings).toHaveBeenCalledTimes(1);
    expect(calls).toEqual([{ url: resumeUrl, method: 'POST', body: { expected_plan_version: 3, expected_offering_revision: 'r1' } }]);
  });

  it('refuses to resume when the Offering changed or went away in the meantime, without calling the server', async () => {
    for (const current of [[offering({ revision: 'r2' })], [offering({ available: false })], [offering({ id: 'other' })], []]) {
      const { calls, fetcher } = recorder();
      await expect(resumeProcessingPlan(base({ fetcher, refreshOfferings: offeringsNow(current) })))
        .rejects.toThrow(messages['ui.workspace.processing.plan_offering_changed']);
      expect(calls).toEqual([]);
    }
    const { fetcher } = recorder();
    await expect(resumeProcessingPlan(base({ fetcher, refreshOfferings: vi.fn(async () => undefined) })))
      .rejects.toThrow(messages['ui.workspace.processing.plan_offering_changed']);
  });

  it('reconciles an unknown submission without re-checking the Offering', async () => {
    const { calls, fetcher } = recorder();
    const refreshOfferings = offeringsNow();
    const unknown = step({ failure: 'plan_submit_unknown', revision: 'r0' });
    await resumeProcessingPlan(base({ fetcher, refreshOfferings, step: unknown, offering: undefined }));
    expect(refreshOfferings).not.toHaveBeenCalled();
    expect(calls).toEqual([{ url: resumeUrl, method: 'POST', body: { expected_plan_version: 3, expected_offering_revision: 'r0' } }]);
  });

  describe('a step that waits for consent', () => {
    const consent = step({ failure: 'plan_consent_required' });
    const grantUrl = schema.runtime.http.routes.task_grants;
    const decisionUrl = schema.runtime.http.routes.task_grant_decision.replace('{requestID}', 'req%201');

    it('makes the grant first and waits for a second click to approve it', async () => {
      const { calls, fetcher } = recorder({ [grantUrl]: { json: { request_id: 'req 1', status: 'pending' } } });
      const onGrant = vi.fn();
      const outcome = await resumeProcessingPlan(base({ fetcher, step: consent, onGrant }));
      expect(outcome).toMatchObject({ kind: 'confirm', grant: { requestId: 'req 1', planVersion: 3, preparedInputId: 'pi1', planStepId: 'ps1' } });
      expect(onGrant).toHaveBeenCalledWith(expect.objectContaining({ requestId: 'req 1' }));
      expect(calls).toEqual([{ url: grantUrl, method: 'POST', body: { prepared_input_id: 'pi1', plan_id: 'plan 1', plan_step_id: 'ps1' } }]);
    });

    it('approves the kept grant on the second click, then resumes with its request id', async () => {
      const { calls, fetcher } = recorder();
      const pendingGrant: PendingStepGrant = { requestId: 'req 1', planVersion: 3, preparedInputId: 'pi1', planStepId: 'ps1' };
      await expect(resumeProcessingPlan(base({ fetcher, step: consent, pendingGrant }))).resolves.toEqual({ kind: 'resumed' });
      expect(calls).toEqual([
        { url: decisionUrl, method: 'PUT', body: { decision: 'approve' } },
        { url: resumeUrl, method: 'POST', body: { expected_plan_version: 3, expected_offering_revision: 'r1', authorization_request_id: 'req 1' } },
      ]);
    });

    it('goes straight on when the approval mode already approved the grant', async () => {
      const { calls, fetcher } = recorder({ [grantUrl]: { json: { request_id: 'req 1', status: 'approved', approved_by_policy: true } } });
      await expect(resumeProcessingPlan(base({ fetcher, step: consent }))).resolves.toEqual({ kind: 'resumed' });
      expect(calls.map((call) => call.method + ' ' + (call.url === resumeUrl ? 'resume' : call.url === grantUrl ? 'grant' : 'other')))
        .toEqual(['POST grant', 'POST resume']);
      expect(calls[1].body).toMatchObject({ authorization_request_id: 'req 1' });
    });

    it('does not take a policy flag on a grant that is not approved as approval', async () => {
      const { fetcher } = recorder({ [grantUrl]: { json: { request_id: 'req 1', status: 'pending', approved_by_policy: true } } });
      await expect(resumeProcessingPlan(base({ fetcher, step: consent }))).resolves.toMatchObject({ kind: 'confirm' });
    });

    it('drops a grant made for another plan version or another prepared step', async () => {
      for (const stale of [{ planVersion: 2 }, { preparedInputId: 'other' }, { planStepId: 'other' }]) {
        const { calls, fetcher } = recorder();
        const pendingGrant: PendingStepGrant = { requestId: 'req 1', planVersion: 3, preparedInputId: 'pi1', planStepId: 'ps1', ...stale };
        await expect(resumeProcessingPlan(base({ fetcher, step: consent, pendingGrant }))).rejects.toBeInstanceOf(StepChangedError);
        expect(calls).toEqual([]);
      }
    });

    it('needs the prepared step the server names', async () => {
      const { calls, fetcher } = recorder();
      await expect(resumeProcessingPlan(base({ fetcher, step: step({ failure: 'plan_consent_required', prepared_input_id: undefined }) })))
        .rejects.toThrow('prepared step missing');
      expect(calls).toEqual([]);
    });

    it('reports the server reason when the grant or the approval is refused', async () => {
      const refusedGrant = recorder({ [grantUrl]: { ok: false, status: 409, json: { detail: '已经处理过' } } });
      await expect(resumeProcessingPlan(base({ fetcher: refusedGrant.fetcher, step: consent }))).rejects.toThrow('已经处理过');
      const refusedDecision = recorder({ [decisionUrl]: { ok: false, status: 403, json: {} } });
      const pendingGrant: PendingStepGrant = { requestId: 'req 1', planVersion: 3, preparedInputId: 'pi1', planStepId: 'ps1' };
      await expect(resumeProcessingPlan(base({ fetcher: refusedDecision.fetcher, step: consent, pendingGrant }))).rejects.toThrow('HTTP 403');
      expect(refusedDecision.calls).toHaveLength(1);
    });
  });

  it('reports the server reason (or the status) when the resume itself is refused', async () => {
    const withReason = recorder({ [resumeUrl]: { ok: false, status: 409, json: { detail: '计划版本已变化' } } });
    await expect(resumeProcessingPlan(base({ fetcher: withReason.fetcher }))).rejects.toThrow('计划版本已变化');
    const bare = recorder({ [resumeUrl]: { ok: false, status: 500 } });
    await expect(resumeProcessingPlan(base({ fetcher: bare.fetcher }))).rejects.toThrow('HTTP 500');
  });
});
