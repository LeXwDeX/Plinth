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

/**
 * #120: what a plan state means to the browser, declared by the Schema next to
 * the state id: `pending` waits for the person's decision, `active` may spend,
 * `ended` is decided and finished but still shown, `denied` was refused and is
 * not shown. The browser acts on the role, never on a state id.
 */
export type BudgetStateRole = 'pending' | 'active' | 'ended' | 'denied';

export interface BudgetStateDeclaration {
  id: string;
  role: BudgetStateRole;
}

const budgetStateRoles: readonly string[] = ['pending', 'active', 'ended', 'denied'];

function isBudgetStateRole(value: unknown): value is BudgetStateRole {
  return typeof value === 'string' && budgetStateRoles.includes(value);
}

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

/**
 * The budget states the Schema declares with their roles; empty on older servers
 * that do not project them. A declaration without a known role is left out, so
 * its state reads as unknown rather than as any role.
 */
export function budgetStates(schema: ProductSchema): readonly BudgetStateDeclaration[] {
  const declared = schema.chat.approval_policy.budget_states;
  if (!Array.isArray(declared)) return [];
  return declared.flatMap((item) => {
    const entry = record(item);
    const id = text(entry.id);
    return id && isBudgetStateRole(entry.role) ? [{ id, role: entry.role }] : [];
  });
}

/** The Schema role of a plan state; null for a state the Schema does not declare. */
export function budgetStateRole(schema: ProductSchema, state: BudgetState): BudgetStateRole | null {
  return budgetStates(schema).find((item) => item.id === state)?.role ?? null;
}

/** The state id the Schema gives the role, or '' when it declares none. */
export function budgetStateWithRole(schema: ProductSchema, role: BudgetStateRole): BudgetState {
  return budgetStates(schema).find((item) => item.role === role)?.id ?? '';
}

/**
 * A plan the session shows (top-bar badge, plan area): any plan except a refused
 * one. A state the Schema does not declare is still shown, never hidden.
 */
export function budgetIsShown(schema: ProductSchema, budget: SessionBudget | null | undefined): budget is SessionBudget {
  return budget != null && budgetStateRole(schema, budget.state) !== 'denied';
}

/** A plan that still waits for the person's decision. */
export function budgetAwaitsDecision(schema: ProductSchema, budget: SessionBudget | null | undefined): boolean {
  return budget != null && budgetStateRole(schema, budget.state) === 'pending';
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

export function parseSessionBudget(schema: ProductSchema, value: unknown): SessionBudget | null {
  const item = record(value);
  const id = text(item.id);
  const plan = parseProductionPlan(item.plan);
  if (!id || !plan) return null;
  // Only a missing state falls back, to the Schema's waiting state; a state this
  // build does not know is preserved.
  const state = text(item.state) || budgetStateWithRole(schema, 'pending');
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

/** A budget that can still spend: only a plan whose state has the active role, with credits left. */
export function budgetIsSpendable(schema: ProductSchema, budget: SessionBudget | null | undefined): budget is SessionBudget {
  return budget != null && budgetStateRole(schema, budget.state) === 'active' && budget.remainingCredits > 0;
}
