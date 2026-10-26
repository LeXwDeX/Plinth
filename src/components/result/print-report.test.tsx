import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { parseNormalizedPrintReport, printReportRows, readPrintReport, PrintReportContent } from './print-report';

const ui = defaultCatalog.application.task_history.assets.print_report;
const unknown = { format: 'art-ai.print-report.v1', kind: 'analysis', analysis: { status: 'unknown' } };
const known = { format: 'art-ai.print-report.v1', kind: 'analysis', analysis: { status: 'error', issue_count: 2, error_count: 2, warning_count: 0, metrics: { is_watertight: false, volume_m3: -1, non_manifold_edges: 2, degenerate_faces: 0, holes: 0 } } };
afterEach(() => vi.unstubAllGlobals());

describe('normalized print report presentation', () => {
  it('accepts exact v1 normalized analysis/repair, rejects vendor wire and ambiguous envelopes', () => {
    expect(parseNormalizedPrintReport(known)).toEqual(known);
    expect(parseNormalizedPrintReport({ format: 'art-ai.print-report.v1', kind: 'repair', repair: { details_status: 'not_reported' } })).not.toBeNull();
    for (const raw of [{ ...known, format: 'v2' }, { ...known, repair: { details_status: 'not_reported' } }, { result: known }, { ...known, analysis: { status: 'failed' } }, { ...known, analysis: { ...known.analysis, status: ['error'] } }, { ...known, analysis: { status: 'healthy', issue_count: -1 } }]) expect(parseNormalizedPrintReport(raw)).toBeNull();
  });
  it('keeps unknown values unknown while preserving zero, false and nonpositive measured volume', () => {
    const missing = printReportRows(parseNormalizedPrintReport(unknown)!, ui);
    expect(missing.find(row => row.key === 'issue_count')?.value).toBe(ui.unknown_value);
    expect(missing.find(row => row.key === 'is_watertight')?.value).toBe(ui.unknown_value);
    const rows = printReportRows(parseNormalizedPrintReport(known)!, ui);
    expect(rows.find(row => row.key === 'warning_count')?.value).toBe('0');
    expect(rows.find(row => row.key === 'is_watertight')?.value).toBe(ui.value_labels.false);
    expect(rows.find(row => row.key === 'volume_m3')?.value).toBe('-1');
    expect(rows.find(row => row.key === 'status')?.value).toBe(ui.status_labels.error);
  });
  it('not_reported explicitly says details were not supplied, never invents successful repairs', () => {
    const report = parseNormalizedPrintReport({ format: 'art-ai.print-report.v1', kind: 'repair', repair: { details_status: 'not_reported' } })!;
    const html = renderToStaticMarkup(<PrintReportContent report={report} presentation={ui} />);
    expect(html).toContain(ui.not_reported);
    expect(html).not.toContain('data-report-field="applied"');
    expect(html).not.toContain('data-report-field="faces_before"');
  });
  it('renders diagnostic errors as report data with the Schema caveat, never a failed Task', () => {
    const report = parseNormalizedPrintReport(known)!;
    const html = renderToStaticMarkup(<PrintReportContent report={report} presentation={ui} />);
    expect(html).toContain(ui.status_labels.error); expect(html).toContain(ui.diagnostic_notice);
    expect(html).not.toContain('role="alert"'); expect(html).not.toContain('Task failed');
    const renamed = { ...ui, field_labels: { ...ui.field_labels, error_count: 'Schema error label' }, status_labels: { ...ui.status_labels, error: 'Schema diagnostic label' } };
    const changed = renderToStaticMarkup(<PrintReportContent report={report} presentation={renamed} />);
    expect(changed).toContain('Schema error label'); expect(changed).toContain('Schema diagnostic label');
  });
});

describe('authenticated bounded report loading', () => {
  it('reads only same-origin owner URLs and exact vendor MIME/version, without following redirects', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(known), { headers: { 'content-type': ui.media_type } })); vi.stubGlobal('fetch', fetch);
    expect(await readPrintReport('/api/v1/tasks/t/assets/a', ui)).toEqual(known);
    expect(fetch).toHaveBeenCalledWith('/api/v1/tasks/t/assets/a', expect.objectContaining({ credentials: 'same-origin', redirect: 'error' }));
    await expect(readPrintReport('https://foreign.invalid/report', ui)).rejects.toThrow(); expect(fetch).toHaveBeenCalledTimes(1);
  });
  it('rejects auth failures, generic JSON, oversized/chunked responses and incompatible versions', async () => {
    for (const response of [new Response('{}', { status: 403 }), new Response(JSON.stringify(known), { headers: { 'content-type': 'application/json' } }), new Response(' '.repeat(ui.max_bytes + 1), { headers: { 'content-type': ui.media_type } }), new Response(JSON.stringify({ ...known, format: 'v2' }), { headers: { 'content-type': ui.media_type } })]) {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response)); await expect(readPrintReport('/api/v1/tasks/t/assets/a', ui)).rejects.toThrow();
    }
  });
});
