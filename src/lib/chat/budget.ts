import type { ProductSchema } from '@/lib/product-schema';

/**
 * The production plan and total credit budget of a session (#103). The server
 * freezes the plan when the person decides; this module only reads the public
 * projection and never recalculates what it shows.
 */

export interface ProductionPlanStep {
  capability: string;
  /** Empty when the plan does not pin a model for the step. */
  offeringId: string;
  purpose: string;
  /** The server's quote for the step at plan time. */
  estimatedCredits: number;
}

export interface ProductionPlan {
  title: string;
  steps: ProductionPlanStep[];
  correctionCapabilities: string[];
}

/**
 * #103: the state stays an open string. Which states exist is product Schema
 * data (`chat.approval_policy.budget_states`), and a newer server may know one
 * this client build does not; it must still be shown instead of being folded
 * into "pending".
 */
export type BudgetState = string;

export interface SessionBudget {
  id: string;
  state: BudgetState;
  limitCredits: number;
  spentCredits: number;
  reservedCredits: number;
  remainingCredits: number;
  plan: ProductionPlan;
  createdAt: string;
  decidedAt: string;
}

/** The budget states the Schema declares; empty on older servers that do not project them. */
export function budgetStates(schema: ProductSchema): readonly string[] {
  return schema.chat.approval_policy.budget_states ?? [];
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function count(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item !== '') : [];
}

export function parseProductionPlanStep(value: unknown): ProductionPlanStep | null {
  const item = record(value);
  const capability = text(item.capability);
  if (!capability) return null;
  return {
    capability,
    offeringId: text(item.offering_id),
    purpose: text(item.purpose),
    estimatedCredits: count(item.estimated_credits),
  };
}

export function parseProductionPlan(value: unknown): ProductionPlan | null {
  const item = record(value);
  const steps = (Array.isArray(item.steps) ? item.steps : [])
    .map(parseProductionPlanStep)
    .filter((step): step is ProductionPlanStep => step !== null);
  if (steps.length === 0 && !text(item.title)) return null;
  return {
    title: text(item.title),
    steps,
    correctionCapabilities: strings(item.correction_capabilities),
  };
}

export function parseSessionBudget(_schema: ProductSchema, value: unknown): SessionBudget | null {
  const item = record(value);
  const id = text(item.id);
  const plan = parseProductionPlan(item.plan);
  if (!id || !plan) return null;
  // Only a missing state falls back; a state this build does not know is preserved.
  const state = text(item.state) || 'pending';
  return {
    id,
    state,
    limitCredits: count(item.limit_credits),
    spentCredits: count(item.spent_credits),
    reservedCredits: count(item.reserved_credits),
    remainingCredits: count(item.remaining_credits),
    plan,
    createdAt: text(item.created_at),
    decidedAt: text(item.decided_at),
  };
}

/** The sum of the plan's own step quotes; the server's estimate total is authoritative when present. */
export function planEstimatedTotal(plan: ProductionPlan): number {
  return plan.steps.reduce((total, step) => total + step.estimatedCredits, 0);
}

/** A budget that can still spend: only an approved, unfinished plan with credits left. */
export function budgetIsSpendable(budget: SessionBudget | null | undefined): budget is SessionBudget {
  return budget != null && budget.state === 'active' && budget.remainingCredits > 0;
}
