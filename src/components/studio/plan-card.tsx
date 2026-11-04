'use client';

import { useMemo } from 'react';
import useSWR from 'swr';
import { ListChecks } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import { planEstimatedTotal, type ProductionPlan, type SessionBudget } from '@/lib/chat/budget';
import { completedPlanSteps, planStepProgress, type PlanTask } from '@/lib/chat/plan-progress';
import { formatSchemaText, schemaCountMessage, schemaText, taskStatePresentation } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

export interface PlanCardProps {
  plan: ProductionPlan;
  /** The total budget the person approves; the plan's step quotes when the server sends none. */
  budgetCredits: number;
  /** The session's Task projection: each plan step takes the task that ran it. */
  tasks: readonly PlanTask[];
  /** Waiting for a decision: the card shows the approve and decline buttons. */
  onDecide?: (approved: boolean) => void;
  busy?: boolean;
  error?: string | null;
  /** The decided plan's live budget: the card shows spent/limit and each step's task state. */
  progress?: SessionBudget | null;
}

function capabilityLabel(schema: ReturnType<typeof useProductSchema>['schema'], id: string): string {
  const capability = schema.capabilities.find((item) => item.id === id);
  return capability?.label_cn || capability?.label || id;
}

/**
 * #103: one production plan. While it waits for approval it lists the steps,
 * their models and quotes plus the correction capabilities it allows; once
 * approved the same card shows the live budget and each step's Task state.
 */
export function PlanCard({ plan, budgetCredits, tasks, onDecide, busy = false, error = null, progress = null }: PlanCardProps) {
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
  const stepProgress = useMemo(() => planStepProgress(plan, tasks), [plan, tasks]);
  const total = progress?.limitCredits ?? (budgetCredits > 0 ? budgetCredits : planEstimatedTotal(plan));
  const done = progress
    ? completedPlanSteps(stepProgress, (task) => taskStatePresentation(schema, task.state).outcome === 'success')
    : 0;
  const modelName = (capability: string, offeringId: string): string => {
    if (!offeringId) return text('ui.studio.plan.model_unspecified');
    return offeringsByCapability?.[capability]?.find((offering) => offering.id === offeringId)?.model_name || offeringId;
  };
  const stateLabel = progress ? text(`ui.studio.plan.state.${progress.state}`) : '';
  return (
    <article
      className="flex flex-col gap-1.5 rounded-lg border border-border bg-panel-raised p-3 text-xs"
      data-testid="studio-plan-card"
      data-state={progress?.state ?? 'pending'}
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
          ? schemaCountMessage(schema, 'ui.studio.plan.progress', progress.spentCredits, { spent: progress.spentCredits, limit: progress.limitCredits })
          : schemaCountMessage(schema, 'ui.studio.plan.budget', total, { credits: total })}
      </p>
      {progress ? (
        <p className="flex flex-wrap gap-x-2 text-[11px] tabular-nums text-muted-foreground" data-testid="studio-plan-progress">
          <span>{schemaCountMessage(schema, 'ui.studio.plan.remaining', progress.remainingCredits, { credits: progress.remainingCredits })}</span>
          {progress.reservedCredits > 0 ? <span>{schemaCountMessage(schema, 'ui.studio.plan.reserved', progress.reservedCredits, { credits: progress.reservedCredits })}</span> : null}
          <span>{formatSchemaText(schemaText(schema, 'ui.studio.plan.steps_done'), { done, total: plan.steps.length })}</span>
        </p>
      ) : null}
      {plan.steps.length > 0 ? (
        <ol className="flex flex-col gap-0.5" aria-label={text('ui.studio.plan.step')}>
          {stepProgress.map((entry, index) => {
            const state = entry.task ? taskStatePresentation(schema, entry.task.state) : null;
            return (
              <li
                key={`${index}-${entry.step.capability}-${entry.step.offeringId}`}
                className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-baseline gap-x-2"
                data-testid="studio-plan-step"
                data-capability={entry.step.capability}
                data-state={entry.task?.state ?? 'pending'}
              >
                <span className="tabular-nums text-muted-foreground">{index + 1}.</span>
                <HintTooltip name={`${capabilityLabel(schema, entry.step.capability)} · ${modelName(entry.step.capability, entry.step.offeringId)}`} className="block min-w-0 max-w-full">
                  <span className="block truncate">
                    <span>{capabilityLabel(schema, entry.step.capability)}</span>
                    <span className="text-muted-foreground"> · {modelName(entry.step.capability, entry.step.offeringId)}</span>
                  </span>
                </HintTooltip>
                <span className="flex shrink-0 items-baseline gap-1.5 tabular-nums text-muted-foreground">
                  {entry.step.estimatedCredits > 0 ? (
                    <span>{schemaCountMessage(schema, 'ui.studio.plan.estimated', entry.step.estimatedCredits, { credits: entry.step.estimatedCredits })}</span>
                  ) : null}
                  <span className={cn('text-[11px]', state?.outcome === 'success' && 'text-success', state?.outcome === 'failure' && 'text-destructive')}>
                    {state ? state.label : text('ui.studio.plan.step_pending')}
                  </span>
                </span>
              </li>
            );
          })}
        </ol>
      ) : null}
      <p className="text-[11px] text-muted-foreground" data-testid="studio-plan-corrections">
        <span className="text-foreground/70">{text('ui.studio.plan.corrections')}</span>
        {' '}
        {plan.correctionCapabilities.length > 0
          ? plan.correctionCapabilities.map((capability) => capabilityLabel(schema, capability)).join(text('ui.studio.approval.card.extra_items_separator'))
          : text('ui.studio.plan.corrections_none')}
      </p>
      {error ? <p role="alert" className="text-destructive" data-testid="studio-plan-error">{error}</p> : null}
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
