'use client';

import { Download, FileText } from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatBytes } from '@/lib/admin-utils';
import type { TrackedAsset } from '@/lib/workspace/task-projection';
import { PrintReportAsset } from './print-report';
import { isRigCheckReportAsset, RigCheckReportAsset } from './rig-check-report';

/** Attachments are delivery facts, never image/model candidates or workspace objects. */
export function ResultAttachments({ assets, taskId, ownerId }: { assets: ReadonlyArray<TrackedAsset>; taskId: string; ownerId?: string }) {
  const { schema } = useProductSchema();
  const { attachments: ui, print_report: reportUI, rig_check_report: rigUI } = schema.task_history.assets;
  const files = assets.filter(asset => asset.kind === 'other' && asset.id && safeAssetURL(asset.url)).sort((a, b) => a.ordinal - b.ordinal);
  if (!files.length) return null;
  return <section className="flex min-w-0 flex-col gap-3 border-t border-border pt-3" aria-label={ui.title} data-testid="result-detail-attachments">
    <h3 className="text-sm font-semibold">{ui.title}</h3>
    {files.map(asset => {
      const url = safeAssetURL(asset.url);
      const label = ui.role_labels[asset.role] ?? ui.fallback_role;
      const media = asset.mediaType.split(';')[0].trim().toLowerCase();
      return <article key={asset.id} className="min-w-0 rounded-md border border-border p-3" data-testid="result-detail-attachment">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2"><FileText className="size-4 shrink-0 text-muted-foreground" aria-hidden /><span className="min-w-0 break-words text-xs font-medium">{label}</span></div>
          <a href={url} download className="inline-flex shrink-0 items-center gap-1.5 text-xs text-primary underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-ring" aria-label={`${ui.download}: ${label}${asset.variant ? ` (${asset.variant})` : ''}`} data-testid="result-detail-attachment-download"><Download className="size-3.5" aria-hidden />{ui.download}</a>
        </div>
        <dl className="mt-2 grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
          <dt className="text-muted-foreground">{ui.format}</dt><dd className="break-words">{asset.variant || reportUI.unknown_value}</dd>
          <dt className="text-muted-foreground">{ui.media_type}</dt><dd className="break-all">{asset.mediaType || reportUI.unknown_value}</dd>
          <dt className="text-muted-foreground">{ui.size}</dt><dd className="tabular-nums">{asset.size > 0 ? formatBytes(asset.size) : ui.unknown_size}</dd>
        </dl>
        {isRigCheckReportAsset(media, rigUI) ? <RigCheckReportAsset ownerId={ownerId} taskId={taskId} assetId={asset.id} url={url} presentation={rigUI} /> : null}
        {media === reportUI.media_type ? <PrintReportAsset ownerId={ownerId} taskId={taskId} assetId={asset.id} url={url} presentation={reportUI} /> : null}
      </article>;
    })}
  </section>;
}
