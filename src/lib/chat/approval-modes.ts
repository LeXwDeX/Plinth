import { schemaCountMessage, schemaText, taskStatePresentation, type ProductSchema } from '@/lib/product-schema';
import type { ApprovalMode } from '@/store/chat-store';
import type { TrackedTask } from '@/store/workspace-store';
import { formatPriceTotal, type PriceTotal } from '@/lib/pricing';

export interface ApprovalModeOption {
  mode: ApprovalMode;
  label: string;
  tooltip: string;
}

/** 可选的批准方式、顺序与文案全部来自 Product Schema，不在组件里写死。 */
export function approvalModeOptions(schema: ProductSchema): ApprovalModeOption[] {
  return schema.chat.approval_policy.modes
    .filter((mode): mode is ApprovalMode => mode === 'auto' || mode === 'manual')
    .map((mode) => ({
      mode,
      label: schemaText(schema, `ui.workspace.approval.mode_${mode}`),
      tooltip: schemaText(schema, `ui.workspace.approval.tooltip_${mode}`),
    }));
}

/** 本会话累计估算积分的展示文案；始终带「估算」字样（文案由 Schema 给出）。 */
export function sessionCreditsText(schema: ProductSchema, credits: number): string {
  return schemaCountMessage(schema, 'ui.workspace.approval.session_credits', credits, { credits });
}

/** Server-provided per-unit amounts, legacy credits under their Schema label; unknown when none is valid. */
export function priceTotalsAmountsText(schema: ProductSchema, totals: readonly PriceTotal[]): string {
  const rendered = totals.map((total) => formatPriceTotal(total.unit === 'legacy.credits'
    ? { ...total, unit_label: schemaText(schema, 'ui.pricing.legacy_estimate') }
    : total)).filter((value): value is string => value !== null);
  return rendered.length > 0 ? rendered.join(' · ') : schemaText(schema, 'ui.pricing.unknown');
}

/** Native session totals stay grouped by the server-provided unit. */
export function sessionPriceTotalsText(schema: ProductSchema, totals: readonly PriceTotal[]): string {
  return `${schemaText(schema, 'ui.pricing.totals_heading')}: ${priceTotalsAmountsText(schema, totals)}`;
}

/** 单个付费步骤的积分文案。 */
export function stepCreditsText(schema: ProductSchema, credits: number): string {
  return schemaCountMessage(schema, 'ui.workspace.approval.step_credits', credits, { credits });
}

/** 未完成（失败或取消）的付费步骤文案：积分只是预计，不能读成已扣。 */
export function stepFailedText(schema: ProductSchema, credits: number): string {
  return schemaCountMessage(schema, 'ui.studio.result.step_failed', credits, { credits });
}

/** 该付费步骤是否已确定没有完成（失败、取消或任务已不存在）。 */
export function paidStepFailed(schema: ProductSchema, task: TrackedTask | undefined): boolean {
  if (!task) return false;
  if (task.notFound) return true;
  const outcome = task.state ? taskStatePresentation(schema, task.state).outcome : 'pending';
  if (outcome === 'failure' || outcome === 'canceled') return true;
  return task.terminal && (task.failure !== null || task.liveError !== '');
}
