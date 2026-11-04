import { formatNumber } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatToolCall } from '@/store/chat-store';
import type { MessagePicture } from '@/lib/chat/message-pictures';

/**
 * The read-only model review tools (#103): inspect_model, render_model_views
 * and compare_models. Each stores a compact summary on its tool call; the
 * conversation shows that summary as one line and pairs the rendered pictures
 * with the calls that produced them. Everything here reads the public wire
 * projection; the full report stays with the Agent and is not fetched again.
 */

export type ReviewToolKind = 'inspect' | 'render' | 'compare';

export type ReviewCheckStatus = 'pass' | 'fail' | 'unknown';

export interface ReviewCheckItem {
  /** The Schema check key (triangles, uv, watertight, ...). */
  check: string;
  status: ReviewCheckStatus;
  actual: unknown;
  budget: Record<string, unknown>;
  /** Why the check failed and what can fix it; empty unless it failed. */
  fixHint: string;
}

export interface ReviewUsage {
  purpose: string;
  purposeLabel: string;
  purposeSource: string;
  note: string;
  checks: ReviewCheckItem[];
}

export interface ReviewSummary {
  kind: ReviewToolKind;
  /** The model handle or task ID the call named; empty means the selected model. */
  model: string;
  /** The two models of a comparison; empty otherwise. */
  models: string[];
  /** The Schema view keys that rendered. */
  views: string[];
  /** #103: the attachment handles of the rendered pictures, in the same order as `views`; absent on old replies. */
  handles: string[];
  failedViews: string[];
  triangles: number | null;
  hasUV: boolean | null;
  watertight: boolean | null;
  hasSkin: boolean | null;
  usage: ReviewUsage | null;
}

export interface ReviewCallModel {
  kind: ReviewToolKind;
  summary: ReviewSummary;
  /** The pictures this call produced, in attachment order. */
  pictures: MessagePicture[];
}

export interface ReviewRecord {
  calls: ReviewCallModel[];
  /** Rendered pictures no call could claim; shown so they are never silently dropped. */
  leftover: MessagePicture[];
}

/** A failed inspection item, which is what a round's "发现" lists. */
export function failedReviewChecks(summary: ReviewSummary): ReviewCheckItem[] {
  return summary.usage?.checks.filter((check) => check.status === 'fail') ?? [];
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item !== '') : [];
}

function bool(value: unknown): boolean | null {
  return typeof value === 'boolean' ? value : null;
}

function number(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function parseCheck(value: unknown): ReviewCheckItem | null {
  const item = record(value);
  const check = text(item.check);
  const status = item.status === 'pass' || item.status === 'fail' || item.status === 'unknown' ? item.status : 'unknown';
  if (!check) return null;
  return { check, status, actual: item.actual, budget: record(item.budget), fixHint: text(item.fix_hint) };
}

function parseUsage(value: unknown): ReviewUsage | null {
  const item = record(value);
  const checks = (Array.isArray(item.checks) ? item.checks : [])
    .map(parseCheck)
    .filter((check): check is ReviewCheckItem => check !== null);
  if (checks.length === 0) return null;
  return {
    purpose: text(item.purpose),
    purposeLabel: text(item.purpose_label),
    purposeSource: text(item.purpose_source),
    note: text(item.note),
    checks,
  };
}

/** The tool kind a public tool name stands for, read from the Schema's tool table. */
export function reviewToolKind(schema: ProductSchema, toolName: string): ReviewToolKind | null {
  const tools = schema.chat.model_review?.tools;
  if (!tools || !toolName) return null;
  if (tools.inspect === toolName) return 'inspect';
  if (tools.render === toolName) return 'render';
  if (tools.compare === toolName) return 'compare';
  return null;
}

/** One tool call's compact summary; null when the call is not a review or stored no summary. */
export function reviewSummaryOf(schema: ProductSchema, call: ChatToolCall): ReviewSummary | null {
  const kind = reviewToolKind(schema, call.name);
  if (!kind) return null;
  const item = record(call.summary);
  const reported = item.tool === 'inspect' || item.tool === 'render' || item.tool === 'compare' ? item.tool : kind;
  return {
    kind: reported,
    model: text(item.model),
    models: strings(item.models),
    views: strings(item.views),
    handles: strings(item.handles),
    failedViews: strings(item.failed_views),
    triangles: number(item.triangles),
    hasUV: bool(item.has_uv),
    watertight: bool(item.watertight),
    hasSkin: bool(item.has_skin),
    usage: parseUsage(item.usage),
  };
}

/** The name a summary uses for a reviewed model; the selected model when the call named none. */
export function reviewModelLabel(schema: ProductSchema, model: string): string {
  return model || schemaText(schema, 'ui.chat.review.current_model');
}

export function reviewViewName(schema: ProductSchema, view: string): string {
  return schemaText(schema, `ui.chat.review.view_names.${view}`) || view;
}

export function reviewCheckName(schema: ProductSchema, check: string): string {
  return schemaText(schema, `ui.chat.review.check_names.${check}`) || check;
}

function joined(schema: ProductSchema, values: readonly string[]): string {
  return values.join(schemaText(schema, 'ui.chat.review.item_separator'));
}

/**
 * The facts a one-line inspection reports: triangle count, UVs and
 * watertightness, then the failing checks the purpose judges that those facts
 * do not already state. The line stays short; the details hold the rest.
 */
export function inspectFactItems(schema: ProductSchema, summary: ReviewSummary): string[] {
  const items: string[] = [];
  if (summary.triangles !== null) {
    items.push(formatSchemaText(schemaText(schema, 'ui.chat.review.triangles'), {
      value: formatNumber(summary.triangles, activeLocale(schema), { notation: 'compact', maximumFractionDigits: 1 }),
    }));
  }
  if (summary.hasUV !== null) items.push(schemaText(schema, summary.hasUV ? 'ui.chat.review.uv_ok' : 'ui.chat.review.uv_missing'));
  if (summary.watertight !== null) items.push(schemaText(schema, summary.watertight ? 'ui.chat.review.watertight' : 'ui.chat.review.not_watertight'));
  // A rig is worth naming when it is there; its absence is not a defect for most purposes.
  if (summary.hasSkin === true) items.push(schemaText(schema, 'ui.chat.review.skin_ok'));
  const stated = new Set(['triangles', 'uv', 'watertight', 'skin']);
  const extra = failedReviewChecks(summary).filter((check) => !stated.has(check.check));
  for (const check of extra.slice(0, 2)) {
    items.push(formatSchemaText(schemaText(schema, 'ui.chat.review.check_fail'), { label: reviewCheckName(schema, check.check) }));
  }
  if (extra.length > 2) {
    const count = extra.length - 2;
    items.push(schemaCountMessage(schema, 'ui.chat.review.checks_more', count, { count }));
  }
  return items;
}

/** What one review call says in the conversation: exactly one line. */
export function reviewLine(schema: ProductSchema, summary: ReviewSummary): string {
  if (summary.kind === 'inspect') {
    return formatSchemaText(schemaText(schema, 'ui.chat.review.inspect_line'), {
      model: reviewModelLabel(schema, summary.model),
      items: joined(schema, inspectFactItems(schema, summary)),
    });
  }
  if (summary.kind === 'render') {
    const views = summary.views.length > 0
      ? summary.views.map((view) => reviewViewName(schema, view)).join(schemaText(schema, 'ui.chat.review.views_separator'))
      : schemaCountMessage(schema, 'ui.chat.review.failed_views', summary.failedViews.length, { count: summary.failedViews.length });
    const line = formatSchemaText(schemaText(schema, 'ui.chat.review.render_line'), { model: reviewModelLabel(schema, summary.model), views });
    return withFailedViews(schema, line, summary);
  }
  const models = summary.models.length > 0
    ? summary.models.map((model) => reviewModelLabel(schema, model)).join(schemaText(schema, 'ui.chat.review.views_separator'))
    : reviewModelLabel(schema, '');
  const line = formatSchemaText(schemaText(schema, 'ui.chat.review.compare_line'), { models });
  return withFailedViews(schema, line, summary);
}

function withFailedViews(schema: ProductSchema, line: string, summary: ReviewSummary): string {
  if (summary.views.length === 0 || summary.failedViews.length === 0) return line;
  return line + schemaText(schema, 'ui.chat.review.item_separator')
    + schemaCountMessage(schema, 'ui.chat.review.failed_views', summary.failedViews.length, { count: summary.failedViews.length });
}

/**
 * The review calls of one reply and the pictures they rendered (#103). New
 * summaries name each rendered picture by its attachment handle, in view order;
 * those handles bind pictures even when the attachment order differs or the
 * view count disagrees. Older replies carry no handles: only then do calls
 * claim the next pictures positionally, as many as their view count says.
 * Nothing is claimed twice, and pictures no call names stay in `leftover`.
 */
export function reviewRecordOf(
  schema: ProductSchema,
  calls: readonly ChatToolCall[],
  renders: readonly MessagePicture[],
): ReviewRecord {
  const result: ReviewCallModel[] = [];
  const used = new Set<number>();
  for (const call of calls) {
    const summary = reviewSummaryOf(schema, call);
    if (!summary) continue;
    const pictures: MessagePicture[] = [];
    if (summary.kind !== 'inspect') {
      if (summary.handles.length > 0) {
        for (const handle of summary.handles) {
          const at = renders.findIndex((entry, index) => !used.has(index) && entry.handle === handle);
          if (at < 0) continue;
          used.add(at);
          pictures.push(renders[at]);
        }
      } else {
        const count = summary.views.length;
        for (let at = 0; at < renders.length && pictures.length < count; at += 1) {
          if (used.has(at)) continue;
          used.add(at);
          pictures.push(renders[at]);
        }
      }
    }
    result.push({ kind: summary.kind, summary, pictures });
  }
  return { calls: result, leftover: renders.filter((_entry, index) => !used.has(index)) };
}

export type ReviewRoundResult = { kind: 'ok' } | { kind: 'rest'; count: number } | { kind: 'pending' };

/** One inspect and the renders or comparisons that followed it, until the next inspect. */
export interface ReviewRound {
  /** 1-based, as the Schema's "第 {index} 轮" reads. */
  index: number;
  inspect: ReviewCallModel | null;
  follow: ReviewCallModel[];
  findings: ReviewCheckItem[];
  result: ReviewRoundResult;
}

/**
 * The reply's self-check rounds: each inspect opens a round, later renders and
 * comparisons belong to it, and the next inspect is its recheck. A reply that
 * only renders has one round without findings.
 */
export function reviewRounds(calls: readonly ReviewCallModel[]): ReviewRound[] {
  const rounds: ReviewRound[] = [];
  for (const call of calls) {
    if (call.kind === 'inspect') {
      rounds.push({
        index: rounds.length + 1,
        inspect: call,
        follow: [],
        findings: failedReviewChecks(call.summary),
        result: { kind: 'pending' },
      });
      continue;
    }
    if (rounds.length === 0) {
      rounds.push({ index: 1, inspect: null, follow: [], findings: [], result: { kind: 'pending' } });
    }
    rounds[rounds.length - 1].follow.push(call);
  }
  rounds.forEach((round, index) => {
    const next = rounds[index + 1];
    if (!next?.inspect) return;
    round.result = next.findings.length === 0 ? { kind: 'ok' } : { kind: 'rest', count: next.findings.length };
  });
  return rounds;
}
