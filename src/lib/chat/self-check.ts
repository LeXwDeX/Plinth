import { formatNumber } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { reviewViewName } from '@/lib/chat/model-review';

/**
 * #246: the persisted 3D self-check record of one finished result. The server
 * derives the delivery state and the counters; the browser reads the status
 * role from the public Schema and never hard-codes a status id.
 *
 * The record is session data, not a Task projection: a result without a record
 * is unchecked, and its card shows that state without inventing a record.
 */

export interface SelfCheckStatus {
  id: string;
  role: string;
  label: string;
}

export interface SelfCheckMetrics {
  silhouetteIou: number;
  bboxAspectDelta: number;
  dominantColorDistance: number;
  dominantReference: string;
  dominantRender: string;
  referenceSubjectRatio: number;
  renderSubjectRatio: number;
}

export interface SelfCheckCheck {
  round: number;
  kind: string;
  tool: string;
  assetId: string;
  views: string[];
  handles: string[];
  metrics: SelfCheckMetrics | null;
  ok: boolean;
  error: string;
  at: string;
}

export interface SelfCheckConclusion {
  status: string;
  summary: string;
  findings: string[];
  fixes: string[];
  remaining: string[];
  /** The reason a pass was accepted despite failed purpose checks (#281). */
  waiver: string;
  /** The purpose the conclusion was judged against (#281). */
  purposeLabel: string;
  at: string;
}

export interface SelfCheckRecord {
  taskId: string;
  status: string;
  roundsUsed: number;
  roundsLimit: number;
  rendersInRound: number;
  rendersLimit: number;
  correctionsUsed: number;
  correctionsLimit: number;
  checks: SelfCheckCheck[];
  conclusion: SelfCheckConclusion | null;
  updatedAt: string;
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function number(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item !== '') : [];
}

function member(source: Record<string, unknown>, name: string | undefined): unknown {
  return name ? source[name] : undefined;
}

function parseMetrics(value: unknown): SelfCheckMetrics | null {
  const item = record(value);
  if (Object.keys(item).length === 0) return null;
  return {
    silhouetteIou: number(item.silhouette_iou),
    bboxAspectDelta: number(item.bbox_aspect_delta),
    dominantColorDistance: number(item.dominant_color_distance),
    dominantReference: text(item.dominant_reference),
    dominantRender: text(item.dominant_render),
    referenceSubjectRatio: number(item.reference_subject_ratio),
    renderSubjectRatio: number(item.render_subject_ratio),
  };
}

function parseCheck(value: unknown): SelfCheckCheck {
  const item = record(value);
  return {
    round: number(item.round),
    kind: text(item.kind),
    tool: text(item.tool),
    assetId: text(item.asset_id),
    views: strings(item.views),
    handles: strings(item.handles),
    metrics: parseMetrics(item.metrics),
    ok: item.ok === true,
    error: text(item.error),
    at: text(item.at),
  };
}

function parseConclusion(value: unknown): SelfCheckConclusion | null {
  const item = record(value);
  if (item.status === undefined && item.summary === undefined) return null;
  return {
    status: text(item.status),
    summary: text(item.summary),
    findings: strings(item.findings),
    fixes: strings(item.fixes),
    remaining: strings(item.remaining),
    waiver: text(item.waiver),
    purposeLabel: text(item.purpose_label),
    at: text(item.at),
  };
}

/** Reads one session's records from the server's response-field names. */
export function parseSelfChecks(schema: ProductSchema, payload: unknown): SelfCheckRecord[] {
  const fields = schema.chat.api.response_fields;
  const response = record(payload);
  const items = member(response, fields.self_checks);
  if (!Array.isArray(items)) return [];
  const records: SelfCheckRecord[] = [];
  for (const value of items) {
    const item = record(value);
    const taskId = text(member(item, fields.task_id));
    if (!taskId) continue;
    const checks = Array.isArray(member(item, fields.checks))
      ? (member(item, fields.checks) as unknown[]).map(parseCheck)
      : [];
    records.push({
      taskId,
      status: text(member(item, fields.status)),
      roundsUsed: number(member(item, fields.rounds_used)),
      roundsLimit: number(member(item, fields.rounds_limit)),
      rendersInRound: number(member(item, fields.renders_in_round)),
      rendersLimit: number(member(item, fields.renders_limit)),
      correctionsUsed: number(member(item, fields.corrections_used)),
      correctionsLimit: number(member(item, fields.corrections_limit)),
      checks,
      conclusion: parseConclusion(member(item, fields.conclusion)),
      updatedAt: text(member(item, fields.updated_at)),
    });
  }
  return records;
}

/** The declared status by id, then by role; an older/unknown id degrades to unchecked. */
export function selfCheckStatus(schema: ProductSchema, idOrRecord: string | SelfCheckRecord | null | undefined): SelfCheckStatus {
  const id = typeof idOrRecord === 'string' ? idOrRecord : idOrRecord?.status ?? '';
  const statuses = schema.chat.self_check?.statuses ?? [];
  const exact = statuses.find((status) => status.id === id);
  if (exact) return exact;
  const unchecked = statuses.find((status) => status.role === 'unchecked');
  if (unchecked) return unchecked;
  return { id: 'unchecked', role: 'unchecked', label: schemaText(schema, 'ui.chat.self_check.status_unchecked') };
}

/** A record by Task ID; a result without one is unchecked, not a fabricated record. */
export function selfCheckRecordsByTask(records: readonly SelfCheckRecord[]): Map<string, SelfCheckRecord> {
  return new Map(records.map((record) => [record.taskId, record]));
}

export function selfCheckStatusLabel(schema: ProductSchema, record: SelfCheckRecord | null | undefined): string {
  const status = selfCheckStatus(schema, record);
  if (status.label) return status.label;
  if (status.role === 'passed') return schemaText(schema, 'ui.chat.self_check.status_passed');
  if (status.role === 'issues') return schemaText(schema, 'ui.chat.self_check.status_has_issues');
  return schemaText(schema, 'ui.chat.self_check.status_unchecked');
}

export function selfCheckRoundsText(schema: ProductSchema, record: SelfCheckRecord): string {
  return schemaText(schema, 'ui.chat.self_check.rounds')
    .replaceAll('{used}', formatNumber(record.roundsUsed, activeLocale(schema)))
    .replaceAll('{limit}', formatNumber(record.roundsLimit, activeLocale(schema)));
}

export function selfCheckCorrectionsText(schema: ProductSchema, record: SelfCheckRecord): string {
  return schemaText(schema, 'ui.chat.self_check.corrections')
    .replaceAll('{used}', formatNumber(record.correctionsUsed, activeLocale(schema)))
    .replaceAll('{limit}', formatNumber(record.correctionsLimit, activeLocale(schema)));
}

export function selfCheckRendersText(schema: ProductSchema, record: SelfCheckRecord): string {
  return schemaText(schema, 'ui.chat.self_check.renders')
    .replaceAll('{used}', formatNumber(record.rendersInRound, activeLocale(schema)))
    .replaceAll('{limit}', formatNumber(record.rendersLimit, activeLocale(schema)));
}

/** One evidence line: the kind-specific Schema template, with the views it covered. */
export function selfCheckLine(schema: ProductSchema, check: SelfCheckCheck, model: string): string {
  const views = check.views.map((view) => reviewViewName(schema, view)).join(schemaText(schema, 'ui.chat.review.views_separator'));
  const fill = (key: string, values: Record<string, string>): string => {
    let line = schemaText(schema, key);
    for (const [name, value] of Object.entries(values)) line = line.replaceAll(`{${name}}`, value);
    return line;
  };
  switch (check.kind) {
    case 'inspect':
      return fill('ui.chat.self_check.inspect_line', { model });
    case 'render':
      return fill('ui.chat.self_check.render_line', { views });
    case 'compare':
      return fill('ui.chat.self_check.compare_line', { views });
    case 'reference':
      return check.views.length > 0
        ? fill('ui.chat.self_check.reference_line', { view: reviewViewName(schema, check.views[0]) })
        : fill('ui.chat.self_check.check_line', { kind: check.kind, views });
    default:
      return fill('ui.chat.self_check.check_line', { kind: check.kind || check.tool, views });
  }
}

export function selfCheckMetricText(schema: ProductSchema, check: SelfCheckCheck): string[] {
  if (!check.metrics) return [];
  const items: string[] = [];
  const iou = schemaText(schema, 'ui.chat.self_check.reference_metric_iou').replaceAll('{value}', formatNumber(check.metrics.silhouetteIou, activeLocale(schema), { maximumFractionDigits: 4 }));
  if (iou) items.push(iou);
  const color = schemaText(schema, 'ui.chat.self_check.reference_metric_color').replaceAll('{value}', formatNumber(check.metrics.dominantColorDistance, activeLocale(schema), { maximumFractionDigits: 1 }));
  if (color) items.push(color);
  return items;
}

export function selfCheckCaveat(schema: ProductSchema, records: readonly SelfCheckRecord[]): string {
  return records.some((record) => record.checks.some((check) => check.kind === 'reference'))
    ? schemaText(schema, 'ui.chat.self_check.caveat')
    : '';
}

/** The count message the card header uses; the one-form key must exist for {count}. */
export function selfCheckChecksCount(schema: ProductSchema, count: number): string {
  return schemaCountMessage(schema, 'ui.chat.self_check.check_count', count, { count });
}