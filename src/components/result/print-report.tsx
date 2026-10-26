'use client';

import { useEffect, useState } from 'react';
import { Action } from '@/components/ui/button';
import type { PrintReportPresentation } from '@/lib/product-schema';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';

const FORMAT = 'art-ai.print-report.v1';
const MEDIA_TYPE = 'application/vnd.art-ai.print-report+json';
const PROTOCOL_MAX_BYTES = 64 * 1024;
type Fields = Record<string, unknown>;
export type NormalizedPrintReport = { format: typeof FORMAT; kind: 'analysis' | 'repair'; analysis?: Fields; repair?: Fields };
const record = (raw: unknown): raw is Fields => raw !== null && typeof raw === 'object' && !Array.isArray(raw);
const finite = (raw: unknown): raw is number => typeof raw === 'number' && Number.isFinite(raw);
const count = (raw: unknown): raw is number => typeof raw === 'number' && Number.isSafeInteger(raw) && raw >= 0;
const ratio = (raw: unknown) => finite(raw) && raw >= 0 && raw <= 1;
const member = (raw: unknown, values: string[]): raw is string => typeof raw === 'string' && values.includes(raw);
const optional = (item: Fields, keys: string[], valid: (value: unknown) => boolean) => keys.every(key => item[key] === undefined || valid(item[key]));
const only = (item: Fields, keys: string[]) => Object.keys(item).every(key => keys.includes(key));
function intended(raw: unknown): boolean {
  if (raw === undefined) return true;
  return record(raw) && only(raw, ['printer_type', 'height_mm', 'longest_side_mm', 'wall_thickness_mm', 'dimensions_mm']) && member(raw.printer_type, ['fdm', 'sla', 'full_color']) && optional(raw, ['height_mm', 'longest_side_mm', 'wall_thickness_mm'], value => finite(value) && value > 0) && (raw.dimensions_mm === undefined || (Array.isArray(raw.dimensions_mm) && raw.dimensions_mm.length === 3 && raw.dimensions_mm.every(value => finite(value) && value >= 0)));
}
/** This recognizes our public report DTO only. No provider fields or raw-wire aliases. */
export function parseNormalizedPrintReport(raw: unknown): NormalizedPrintReport | null {
  if (!record(raw) || raw.format !== FORMAT || !only(raw, ['format', 'kind', 'analysis', 'repair'])) return null;
  if (raw.kind === 'analysis') {
    const a = raw.analysis;
    if (!record(a) || raw.repair !== undefined || !only(a, ['status', 'issue_count', 'error_count', 'warning_count', 'metrics', 'intended_print', 'thin_walls', 'evaluated_at_ms']) || !member(a.status, ['healthy', 'warning', 'error', 'unknown']) || !optional(a, ['issue_count', 'error_count', 'warning_count', 'evaluated_at_ms'], count) || !intended(a.intended_print)) return null;
    const counts = ['issue_count', 'error_count', 'warning_count'].filter(key => a[key] !== undefined).length;
    if ((counts !== 0 && counts !== 3) || (a.status !== 'unknown' && (counts !== 3 || a.metrics === undefined))) return null;
    if (counts === 3 && (Number(a.issue_count) !== Number(a.error_count) + Number(a.warning_count) || (a.status === 'healthy' && a.issue_count !== 0) || (a.status === 'warning' && (a.error_count !== 0 || Number(a.warning_count) <= 0)) || (a.status === 'error' && Number(a.error_count) <= 0))) return null;
    if (a.metrics !== undefined) {
      const m = a.metrics;
      if (!record(m) || !only(m, ['is_watertight', 'volume_m3', 'non_manifold_edges', 'degenerate_faces', 'holes']) || typeof m.is_watertight !== 'boolean' || !finite(m.volume_m3) || !['non_manifold_edges', 'degenerate_faces', 'holes'].every(key => count(m[key]))) return null;
    }
    if (a.thin_walls !== undefined) {
      const w = a.thin_walls;
      if (!record(w) || !only(w, ['status', 'thin_area_ratio', 'has_thin_walls']) || a.intended_print === undefined || !member(w.status, ['healthy', 'error', 'unknown']) || !optional(w, ['thin_area_ratio'], ratio) || !optional(w, ['has_thin_walls'], value => typeof value === 'boolean') || (w.status !== 'unknown' && (w.thin_area_ratio === undefined || w.has_thin_walls === undefined))) return null;
    }
    return raw as NormalizedPrintReport;
  }
  if (raw.kind === 'repair') {
    const r = raw.repair;
    if (!record(r) || raw.analysis !== undefined || !only(r, ['details_status', 'intended_print', 'route', 'applied', 'faces_before', 'faces_after', 'thin_area_ratio_before', 'thin_area_ratio_after']) || !intended(r.intended_print) || !optional(r, ['faces_before', 'faces_after'], count) || !optional(r, ['thin_area_ratio_before', 'thin_area_ratio_after'], ratio) || !optional(r, ['applied'], value => typeof value === 'boolean')) return null;
    if (r.details_status === 'not_reported') {
      if (['route', 'applied', 'faces_before', 'faces_after', 'thin_area_ratio_before', 'thin_area_ratio_after'].some(key => r[key] !== undefined)) return null;
    } else if (r.details_status === 'reported') {
      if (r.route === 'thin_wall') { if (['applied', 'faces_before', 'faces_after'].some(key => r[key] === undefined)) return null; }
      else if (r.route === 'topology' || r.route === 'topology_unmeasured') { if (['applied', 'faces_before', 'faces_after'].some(key => r[key] !== undefined)) return null; }
      else return null;
    } else return null;
    return raw as NormalizedPrintReport;
  }
  return null;
}

export function printReportRows(report: NormalizedPrintReport, ui: PrintReportPresentation): Array<{ key: string; label: string; value: string }> {
  const rows: Array<{ key: string; label: string; value: string }> = [];
  const add = (key: string, value: unknown, labels?: Record<string, string>) => {
    const shown = value === undefined || value === null ? ui.unknown_value : labels ? labels[String(value)] ?? ui.unknown_value : Array.isArray(value) ? value.join(' × ') : String(value);
    rows.push({ key, label: ui.field_labels[key], value: shown });
  };
  const body = report.kind === 'analysis' ? report.analysis! : report.repair!;
  if (report.kind === 'analysis') {
    add('status', body.status, ui.status_labels);
    for (const key of ['issue_count', 'error_count', 'warning_count']) add(key, body[key]);
    const metrics = record(body.metrics) ? body.metrics : {};
    for (const key of ['is_watertight', 'volume_m3', 'non_manifold_edges', 'degenerate_faces', 'holes']) add(key, metrics[key], key === 'is_watertight' ? ui.value_labels : undefined);
    if (record(body.thin_walls)) {
      add('thin_wall_status', body.thin_walls.status, ui.status_labels);
      add('thin_area_ratio', body.thin_walls.thin_area_ratio);
      add('has_thin_walls', body.thin_walls.has_thin_walls, ui.value_labels);
    }
    if (count(body.evaluated_at_ms)) {
      const date = new Date(body.evaluated_at_ms);
      add('evaluated_at_ms', Number.isFinite(date.getTime()) ? date.toISOString() : body.evaluated_at_ms);
    }
  } else {
    add('details_status', body.details_status, ui.details_status_labels);
    if (body.details_status === 'reported') {
      add('route', body.route, ui.value_labels);
      if (body.route === 'thin_wall') {
        add('applied', body.applied, ui.value_labels); add('faces_before', body.faces_before); add('faces_after', body.faces_after);
      }
      add('thin_area_ratio_before', body.thin_area_ratio_before); add('thin_area_ratio_after', body.thin_area_ratio_after);
    }
  }
  if (record(body.intended_print)) {
    const print = body.intended_print;
    add('printer_type', print.printer_type, ui.value_labels);
    for (const key of ['height_mm', 'longest_side_mm', 'wall_thickness_mm', 'dimensions_mm']) add(key, print[key]);
  }
  return rows;
}

export function PrintReportContent({ report, presentation: ui }: { report: NormalizedPrintReport; presentation: PrintReportPresentation }) {
  return <div className="flex min-w-0 flex-col gap-2" data-testid="print-report-content" data-report-kind={report.kind}>
    <h4 className="text-xs font-semibold">{ui.kind_labels[report.kind]}</h4>
    {report.kind === 'repair' && report.repair?.details_status === 'not_reported' ? <p className="text-xs text-muted-foreground" role="note">{ui.not_reported}</p> : null}
    <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
      {printReportRows(report, ui).map(row => <div key={row.key} className="contents" data-report-field={row.key}><dt className="text-muted-foreground">{row.label}</dt><dd className="min-w-0 break-words tabular-nums">{row.value}</dd></div>)}
    </dl>
    {report.kind === 'analysis' ? <p className="text-xs leading-relaxed text-muted-foreground" role="note">{ui.diagnostic_notice}</p> : null}
  </div>;
}

export async function readPrintReport(url: string, ui: PrintReportPresentation, signal?: AbortSignal): Promise<NormalizedPrintReport> {
  const safe = safeAssetURL(url);
  if (!safe.startsWith('/') || ui.format !== FORMAT || ui.media_type !== MEDIA_TYPE || !Number.isSafeInteger(ui.max_bytes) || ui.max_bytes <= 0 || ui.max_bytes > PROTOCOL_MAX_BYTES) throw new Error('unsupported_report');
  const response = await fetch(safe, { method: 'GET', credentials: 'same-origin', redirect: 'error', signal });
  if (!response.ok) throw new Error('report_unavailable');
  if (response.headers.get('content-type')?.split(';')[0].trim().toLowerCase() !== MEDIA_TYPE || !response.body) throw new Error('unsupported_report');
  const reader = response.body.getReader(); const chunks: Uint8Array[] = []; let length = 0;
  try {
    for (;;) {
      const chunk = await reader.read(); if (chunk.done) break;
      length += chunk.value.byteLength;
      if (length > ui.max_bytes) { await reader.cancel(); throw new Error('unsupported_report'); }
      chunks.push(chunk.value);
    }
  } finally { reader.releaseLock(); }
  const bytes = new Uint8Array(length); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  let raw: unknown;
  try { raw = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); } catch { throw new Error('unsupported_report'); }
  const report = parseNormalizedPrintReport(raw);
  if (!report) throw new Error('unsupported_report');
  return report;
}

/** Scope keys and abort cleanup prevent an old owner/task/asset response from flashing into a new detail. */
export function PrintReportAsset({ ownerId, taskId, assetId, url, presentation: ui }: { ownerId?: string; taskId: string; assetId: string; url: string; presentation: PrintReportPresentation }) {
  const scope = JSON.stringify([ownerId, taskId, assetId, url, ui.format, ui.media_type, ui.max_bytes]);
  const [result, setResult] = useState<{ scope: string; report?: NormalizedPrintReport; error?: 'unavailable' | 'unsupported' }>();
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!ownerId) return;
    const controller = new AbortController(); let active = true;
    setResult({ scope });
    void readPrintReport(url, ui, controller.signal).then(report => { if (active) setResult({ scope, report }); }, error => { if (active) setResult({ scope, error: error instanceof Error && error.message === 'unsupported_report' ? 'unsupported' : 'unavailable' }); });
    return () => { active = false; controller.abort(); };
  }, [scope, retry, ownerId, url, ui.format, ui.media_type, ui.max_bytes]);
  const current = result?.scope === scope ? result : undefined;
  return <section className="mt-3 flex min-w-0 flex-col gap-2 border-t border-border pt-3" aria-label={ui.title} data-testid="print-report">
    {current?.report ? <PrintReportContent report={current.report} presentation={ui} /> : current?.error ? <div className="flex items-start justify-between gap-3 text-xs"><p role="status" className="text-muted-foreground">{current.error === 'unsupported' ? ui.unsupported : ui.load_failed}</p><Action type="button" variant="outline" size="xs" onClick={() => setRetry(value => value + 1)}>{ui.retry}</Action></div> : <p role="status" className="text-xs text-muted-foreground">{ui.loading}</p>}
  </section>;
}
