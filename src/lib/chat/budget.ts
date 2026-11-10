import { formatNativeAmount, parsePriceTotals, type PriceTotal } from '@/lib/pricing';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

/**
 * The production plan and credit budget of a session (#103). The budget is kept
 * per credit unit: each provider unit and the legacy bucket has its own limit,
 * and different units are never added (#120). The server freezes the plan when
 * the person decides; this module only reads the public projection and never
 * recalculates what it shows.
 */

export interface ProductionPlanStep {
  capability: string;
  /** Empty when the plan does not pin a model for the step. */
  offeringId: string;
  purpose: string;
  /** The server's quote for the step at plan time, in the step's own unit; null when none was sent. */
  estimate: PriceTotal | null;
  /**
   * #129: the server's note naming the values this step would run with that are
   * over the session purpose's budget; empty when they fit.
   */
  purposeBudgetNote: string;
  /** #246: the Schema checkpoint declared after this step; empty or absent when none. */
  check?: string;
}

/** #120: one credit unit of a plan budget, as the server's decimal strings. */
export interface BudgetUnit {
  unit: string;
  unitLabel: string;
  limit: string;
  spent: string;
  reserved: string;
  remaining: string;
}

/** A figure of every budget unit. */
export type BudgetFigure = 'limit' | 'spent' | 'reserved' | 'remaining';

/** #251: one final-delivery condition the server derived from the purpose. */
export interface AcceptanceCriterion {
  id: string;
  statement: string;
  check: string;
}

/** #251: one frozen checkpoint check; thresholds are server-side values. */
export interface PlanCheck {
  check: string;
  minimum?: number;
  maximum?: number;
  maximumRatio?: number;
  required?: boolean;
}

/** #251: a checkpoint after one processing capability of the plan. */
export interface PlanCheckpoint {
  afterCapability: string;
  checks: PlanCheck[];
}

/** #251: a declared repair step; its worst-case estimate is in the approved limit. */
export interface PlanContingency {
  id: string;
  trigger: string[];
  capability: string;
  offeringId: string;
  purpose: string;
  estimate: PriceTotal | null;
}

export interface ProductionPlan {
  title: string;
  steps: ProductionPlanStep[];
  correctionCapabilities: string[];
  /** #251: absent on a historical server. */
  acceptance?: AcceptanceCriterion[];
  checkpoints?: PlanCheckpoint[];
  contingencies?: PlanContingency[];
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
  /** One entry per credit unit of the plan, in the server's order. */
  units: BudgetUnit[];
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

/** One server price total, or null when it is malformed. */
function priceTotal(value: unknown): PriceTotal | null {
  return parsePriceTotals([value])?.[0] ?? null;
}

/** A canonical decimal above zero. */
function positive(amount: string): boolean {
  return formatNativeAmount(amount) !== null && /[1-9]/.test(amount);
}

/** The server's per-unit budget figures; a malformed unit is left out rather than shown wrong. */
export function parseBudgetUnits(value: unknown): BudgetUnit[] {
  if (!Array.isArray(value)) return [];
  const units: BudgetUnit[] = [];
  for (const raw of value) {
    const item = record(raw);
    const unit = text(item.unit);
    const unitLabel = text(item.unit_label);
    const figures = (['limit', 'spent', 'reserved', 'remaining'] as const)
      .map((figure) => priceTotal({ unit, unit_label: unitLabel, amount: item[figure] }));
    if (figures.some((figure) => figure === null) || units.some((known) => known.unit === unit)) continue;
    const [limit, spent, reserved, remaining] = figures.map((figure) => figure!.amount);
    units.push({ unit, unitLabel, limit, spent, reserved, remaining });
  }
  return units;
}

/** One figure of every unit as price totals, for the per-unit amount texts. */
export function budgetUnitTotals(units: readonly BudgetUnit[], figure: BudgetFigure): PriceTotal[] {
  return units.map((unit) => ({ unit: unit.unit, unit_label: unit.unitLabel, amount: unit[figure] }));
}

/** The units that hold some amount of the figure, e.g. those with credits reserved. */
export function budgetUnitsWith(units: readonly BudgetUnit[], figure: BudgetFigure): BudgetUnit[] {
  return units.filter((unit) => positive(unit[figure]));
}

/** A unit's label; the legacy bucket reads as the Schema's historical-estimate label. */
export function budgetUnitLabel(schema: ProductSchema, unit: { unit: string; unit_label?: string; unitLabel?: string }): string {
  if (unit.unit === 'legacy.credits') return schemaText(schema, 'ui.pricing.legacy_estimate');
  return unit.unitLabel ?? unit.unit_label ?? unit.unit;
}

/** "spent / limit unit" for every unit, side by side and never summed. */
export function budgetUsageText(schema: ProductSchema, units: readonly BudgetUnit[]): string {
  if (units.length === 0) return schemaText(schema, 'ui.pricing.unknown');
  return units.map((unit) => formatSchemaText(schemaText(schema, 'ui.studio.plan.unit_usage'), {
    spent: formatNativeAmount(unit.spent) ?? unit.spent,
    limit: formatNativeAmount(unit.limit) ?? unit.limit,
    unit: budgetUnitLabel(schema, unit),
  })).join(' · ');
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
    estimate: priceTotal(item.estimate),
    purposeBudgetNote: text(item.purpose_budget_note),
    check: text(item.check),
  };
}

function number(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function parsePlanCheck(value: unknown): PlanCheck | null {
  const item = record(value);
  const check = text(item.check);
  if (!check) return null;
  return {
    check,
    minimum: number(item.minimum),
    maximum: number(item.maximum),
    maximumRatio: number(item.maximum_ratio),
    required: item.required === true ? true : undefined,
  };
}

function parseAcceptance(value: unknown): AcceptanceCriterion[] {
  return (Array.isArray(value) ? value : []).flatMap((entry) => {
    const item = record(entry);
    const statement = text(item.statement);
    if (!statement) return [];
    return [{ id: text(item.id), statement, check: text(item.check) }];
  });
}

function parseCheckpoints(value: unknown): PlanCheckpoint[] {
  return (Array.isArray(value) ? value : []).flatMap((entry) => {
    const item = record(entry);
    const afterCapability = text(item.after_capability);
    const checks = (Array.isArray(item.checks) ? item.checks : [])
      .map(parsePlanCheck)
      .filter((check): check is PlanCheck => check !== null);
    if (!afterCapability || checks.length === 0) return [];
    return [{ afterCapability, checks }];
  });
}

function parseContingencies(value: unknown): PlanContingency[] {
  return (Array.isArray(value) ? value : []).flatMap((entry) => {
    const item = record(entry);
    const id = text(item.id);
    // The public plan JSON nests the repair step as `step` (the same shape as
    // a main step, #251); a flattened shape is also accepted for older or
    // hand-written projections. Reading only the flat fields dropped every
    // real server contingency.
    const step = item.step !== undefined && item.step !== null ? record(item.step) : item;
    const capability = text(step.capability) || text(item.capability);
    if (!id || !capability) return [];
    return [{
      id,
      trigger: strings(item.trigger),
      capability,
      offeringId: text(step.offering_id) || text(item.offering_id),
      purpose: text(step.purpose) || text(item.purpose),
      estimate: priceTotal(step.estimate ?? item.estimate),
    }];
  });
}

export function parseProductionPlan(value: unknown): ProductionPlan | null {
  const item = record(value);
  const steps = (Array.isArray(item.steps) ? item.steps : [])
    .map(parseProductionPlanStep)
    .filter((step): step is ProductionPlanStep => step !== null);
  if (steps.length === 0 && !text(item.title)) return null;
  const acceptance = parseAcceptance(item.acceptance);
  const checkpoints = parseCheckpoints(item.checkpoints);
  const contingencies = parseContingencies(item.contingencies);
  return {
    title: text(item.title),
    steps,
    correctionCapabilities: strings(item.correction_capabilities),
    // #251: a historical server sends none, so the parsed plan keeps its old
    // shape and the card renders no empty sections.
    ...(acceptance.length > 0 ? { acceptance } : {}),
    ...(checkpoints.length > 0 ? { checkpoints } : {}),
    ...(contingencies.length > 0 ? { contingencies } : {}),
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
    units: parseBudgetUnits(item.units),
    plan,
    createdAt: text(item.created_at),
    decidedAt: text(item.decided_at),
  };
}

/** A step quote worth showing: present and above zero. */
export function stepEstimate(step: ProductionPlanStep): PriceTotal | null {
  return step.estimate && positive(step.estimate.amount) ? step.estimate : null;
}

/**
 * A budget that can still spend: only a plan whose state has the active role,
 * with credits left in at least one of its units.
 */
export function budgetIsSpendable(schema: ProductSchema, budget: SessionBudget | null | undefined): budget is SessionBudget {
  return budget != null && budgetStateRole(schema, budget.state) === 'active' && budgetUnitsWith(budget.units, 'remaining').length > 0;
}
