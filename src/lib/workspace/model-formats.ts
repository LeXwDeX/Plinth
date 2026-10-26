/**
 * 3D 任务的交付格式清单（GET /api/v1/tasks/{taskID}/formats）的前端投影。
 *
 * 清单由服务端按 Product Schema 的 model_delivery 计算：哪些格式已有本地
 * 文件、哪些可由哪个供给项补齐（含预计积分）、哪些不可得及原因。这里只做
 * 防御式解析与路由拼装，不持有格式表、转换关系或原因文案。
 */

import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { parseNativeQuote, parsePriceTotals, type NativeQuote, type PriceTotal } from '@/lib/pricing';

export type ModelFormatStatus = 'available' | 'obtainable' | 'converting' | 'unavailable';
/** A listed format whose state the formats API has not (yet) reported. */
export type ModelFormatRowStatus = ModelFormatStatus | 'unknown';

export interface ModelFormatRow {
  id: string;
  label: string;
  status: ModelFormatRowStatus;
  taskId: string;
  assetId: string;
  /** The converting Offering's provider: its id, name and short label (obtainable and converting rows). */
  providerId: string;
  providerName: string;
  providerShortLabel: string;
  estimatedCredits: number;
  /** The Offering's own credits_metered declaration. */
  creditsMetered: boolean;
  nativeQuote?: NativeQuote | null;
  reasonText: string;
}

export interface ModelFormatStep {
  offeringId: string;
  capability: string;
  /** Who runs the conversion, as the rows name it (conversionProviderLabel). */
  providerId: string;
  providerName: string;
  providerShortLabel: string;
  formats: string[];
  estimatedCredits: number;
  state: string;
  planId: string;
  planVersion: number;
  offeringRevision: string;
  failure: string;
  resultTaskId: string;
  nativeQuote?: NativeQuote | null;
  /** A failed step whose provider already finished and billed the run (#330): the credits it charged. */
  chargedCredits: number;
  /** The free refetch (#295) of a failed step's charged result; null when there is nothing to fetch. */
  refetch: ModelFormatStepRefetch | null;
  /** The plan failure of an earlier run that could not be retried; this step runs again as a new conversion (#330). */
  previousFailure: string;
  /** A failed step whose retry is not possible (its outcome is unknown, a new run could charge twice). */
  retryBlocked: boolean;
}

export interface ModelFormatStepRefetch {
  eligible: boolean;
  reason: string;
  /** The refetch Task already running or done ('' when none). */
  taskId: string;
  state: string;
  /** Refetches that failed or were canceled. */
  failed: number;
}

function parseStepRefetch(raw: unknown): ModelFormatStepRefetch | null {
  if (!isRecord(raw) || typeof raw.eligible !== 'boolean') return null;
  return { eligible: raw.eligible, reason: text(raw.reason), taskId: text(raw.task_id), state: text(raw.state), failed: count(raw.failed) };
}

export interface ModelFormatCoverage {
  taskId: string;
  applicable: boolean;
  formats: ModelFormatRow[];
  steps: ModelFormatStep[];
  completionCredits: number;
  canComplete: boolean;
  archiveAvailable: boolean;
  priceTotals?: PriceTotal[];
}

const statuses: ReadonlySet<string> = new Set(['available', 'obtainable', 'converting', 'unavailable']);

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function count(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0;
}

export function parseModelFormatCoverage(raw: unknown): ModelFormatCoverage | null {
  if (!isRecord(raw) || typeof raw.task_id !== 'string') return null;
  const formats: ModelFormatRow[] = [];
  for (const entry of Array.isArray(raw.formats) ? raw.formats : []) {
    if (!isRecord(entry) || !text(entry.id) || !statuses.has(text(entry.status))) continue;
    const conversion = isRecord(entry.conversion) ? entry.conversion : {};
    const nativeQuote = Object.hasOwn(conversion, 'native_quote') ? parseNativeQuote(conversion.native_quote) : undefined;
    formats.push({
      id: text(entry.id),
      label: text(entry.label) || text(entry.id),
      status: text(entry.status) as ModelFormatStatus,
      taskId: text(entry.task_id),
      assetId: text(entry.asset_id),
      providerId: text(conversion.provider_id),
      providerName: text(conversion.provider_name),
      providerShortLabel: text(conversion.provider_short_label),
      ...(nativeQuote === undefined ? { estimatedCredits: count(conversion.estimated_credits) } : { estimatedCredits: 0, nativeQuote }),
      creditsMetered: conversion.credits_metered === true,
      reasonText: text(entry.reason_text),
    });
  }
  const steps: ModelFormatStep[] = [];
  for (const entry of Array.isArray(raw.steps) ? raw.steps : []) {
    if (!isRecord(entry)) continue;
    const nativeQuote = Object.hasOwn(entry, 'native_quote') ? parseNativeQuote(entry.native_quote) : undefined;
    steps.push({
      offeringId: text(entry.offering_id),
      capability: text(entry.capability),
      providerId: text(entry.provider_id),
      providerName: text(entry.provider_name),
      providerShortLabel: text(entry.provider_short_label),
      formats: Array.isArray(entry.formats) ? entry.formats.filter((value): value is string => typeof value === 'string') : [],
      ...(nativeQuote === undefined ? { estimatedCredits: count(entry.estimated_credits) } : { estimatedCredits: 0, nativeQuote }),
      state: text(entry.state),
      planId: text(entry.plan_id),
      planVersion: count(entry.plan_version),
      offeringRevision: text(entry.offering_revision),
      failure: text(entry.failure),
      resultTaskId: text(entry.result_task_id),
      chargedCredits: count(entry.charged_credits),
      refetch: parseStepRefetch(entry.refetch),
      previousFailure: text(entry.previous_failure),
      retryBlocked: entry.retry_blocked === true,
    });
  }
  const priceTotals = Object.hasOwn(raw, 'price_totals') ? parsePriceTotals(raw.price_totals) : undefined;
  return {
    taskId: raw.task_id,
    applicable: raw.applicable === true,
    formats,
    steps,
    completionCredits: priceTotals === undefined ? count(raw.completion_credits) : 0,
    canComplete: raw.can_complete === true,
    archiveAvailable: raw.archive_available === true,
    ...(priceTotals === undefined ? {} : { priceTotals: priceTotals ?? [] }),
  };
}

/** 补齐正在进行：存在排队/运行/暂停的计划（暂停也不会再自动推进，但不是失败）。 */
export function stepsInProgress(coverage: ModelFormatCoverage): boolean {
  return coverage.steps.some((step) => step.state === 'PENDING' || step.state === 'RUNNING');
}

export function pausedSteps(coverage: ModelFormatCoverage): ModelFormatStep[] {
  return coverage.steps.filter((step) => step.state === 'PAUSED');
}

export function failedSteps(coverage: ModelFormatCoverage): ModelFormatStep[] {
  return coverage.steps.filter((step) => step.state === 'FAILED' && step.planId !== '' && !refetchUnderway(step));
}

/** A failed step whose free refetch is running or already delivered: nothing to act on (#330). */
export function refetchUnderway(step: ModelFormatStep): boolean {
  return Boolean(step.refetch?.taskId) && step.refetch?.state !== 'FAILED' && step.refetch?.state !== 'CANCELED';
}

/**
 * The free refetch is offered first (#330): the failed step's charged result can still be fetched from the provider
 * and no refetch has failed yet. After a failed refetch, or when it cannot be fetched, the retry (which charges again)
 * is what remains.
 */
export function refetchOffered(step: ModelFormatStep): boolean {
  return step.state === 'FAILED' && step.resultTaskId !== '' && step.refetch?.eligible === true && !step.refetch.taskId && step.refetch.failed === 0;
}

function fill(template: string, values: Record<string, string>): string {
  return Object.entries(values).reduce((route, [name, value]) => route.replace(`{${name}}`, encodeURIComponent(value)), template);
}

/** The server's safe user-facing reason, or the given fallback. */
export async function problemText(response: Response, fallback: string): Promise<string> {
  const problem: { detail?: unknown } = await response.json().catch(() => ({}));
  return typeof problem.detail === 'string' && problem.detail !== '' ? problem.detail : fallback;
}

/**
 * Why a one-click ZIP failed: the too-large answer is a declared product reason and follows the UI language; other
 * failures keep the server's safe user-facing detail, or the generic download-failed text.
 */
export async function archiveFailureText(schema: ProductSchema, response: Response): Promise<string> {
  if (response.status === 413) {
    const tooLarge = schemaText(schema, 'error.model_archive_too_large');
    if (tooLarge) return tooLarge;
  }
  return problemText(response, schemaText(schema, 'ui.workspace.model_formats.download_failed'));
}

export function modelFormatsRoute(schema: ProductSchema, taskId: string): string | null {
  const route = schema.runtime.http.routes.model_formats;
  return route ? fill(route, { taskID: taskId }) : null;
}

export function modelFormatsCompleteRoute(schema: ProductSchema, taskId: string): string | null {
  const route = schema.runtime.http.routes.model_formats_complete;
  return route ? fill(route, { taskID: taskId }) : null;
}

export function modelArchiveRoute(schema: ProductSchema, taskId: string): string | null {
  const route = schema.runtime.http.routes.model_archive;
  return route ? fill(route, { taskID: taskId }) : null;
}

export function taskAssetRoute(schema: ProductSchema, taskId: string, assetId: string): string {
  return fill(schema.runtime.http.routes.task_asset, { taskID: taskId, assetID: assetId });
}

export function planRetryRoute(schema: ProductSchema, planId: string): string {
  return fill(schema.runtime.http.routes.processing_plan_retry, { planID: planId });
}

export type DeliveryFormatPhase = 'ready' | 'loading' | 'failed';

export interface DeliveryFormatTexts {
  /** The task has no format list: not finished, or its kind has no delivery formats. */
  notReady: string;
  /** The API did not mention this format although the list applies. */
  notListed: string;
  loading: string;
  failed: string;
}

/**
 * One row per Schema delivery format, always, in the Schema's order. The state of
 * each comes from the formats API; a format the API did not report is shown as
 * unavailable with the reason why, never silently dropped. API-only formats (a
 * server newer than the Schema) follow the Schema's.
 */
export function deliveryFormatRows(
  listed: ReadonlyArray<{ id: string; label: string }> | undefined,
  coverage: ModelFormatCoverage | null,
  phase: DeliveryFormatPhase,
  texts: DeliveryFormatTexts,
): ModelFormatRow[] {
  const reported = new Map((coverage?.formats ?? []).map((row) => [row.id, row]));
  const ordered = (listed ?? []).map((format) => format.id);
  const rows: ModelFormatRow[] = (listed ?? []).map((format) => {
    const row = reported.get(format.id);
    if (row) return { ...row, label: row.label || format.label };
    const base = { id: format.id, label: format.label, taskId: '', assetId: '', providerId: '', providerName: '', providerShortLabel: '', estimatedCredits: 0, creditsMetered: false };
    if (phase === 'loading') return { ...base, status: 'unknown', reasonText: texts.loading };
    if (phase === 'failed') return { ...base, status: 'unknown', reasonText: texts.failed };
    return { ...base, status: 'unavailable', reasonText: coverage && coverage.applicable ? texts.notListed : texts.notReady };
  });
  for (const row of coverage?.formats ?? []) {
    if (!ordered.includes(row.id)) rows.push(row);
  }
  return rows;
}

/** What sits under the rows: the action, or the reason there is none. */
export type FormatsFooter = 'complete' | 'converting' | 'not_ready' | 'all_present' | 'no_route' | 'none';

export function formatsFooter(coverage: ModelFormatCoverage | null, rows: readonly ModelFormatRow[]): FormatsFooter {
  if (!coverage) return 'none';
  if (!coverage.applicable) return 'not_ready';
  if (coverage.canComplete) return 'complete';
  if (stepsInProgress(coverage) || rows.some((row) => row.status === 'converting')) return 'converting';
  if (rows.length > 0 && rows.every((row) => row.status === 'available')) return 'all_present';
  return rows.some((row) => row.status === 'unavailable') ? 'no_route' : 'none';
}

/**
 * The provider a row's conversion runs at, as the row names it (#323).
 *
 * How it is chosen: a conversion that runs on this platform (`provider_id` `local`, the Offerings of the local
 * adapters) has no outside vendor, and its `provider_name` only says what it does ("格式转换"), which next to
 * "可转换" tells the person nothing. It is named by its `provider_short_label` ("本地") instead. A conversion at an
 * outside provider keeps the provider's name (Meshy AI, Tripo3D), so the person sees where the file is sent.
 */
export function conversionProviderLabel(row: Pick<ModelFormatRow, 'providerId' | 'providerName' | 'providerShortLabel'>): string {
  if (row.providerId === 'local' && row.providerShortLabel) return row.providerShortLabel;
  return row.providerName;
}

/**
 * A conversion that costs nothing: its Offering is not metered in credits, and it has no price — no native quote
 * and no credit estimate, or a native quote of zero. An unreadable native quote (null) is not called free.
 */
export function conversionIsFree(row: Pick<ModelFormatRow, 'creditsMetered' | 'estimatedCredits' | 'nativeQuote'>): boolean {
  if (row.creditsMetered) return false;
  if (row.nativeQuote === undefined) return row.estimatedCredits === 0;
  return row.nativeQuote !== null && Number(row.nativeQuote.total_amount) === 0;
}

/**
 * Whether the completion button (补齐格式) costs nothing (#324): every format it would obtain is a free
 * conversion, the same test each row uses for its 免费. With nothing left to obtain it is not "free" either.
 */
export function completionIsFree(coverage: Pick<ModelFormatCoverage, 'formats'>): boolean {
  const remaining = coverage.formats.filter((row) => row.status === 'obtainable');
  return remaining.length > 0 && remaining.every(conversionIsFree);
}

/**
 * What the row of one obtainable format offers (#323). Conversions run by step: one step may convert several
 * formats in one run, so starting a row starts its whole step and `formats` lists every format that run converts.
 * A step whose plan failed is retried (the plan's own retry, so no second run is paid for); a step whose plan is
 * queued, running, paused or done offers nothing here (the progress, resume and result are shown elsewhere).
 */
export type FormatRowAction =
  | { kind: 'start'; step: ModelFormatStep; formats: string[] }
  | { kind: 'retry'; step: ModelFormatStep; formats: string[] }
  | { kind: 'refetch'; step: ModelFormatStep; formats: string[] };

export function formatRowAction(coverage: ModelFormatCoverage | null, row: ModelFormatRow): FormatRowAction | null {
  if (!coverage || row.status !== 'obtainable') return null;
  const step = coverage.steps.find((candidate) => candidate.formats.includes(row.id));
  if (!step) return null;
  if (step.state === '') return { kind: 'start', step, formats: step.formats };
  if (step.state === 'FAILED' && step.planId !== '' && !refetchUnderway(step)) {
    if (refetchOffered(step)) return { kind: 'refetch', step, formats: step.formats };
    return step.retryBlocked ? null : { kind: 'retry', step, formats: step.formats };
  }
  return null;
}

/** The body of the completion route that starts only the steps delivering these formats. */
export function modelFormatsCompleteBody(formats: readonly string[]): string {
  return JSON.stringify({ formats });
}
