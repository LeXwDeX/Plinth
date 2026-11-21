'use client';

import { useMemo } from 'react';
import useSWR from 'swr';
import { ListChecks } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import {
  budgetStateRole, budgetStateWithRole, budgetUnitTotals, budgetUnitsWith, budgetUsageText, stepEstimate,
  type PlanStepStatus, type ProductionPlan, type ProductionPlanStep, type SessionBudget,
} from '@/lib/chat/budget';
import { priceTotalsAmountsText } from '@/lib/chat/approval-modes';
import type { PriceTotal } from '@/lib/pricing';
import { planStepProgress, type PlanTask } from '@/lib/chat/plan-progress';
import { formatSchemaText, schemaCountMessage, schemaText, taskStatePresentation, type ProductSchema } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

/**
 * #103: shown when the plan card cannot be trusted or shown: a pending budget
 * without its approval card, or a failed session re-read after a decision. It
 * says what happened and offers an explicit reload instead of an unusable card.
 */
export function PlanReloadNotice({
  message, busy = false, onReload,
}: {
  message: string;
  busy?: boolean;
  onReload: () => void;
}) {
  const { schema } = useProductSchema();
  return (
    <div
      role="alert"
      className="flex flex-col items-start gap-1.5 rounded-lg border border-border bg-panel-raised p-3 text-xs"
      data-testid="studio-plan-reload"
    >
      <p className="text-destructive [overflow-wrap:anywhere]">{message}</p>
      <Action
        type="button"
        size="sm"
        variant="outline"
        disabled={busy}
        loading={busy}
        onClick={onReload}
        data-testid="studio-plan-reload-button"
      >
        {schemaText(schema, 'ui.studio.plan.reload')}
      </Action>
    </div>
  );
}

export interface PlanCardProps {
  plan: ProductionPlan;
  /**
   * #120: the limits the person approves, one per credit unit and never summed.
   * Unused once `progress` carries the decided plan's own units.
   */
  budgets?: readonly PriceTotal[];
  /** The session's Task projection: each plan step takes the task that ran it. */
  tasks: readonly PlanTask[];
  /** Waiting for a decision: the card shows the approve and decline buttons. */
  onDecide?: (approved: boolean) => void;
  busy?: boolean;
  error?: string | null;
  /** The decided plan's live budget: the card shows spent/limit and each step's task state. */
  progress?: SessionBudget | null;
  /** #251: the server's per-unit totals of the main steps and the declared contingencies. */
  stepTotals?: readonly PriceTotal[];
  contingencyTotals?: readonly PriceTotal[];
  /** #242: compare the finished candidates of one group in the stage compare view. */
  onCompareCandidates?: (taskIds: readonly string[]) => void;
  /** #242: open one candidate on the stage. */
  onSelectCandidate?: (taskId: string) => void;
  /**
   * #403: a step of an approved plan that has no task can still get one (its turn still runs, or a decision it waits
   * for is open). When false such a step reads "not run" instead of "waiting to run". Read from the running-turn
   * projection of the session's messages; a plan that still waits for approval always reads "waiting".
   *
   * #410: only the fallback path reads this. When the server projected the steps
   * (`progress.steps`) the card reads that result and ignores this flag.
   */
  stepsMayStillRun?: boolean;
  /**
   * #410: the plan is still active, its turn has ended and required steps are
   * left. The card shows "stopped, N steps left" and, when this is given, a
   * continue button that starts the plan's turn again. Without it (the top-bar
   * summary) the card shows the same fact without an action.
   */
  onContinue?: () => void;
  continueBusy?: boolean;
}

function capabilityLabel(schema: ReturnType<typeof useProductSchema>['schema'], id: string): string {
  const capability = schema.capabilities.find((item) => item.id === id);
  return capability?.label_cn || capability?.label || id;
}

/**
 * #246: a plan step carries the checkpoint id the server validates; the card
 * shows the label the Product Schema declares for it.
 */
function checkpointLabel(schema: ProductSchema, id: string): string {
  return schema.chat.self_check?.checkpoints?.find((checkpoint) => checkpoint.id === id)?.label || id;
}

/** #251: the review vocabulary's label for a plan check, else its id. */
function planCheckLabel(schema: ProductSchema, id: string): string {
  return schemaText(schema, `ui.chat.review.check_names.${id}`) || id;
}

/**
 * #410: one rendered plan step. `state` is the Task's own state when the caller
 * paired one (an older server), and the server's step state otherwise; `label`
 * is the Schema text for it, `taskId` names the Task the step ran when it has
 * one.
 */
interface PlanRow {
  step: ProductionPlanStep;
  state: string;
  label: string;
  outcome: 'success' | 'failure' | null;
  taskId: string;
  task: PlanTask | null;
  status: PlanStepStatus | null;
}

/**
 * #103: one production plan. While it waits for approval it lists the steps,
 * their models and quotes plus the correction capabilities it allows; once
 * approved the same card shows the live budget and each step's Task state.
 */
export function PlanCard({ plan, budgets = [], tasks, onDecide, busy = false, error = null, progress = null,
  stepTotals = [], contingencyTotals = [], onCompareCandidates, onSelectCandidate, stepsMayStillRun = true,
  onContinue, continueBusy = false }: PlanCardProps) {
  const { schema } = useProductSchema();
  const text = (key: string) => schemaText(schema, key);
  const capabilities = useMemo(
    () => Array.from(new Set(plan.steps.map((step) => step.capability))).sort(),
    [plan],
  );
  const { data: offeringsByCapability } = useSWR<Record<string, OfferingWire[]>>(
    capabilities.length > 0 ? ['plan-card-offerings', schema.revision, capabilities] : null,
    async () => Object.fromEntries(await Promise.all(
      capabilities.map(async (capability) => [capability, await fetchCapabilityOfferings(schema, capability)]),
    )),
    { revalidateOnFocus: false },
  );
  const decidedAt = progress?.decidedAt ?? '';
  // #403: only a plan the person has decided can have steps that were never run; one still waiting for the decision
  // waits for that, not for a turn.
  const decided = progress !== null && budgetStateRole(schema, progress.state) !== 'pending';
  const mayStillRun = decided ? stepsMayStillRun : true;
  // #410: the server pairs every step with the Task that ran it and the card
  // only renders the result. A server that projected no steps (an older one, or
  // one whose projection failed) keeps the browser's own pairing.
  const serverStatuses = progress?.steps ?? [];
  const useServerStatuses = serverStatuses.length === plan.steps.length;
  const stepProgress = useMemo(
    () => (useServerStatuses ? [] : planStepProgress(plan, tasks, decidedAt, mayStillRun)),
    [useServerStatuses, plan, tasks, decidedAt, mayStillRun],
  );
  const rows: PlanRow[] = plan.steps.map((step, index) => {
    const status = useServerStatuses ? serverStatuses[index] : null;
    if (status) {
      return {
        step,
        taskId: status.taskId,
        state: status.state,
        label: text(`ui.studio.plan.step_state.${status.state}`) || text('ui.studio.plan.state.unknown'),
        outcome: status.state === 'done' ? 'success' : status.state === 'failed' ? 'failure' : null,
        task: null,
        status,
      };
    }
    const entry = stepProgress[index];
    const presentation = entry?.task ? taskStatePresentation(schema, entry.task.state) : null;
    const state = entry?.task?.state ?? (entry?.notRun ? 'not_run' : 'pending');
    return {
      step,
      taskId: entry?.task?.taskId ?? '',
      state,
      label: presentation
        ? presentation.label
        : text(entry?.notRun ? 'ui.studio.plan.step_state.not_run' : 'ui.studio.plan.step_state.pending'),
      outcome: presentation?.outcome === 'success' ? 'success' : presentation?.outcome === 'failure' ? 'failure' : null,
      task: entry?.task ?? null,
      status: null,
    };
  });
  // #242: candidate steps are shown as their own parallel groups; every other
  // step keeps the ordinary ordered list.
  const mainRows = rows.filter((row) => (row.step.candidateGroup ?? 0) <= 0);
  const candidateGroups = useMemo(() => {
    const groups: { group: number; rows: PlanRow[] }[] = [];
    for (const row of rows) {
      const group = row.step.candidateGroup ?? 0;
      if (group <= 0) continue;
      const last = groups[groups.length - 1];
      if (last && last.group === group) last.rows.push(row);
      else groups.push({ group, rows: [row] });
    }
    return groups;
  }, [rows]);
  // #242: the heading counts every declared candidate, not the number of groups.
  const candidateCount = candidateGroups.reduce((total, group) => total + group.rows.length, 0);
  // Every amount is listed per credit unit with its label; the card never adds units.
  const limits = progress ? budgetUnitTotals(progress.units, 'limit') : budgets;
  const reserved = progress ? budgetUnitsWith(progress.units, 'reserved') : [];
  const done = rows.filter((row) => row.outcome === 'success').length;
  // #410: a plan is stopped when it may still spend, its turn has ended and
  // required steps are left. An optional step never keeps a plan from being
  // done, so it is not counted here.
  const remaining = rows.filter((row) =>
    !row.step.optional && (row.state === 'not_run' || row.state === 'failed')).length;
  const stopped = progress !== null && budgetStateRole(schema, progress.state) === 'active'
    && !stepsMayStillRun && remaining > 0;
  const modelName = (capability: string, offeringId: string): string => {
    if (!offeringId) return text('ui.studio.plan.model_unspecified');
    return offeringsByCapability?.[capability]?.find((offering) => offering.id === offeringId)?.model_name || offeringId;
  };
  // #103: a state this build's Schema does not name is shown as "unknown", never as pending.
  const stateLabel = progress
    ? text(`ui.studio.plan.state.${progress.state}`) || text('ui.studio.plan.state.unknown')
    : '';
  return (
    <article
      className="flex flex-col gap-1.5 rounded-lg border border-border bg-panel-raised p-3 text-xs"
      data-testid="studio-plan-card"
      data-state={progress?.state ?? budgetStateWithRole(schema, 'pending')}
      data-role={progress ? budgetStateRole(schema, progress.state) ?? '' : 'pending'}
      aria-label={text('ui.studio.plan.title')}
    >
      <header className="flex items-center gap-1.5">
        <ListChecks aria-hidden className="size-4 shrink-0 text-primary" />
        <span className="min-w-0 truncate text-[13px] font-semibold">{plan.title || text('ui.studio.plan.title')}</span>
        {progress ? (
          <span className="ml-auto shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground" data-testid="studio-plan-state">
            {stateLabel}
          </span>
        ) : null}
      </header>
      <p className="tabular-nums text-muted-foreground" data-testid="studio-plan-budget">
        {progress
          ? formatSchemaText(text('ui.studio.plan.progress'), { amounts: budgetUsageText(schema, progress.units) })
          : formatSchemaText(text('ui.studio.plan.budget'), { amounts: priceTotalsAmountsText(schema, limits) })}
      </p>
      {progress ? (
        <p className="flex flex-wrap gap-x-2 text-[11px] tabular-nums text-muted-foreground" data-testid="studio-plan-progress">
          <span>{formatSchemaText(text('ui.studio.plan.remaining'), { amounts: priceTotalsAmountsText(schema, budgetUnitTotals(progress.units, 'remaining')) })}</span>
          {reserved.length > 0 ? <span>{formatSchemaText(text('ui.studio.plan.reserved'), { amounts: priceTotalsAmountsText(schema, budgetUnitTotals(reserved, 'reserved')) })}</span> : null}
          <span>{formatSchemaText(schemaText(schema, 'ui.studio.plan.steps_done'), { done, total: plan.steps.length })}</span>
        </p>
      ) : null}
      {stepTotals.length > 0 || contingencyTotals.length > 0 ? (
        // #251: the approved limit is the worst case; the card separates the
        // main steps from the declared repairs so the usual route never looks
        // as expensive as the worst case.
        <p className="flex flex-wrap gap-x-2 text-[11px] tabular-nums text-muted-foreground" data-testid="studio-plan-estimates">
          {stepTotals.length > 0 ? (
            <span>{formatSchemaText(text('ui.studio.plan.step_total'), { amounts: priceTotalsAmountsText(schema, stepTotals) })}</span>
          ) : null}
          {contingencyTotals.length > 0 ? (
            <span>{formatSchemaText(text('ui.studio.plan.contingency_total'), { amounts: priceTotalsAmountsText(schema, contingencyTotals) })}</span>
          ) : null}
        </p>
      ) : null}
      {mainRows.length > 0 ? (
        <ol className="flex flex-col gap-0.5" aria-label={text('ui.studio.plan.step')}>
          {mainRows.map((row, index) => {
            const estimate = stepEstimate(row.step);
            return (
              <li
                key={`${index}-${row.step.capability}-${row.step.offeringId}`}
                className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-baseline gap-x-2"
                data-testid="studio-plan-step"
                data-capability={row.step.capability}
                data-state={row.state}
              >
                <span className="tabular-nums text-muted-foreground">{index + 1}.</span>
                <HintTooltip name={`${capabilityLabel(schema, row.step.capability)} · ${modelName(row.step.capability, row.step.offeringId)}`} className="block min-w-0 max-w-full">
                  <span className="block truncate">
                    <span>{capabilityLabel(schema, row.step.capability)}</span>
                    <span className="text-muted-foreground"> · {modelName(row.step.capability, row.step.offeringId)}</span>
                  </span>
                </HintTooltip>
                <span className="flex shrink-0 items-baseline gap-1.5 tabular-nums text-muted-foreground">
                  {estimate ? (
                    <span>{formatSchemaText(text('ui.studio.plan.estimated'), { amount: priceTotalsAmountsText(schema, [estimate]) })}</span>
                  ) : null}
                  <span className={cn('text-[11px]', row.outcome === 'success' && 'text-success', row.outcome === 'failure' && 'text-destructive')}>
                    {row.label}
                  </span>
                </span>
                {row.step.purposeBudgetNote ? (
                  // #129: the server says which values of this step are over the purpose's budget.
                  <p className="col-start-2 col-end-4 text-[11px] text-warning [overflow-wrap:anywhere]" data-testid="studio-plan-step-budget-note">
                    {row.step.purposeBudgetNote}
                  </p>
                ) : null}
                {row.step.check ? (
                  // #246: the Schema checkpoint after this step, repeated by the
                  // approved continuation before the next step or delivery.
                  <p className="col-start-2 col-end-4 text-[11px] text-muted-foreground" data-testid="studio-plan-step-check">
                    {formatSchemaText(schemaText(schema, 'ui.studio.plan.checkpoint'), { check: checkpointLabel(schema, row.step.check) })}
                  </p>
                ) : null}
              </li>
            );
          })}
        </ol>
      ) : null}
      {candidateGroups.length > 0 ? (
        <section className="flex flex-col gap-1" data-testid="studio-plan-candidates">
          <span className="text-[11px] text-foreground/70">
            {schemaCountMessage(schema, 'ui.studio.plan.candidates', candidateCount, { count: candidateCount })}
          </span>
          {candidateGroups.map((group) => {
            // #242: only a succeeded candidate can be compared or opened; a
            // running or failed one is still listed with its own state.
            const finished = group.rows.filter((row) => row.outcome === 'success' && row.taskId !== '');
            const canCompare = onCompareCandidates !== undefined && finished.length >= 2;
            const canSelect = onSelectCandidate !== undefined && finished.length > 0;
            return (
              <div
                key={group.group}
                className="flex flex-col gap-0.5 rounded-md border border-border/70 p-1.5"
                data-testid="studio-plan-candidate-group"
                data-group={group.group}
              >
                <ol className="flex flex-col gap-0.5">
                  {group.rows.map((row, index) => {
                    const estimate = stepEstimate(row.step);
                    return (
                      <li
                        key={`${group.group}-${index}-${row.step.offeringId}`}
                        className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-baseline gap-x-2"
                        data-testid="studio-plan-candidate"
                        data-state={row.state}
                      >
                        <span className="tabular-nums text-muted-foreground">
                          {formatSchemaText(text('ui.studio.plan.candidate_n'), { index: row.step.candidateIndex ?? index + 1 })}
                        </span>
                        <span className="min-w-0 truncate">
                          {capabilityLabel(schema, row.step.capability)}
                          <span className="text-muted-foreground"> · {modelName(row.step.capability, row.step.offeringId)}</span>
                        </span>
                        <span className="flex shrink-0 items-baseline gap-1.5 tabular-nums text-muted-foreground">
                          {estimate ? (
                            <span>{formatSchemaText(text('ui.studio.plan.estimated'), { amount: priceTotalsAmountsText(schema, [estimate]) })}</span>
                          ) : null}
                          <span className={cn('text-[11px]', row.outcome === 'success' && 'text-success', row.outcome === 'failure' && 'text-destructive')}>
                            {row.label}
                          </span>
                        </span>
                      </li>
                    );
                  })}
                </ol>
                {canCompare || canSelect ? (
                  <div className="flex flex-wrap items-center gap-1.5">
                    {canCompare ? (
                      <Action
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={() => onCompareCandidates!(finished.map((row) => row.taskId))}
                        data-testid="studio-plan-compare-candidates"
                      >
                        {text('ui.studio.plan.compare_candidates')}
                      </Action>
                    ) : null}
                    {canSelect ? finished.map((row) => (
                      <Action
                        key={row.taskId}
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => onSelectCandidate!(row.taskId)}
                        data-testid="studio-plan-select-candidate"
                        data-task-id={row.taskId}
                      >
                        {text('ui.studio.plan.select_candidate')}
                      </Action>
                    )) : null}
                  </div>
                ) : null}
              </div>
            );
          })}
        </section>
      ) : null}
      {(plan.acceptance?.length ?? 0) > 0 ? (
        <section className="flex flex-col gap-0.5" data-testid="studio-plan-acceptance">
          <span className="text-[11px] text-foreground/70">{text('ui.studio.plan.acceptance')}</span>
          <ul className="flex list-disc flex-col gap-0.5 pl-4 text-[11px] text-muted-foreground">
            {plan.acceptance!.map((criterion) => (
              <li key={criterion.id || criterion.statement}>{criterion.statement}</li>
            ))}
          </ul>
        </section>
      ) : null}
      {(plan.checkpoints?.length ?? 0) > 0 ? (
        <section className="flex flex-col gap-0.5" data-testid="studio-plan-checkpoints">
          <span className="text-[11px] text-foreground/70">{text('ui.studio.plan.checkpoints')}</span>
          <ul className="flex flex-col gap-0.5 text-[11px] text-muted-foreground">
            {plan.checkpoints!.map((checkpoint) => (
              <li key={checkpoint.afterCapability} data-testid="studio-plan-checkpoint">
                {formatSchemaText(text('ui.studio.plan.checkpoint_item'), {
                  capability: capabilityLabel(schema, checkpoint.afterCapability),
                  checks: checkpoint.checks.map((check) => planCheckLabel(schema, check.check)).join(text('ui.studio.approval.card.extra_items_separator')),
                })}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {(plan.contingencies?.length ?? 0) > 0 ? (
        <section className="flex flex-col gap-0.5" data-testid="studio-plan-contingencies">
          <span className="text-[11px] text-foreground/70">
            {formatSchemaText(text('ui.studio.plan.contingencies'), {
              amounts: priceTotalsAmountsText(schema, contingencyTotals.length > 0 ? contingencyTotals
                : plan.contingencies!.map((item) => item.estimate).filter((item): item is PriceTotal => item !== null)),
            })}
          </span>
          <ul className="flex flex-col gap-0.5 text-[11px] text-muted-foreground">
            {plan.contingencies!.map((contingency) => (
              <li key={contingency.id} data-testid="studio-plan-contingency">
                {formatSchemaText(text('ui.studio.plan.contingency_item'), {
                  trigger: contingency.trigger.map((trigger) => planCheckLabel(schema, trigger)).join(text('ui.studio.approval.card.extra_items_separator')),
                  capability: capabilityLabel(schema, contingency.capability),
                  amount: contingency.estimate ? priceTotalsAmountsText(schema, [contingency.estimate]) : '',
                })}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      <p className="text-[11px] text-muted-foreground" data-testid="studio-plan-corrections">
        <span className="text-foreground/70">{text('ui.studio.plan.corrections')}</span>
        {' '}
        {plan.correctionCapabilities.length > 0
          ? plan.correctionCapabilities.map((capability) => capabilityLabel(schema, capability)).join(text('ui.studio.approval.card.extra_items_separator'))
          : text('ui.studio.plan.corrections_none')}
      </p>
      {error ? <p role="alert" className="text-destructive" data-testid="studio-plan-error">{error}</p> : null}
      {stopped ? (
        // #410: the plan may still spend, its turn has ended and required steps
        // are left. This is a display state, not a stored one: continuing
        // starts the plan's turn again and the badge goes back to running.
        <p className="flex flex-wrap items-center gap-1.5 text-[11px] text-warning" data-testid="studio-plan-stopped">
          <span>{formatSchemaText(text('ui.studio.plan.stopped'), { count: remaining })}</span>
          {onContinue ? (
            <Action
              type="button"
              size="sm"
              variant="outline"
              disabled={continueBusy}
              loading={continueBusy}
              onClick={onContinue}
              data-testid="studio-plan-continue"
            >
              {text('ui.studio.plan.continue')}
            </Action>
          ) : null}
        </p>
      ) : null}
      {onDecide ? (
        <footer className="flex items-center gap-1.5">
          <Action type="button" size="sm" disabled={busy} loading={busy} onClick={() => onDecide(true)} data-testid="studio-plan-approve">
            {text('ui.studio.plan.approve')}
          </Action>
          <Action type="button" size="sm" variant="outline" disabled={busy} onClick={() => onDecide(false)} data-testid="studio-plan-deny">
            {text('ui.studio.plan.deny')}
          </Action>
        </footer>
      ) : null}
    </article>
  );
}
