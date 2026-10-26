'use client';

import { useEffect, useState } from 'react';
import { Action } from '@/components/ui/button';
import type { RigCheckReportPresentation } from '@/lib/product-schema';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';

const FORMAT = 'art-ai.rig-check-report.v1';
const MEDIA_TYPE = 'application/vnd.art-ai.rig-check-report+json';
const MAX_BYTES = 32 * 1024;
const SPECIES = ['biped', 'quadruped', 'hexapod', 'octopod', 'avian', 'serpentine', 'aquatic'];
export type NormalizedRigCheckReport = { format: typeof FORMAT; riggable: boolean; rig_type: string };

/** Only the public domain DTO is recognized here, never Tripo response fields. */
export function parseNormalizedRigCheckReport(raw: unknown): NormalizedRigCheckReport | null {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const value = raw as Record<string, unknown>;
  if (value.format !== FORMAT || typeof value.riggable !== 'boolean' || !Object.keys(value).every(key => ['format', 'riggable', 'rig_type'].includes(key))) return null;
  if (typeof value.rig_type !== 'string' || !SPECIES.includes(value.rig_type)) return null;
  return value as NormalizedRigCheckReport;
}

export function isRigCheckReportAsset(mediaType: string, ui: RigCheckReportPresentation): boolean {
  return mediaType === MEDIA_TYPE && ui.media_type === MEDIA_TYPE && ui.format === FORMAT;
}

export function RigCheckReportContent({ report, presentation: ui }: { report: NormalizedRigCheckReport; presentation: RigCheckReportPresentation }) {
  return <div className="flex min-w-0 flex-col gap-2" data-testid="rig-check-report-content">
    <h4 className="text-xs font-semibold">{ui.title}</h4>
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
      <dt className="text-muted-foreground">{ui.field_labels.riggable}</dt><dd>{ui.boolean_labels[String(report.riggable)]}</dd>
      <dt className="text-muted-foreground">{ui.field_labels.rig_type}</dt><dd>{ui.species_labels[report.rig_type]}</dd>
    </dl>
    <p className="text-xs leading-relaxed text-muted-foreground" role="note">{ui.limitation}</p>
  </div>;
}

export async function readRigCheckReport(url: string, ui: RigCheckReportPresentation, signal?: AbortSignal): Promise<NormalizedRigCheckReport> {
  const safe = safeAssetURL(url);
  if (!safe.startsWith('/') || !isRigCheckReportAsset(MEDIA_TYPE, ui) || !Number.isSafeInteger(ui.max_bytes) || ui.max_bytes <= 0 || ui.max_bytes > MAX_BYTES) throw new Error('unsupported_report');
  const response = await fetch(safe, { method: 'GET', credentials: 'same-origin', redirect: 'error', signal });
  if (!response.ok) throw new Error('report_unavailable');
  if (response.headers.get('content-type')?.split(';')[0].trim().toLowerCase() !== MEDIA_TYPE || !response.body) throw new Error('unsupported_report');
  const reader = response.body.getReader(), chunks: Uint8Array[] = []; let length = 0;
  try {
    for (;;) { const chunk = await reader.read(); if (chunk.done) break; length += chunk.value.byteLength; if (length > ui.max_bytes) { await reader.cancel(); throw new Error('unsupported_report'); } chunks.push(chunk.value); }
  } finally { reader.releaseLock(); }
  const bytes = new Uint8Array(length); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
  try {
    const source = new TextDecoder('utf-8', { fatal: true }).decode(bytes), report = parseNormalizedRigCheckReport(JSON.parse(source));
    if (!report) throw new Error('unsupported_report');
    // This exact DTO contains only closed scalar values, so all colon-prefixed
    // JSON string tokens are keys. Decode escapes before checking duplicates.
    const keys = [...source.matchAll(/("(?:\\.|[^"\\])*")\s*:/g)].map(match => JSON.parse(match[1]) as string);
    if (keys.length !== Object.keys(report).length || new Set(keys).size !== keys.length) throw new Error('unsupported_report');
    return report;
  } catch { throw new Error('unsupported_report'); }
}

/** An old owner/task/asset response cannot appear after the detail scope changes. */
export function RigCheckReportAsset({ ownerId, taskId, assetId, url, presentation: ui }: { ownerId?: string; taskId: string; assetId: string; url: string; presentation: RigCheckReportPresentation }) {
  const scope = JSON.stringify([ownerId, taskId, assetId, url, ui.format, ui.media_type, ui.max_bytes]);
  const [result, setResult] = useState<{ scope: string; report?: NormalizedRigCheckReport; error?: 'unavailable' | 'unsupported' }>();
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!ownerId) return;
    const controller = new AbortController(); let active = true;
    setResult({ scope });
    void readRigCheckReport(url, ui, controller.signal).then(report => { if (active) setResult({ scope, report }); }, error => { if (active) setResult({ scope, error: error instanceof Error && error.message === 'unsupported_report' ? 'unsupported' : 'unavailable' }); });
    return () => { active = false; controller.abort(); };
  }, [scope, retry, ownerId, url, ui.format, ui.media_type, ui.max_bytes]);
  const current = result?.scope === scope ? result : undefined;
  return <section className="mt-3 flex min-w-0 flex-col gap-2 border-t border-border pt-3" aria-label={ui.title} data-testid="rig-check-report">
    {current?.report ? <RigCheckReportContent report={current.report} presentation={ui} /> : current?.error ? <div className="flex items-start justify-between gap-3 text-xs"><p role="status" className="text-muted-foreground">{current.error === 'unsupported' ? ui.unsupported : ui.load_failed}</p><Action type="button" variant="outline" size="xs" onClick={() => setRetry(value => value + 1)}>{ui.retry}</Action></div> : <p role="status" className="text-xs text-muted-foreground">{ui.loading}</p>}
  </section>;
}
