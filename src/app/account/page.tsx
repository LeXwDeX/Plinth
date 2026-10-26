'use client';

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import useSWRInfinite from 'swr/infinite';
import { useAuth } from '@/hooks/use-auth';
import { useProductSchema } from '@/components/product-schema-provider';
import { AssetPreviewDialog, type PreviewAsset } from '@/components/account/asset-preview-dialog';
import { Action } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { SchemaDateField } from '@/components/form/schema-date-field';
import { dateRangeBounds } from '@/components/ui/date-field-math';
import { Field } from '@/components/ui/field';
import { Skeleton } from '@/components/ui/skeleton';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { schemaText, taskStatePresentation } from '@/lib/product-schema';
import { createAccountResourcesClient, type AccountResources, type AccountTask } from '@/lib/account-resources';
import { ResultDetailHost } from '@/components/result/result-detail';
import { openResultDetail } from '@/components/result/result-card-parts';
import { detailStatus } from '@/lib/workspace/task-detail';
import type { ResultTarget } from '@/lib/workspace/result-target';
import type { ProductSchema } from '@/lib/product-schema';
import { sessionPriceTotalsText } from '@/lib/chat/approval-modes';
import { formatFileSize } from '@/lib/size-text';

function dateInput(daysAgo: number): string {
  const date = new Date();
  date.setDate(date.getDate() - daysAgo);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function formatDate(value: string, locale: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(locale);
}

/** Sizes are written one way across the product: decimal MB with the unit (#327). */
const formatSize = formatFileSize;

/** A task row's results for the shared result detail: pictures and GLB/glTF models preview, other files download. */
function taskTargets(schema: ProductSchema, task: AccountTask): ResultTarget[] {
  const status = detailStatus(schema, task.state) ?? 'ready';
  return task.assets.flatMap((asset): ResultTarget[] => {
    const url = safeAssetURL(asset.url);
    if (!url) return [];
    const model = asset.media_type === 'model/gltf-binary' || asset.media_type === 'model/gltf+json';
    return [{
      taskId: task.id, assetId: asset.id, url, status, capability: task.capability, mediaType: asset.media_type,
      kind: asset.media_type.startsWith('image/') ? 'image' : model ? 'model' : 'other',
      title: `${task.capability} · ${asset.variant || asset.role}`,
    }];
  });
}

export default function AccountPage() {
  const { schema } = useProductSchema();
  const { user } = useAuth();
  const copy = (key: string) => schemaText(schema, `ui.account.${key}`);
  const [startDate, setStartDate] = useState(() => dateInput(schema.administration.usage.defaults.days));
  const [endDate, setEndDate] = useState(() => dateInput(0));
  const [uploadPage, setUploadPage] = useState(1);
  const [preview, setPreview] = useState<PreviewAsset | null>(null);
  // Lex's card rule: a click opens the shared result detail on the first picture or model; download stays on the row.
  const openTask = (task: AccountTask) => {
    const group = taskTargets(schema, task);
    // A 3D task's GLB comes before its preview pictures; otherwise the first picture.
    const target = group.find((item) => item.kind === 'model') ?? group.find((item) => item.kind === 'image') ?? group[0];
    openResultDetail(target
      ? (group.length > 1 ? { ...target, group } : target)
      : { taskId: task.id, kind: 'other', url: '', title: `${task.capability} · ${task.id}`, capability: task.capability, status: detailStatus(schema, task.state) ?? 'pending' });
  };
  const client = useMemo(() => createAccountResourcesClient(schema), [schema]);
  const usageQuery = useMemo(() => ({ startDate, endDate }), [startDate, endDate]);
  const usageKey = user ? ['account-usage', schema.revision, user.id, client.usageURL(usageQuery)] : null;
  const { data: usage, error: usageError, isLoading: usageLoading, mutate: refreshUsage } = useSWR(
    usageKey,
    () => client.getUsage(usageQuery),
    { revalidateOnFocus: false },
  );
  const pageSize = schema.administration.user_assets.limits.frontend_page_size;
  const { data: resourcePages, error: resourcesError, isLoading: resourcesLoading, size, setSize, mutate: refreshResources } = useSWRInfinite<AccountResources>(
    (pageIndex, previous) => {
      if (!user || (pageIndex > 0 && !previous?.next_cursor)) return null;
      const cursor = pageIndex > 0 ? previous?.next_cursor : undefined;
      return [
        'account-resources', schema.revision, user.id, cursor ?? '', uploadPage,
      ] as const;
    },
    ([, , , cursor, requestedUploadPage]) => client.getResources({
      cursor: String(cursor) || undefined,
      limit: pageSize,
      page: Number(requestedUploadPage),
      pageSize,
    }),
    { revalidateOnFocus: false },
  );
  const firstPage = resourcePages?.[0];
  const tasks = resourcePages?.flatMap((page) => page.tasks) ?? [];
  const hasMoreTasks = Boolean(resourcePages?.at(-1)?.next_cursor);
  const uploads = firstPage?.uploads;
  const loading = usageLoading || resourcesLoading;

  const refresh = () => Promise.all([refreshUsage(), refreshResources()]);

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{copy('title')}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{copy('description')}</p>
        </div>
        <Action type="button" variant="outline" onClick={() => void refresh()}>
          {copy('refresh')}
        </Action>
      </header>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle>{copy('usage_title')}</CardTitle>
          <div className="flex flex-wrap gap-3">
            <Field id="account-usage-start" label={copy('from_date')}>
              {(props) => <SchemaDateField {...props} {...dateRangeBounds(startDate, endDate).start} value={startDate} onValueChange={setStartDate} clearable={false} className="w-40" />}
            </Field>
            <Field id="account-usage-end" label={copy('to_date')}>
              {(props) => <SchemaDateField {...props} {...dateRangeBounds(startDate, endDate).end} value={endDate} onValueChange={setEndDate} clearable={false} className="w-40" />}
            </Field>
          </div>
        </CardHeader>
        <CardContent>
          {usageError ? <p role="alert" className="text-sm text-destructive">{copy('load_failed')}</p> : null}
          {usageLoading ? <Skeleton className="h-16 w-full" /> : usage ? (
            <div className="grid gap-3 sm:grid-cols-3">
              <Metric label={copy('usage_count')} value={usage.total_tasks} />
              <Metric label={copy('estimated_credits')} value={usage.price_totals
                ? sessionPriceTotalsText(schema, usage.price_totals)
                : usage.estimated_credits ?? schemaText(schema, 'ui.pricing.unknown')} />
              <Metric label={copy('usage_range')} value={Object.entries(usage.by_type ?? {}).map(([key, value]) => `${key}: ${value}`).join(' · ') || '—'} />
            </div>
          ) : null}
        </CardContent>
      </Card>

      <div className="grid gap-5 xl:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>{copy('uploaded_title')} {uploads ? <span className="text-sm font-normal text-muted-foreground">({uploads.total})</span> : null}</CardTitle></CardHeader>
          <CardContent>
            {resourcesError ? <p role="alert" className="text-sm text-destructive">{copy('load_failed')}</p> : null}
            {resourcesLoading && !uploads ? <Skeleton className="h-24 w-full" /> : !uploads?.data.length ? <p className="text-sm text-muted-foreground">{copy('empty_uploads')}</p> : (
              <ul className="divide-y">
                {uploads.data.map((asset) => {
                  const href = safeAssetURL(asset.url);
                  return <li key={asset.id} className="flex items-center justify-between gap-3 py-3 text-sm">
                    <div className="min-w-0">
                      <p className="truncate font-medium">{asset.original_filename || asset.id}</p>
                      <p className="text-xs text-muted-foreground">{asset.media_type} · {formatSize(asset.size)} · {formatDate(asset.created_at, schema.runtime.frontend.display_locale)}</p>
                    </div>
                    {href ? (
                      <Action type="button" variant="link" size="sm" className="shrink-0"
                        onClick={() => setPreview({
                          name: asset.original_filename || asset.id,
                          mediaType: asset.media_type,
                          href,
                          detail: `${asset.media_type} · ${formatSize(asset.size)}`,
                        })}
                      >
                        {copy('view')}
                      </Action>
                    ) : null}
                  </li>;
                })}
              </ul>
            )}
            {uploads && uploads.total > uploads.page_size ? (
              <div className="mt-3 flex items-center justify-between gap-2">
                <span className="text-xs text-muted-foreground">{copy('page')} {uploads.page} · {copy('total')} {uploads.total}</span>
                <div className="flex gap-2">
                  <Action size="sm" variant="outline" disabled={uploadPage <= 1} onClick={() => setUploadPage((page) => page - 1)}>{copy('previous')}</Action>
                  <Action size="sm" variant="outline" disabled={uploadPage * uploads.page_size >= uploads.total} onClick={() => setUploadPage((page) => page + 1)}>{copy('next')}</Action>
                </div>
              </div>
            ) : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>{copy('tasks_title')}</CardTitle></CardHeader>
          <CardContent>
            {resourcesError ? <p role="alert" className="text-sm text-destructive">{copy('load_failed')}</p> : null}
            {resourcesLoading && tasks.length === 0 ? <Skeleton className="h-24 w-full" /> : tasks.length === 0 ? <p className="text-sm text-muted-foreground">{copy('empty_tasks')}</p> : (
              <ul className="divide-y">
                {tasks.map((task) => {
                  const state = taskStatePresentation(schema, task.state);
                  return <li key={task.id} className="py-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <button type="button" className="min-w-0 rounded-sm text-left hover:text-primary focus-visible:outline-2 focus-visible:outline-ring" onClick={() => openTask(task)} data-testid="account-task-open">
                        <p className="truncate text-sm font-medium">{task.capability} · {task.id}</p>
                        <p className="text-xs text-muted-foreground">{formatDate(task.created_at, schema.runtime.frontend.display_locale)}</p>
                      </button>
                      <span className={`text-xs ${state?.color ?? ''}`}>{state?.label ?? task.state}</span>
                    </div>
                    {task.assets.length > 0 ? (
                      <div className="mt-2 flex flex-wrap gap-2">
                        {task.assets.map((asset) => {
                          const href = safeAssetURL(asset.url);
                          return href ? <a key={asset.id} className="rounded border px-2 py-1 text-xs text-primary hover:bg-muted" href={href} target="_blank" rel="noopener noreferrer" download>{copy('download')} · {asset.variant || asset.role}</a> : null;
                        })}
                      </div>
                    ) : null}
                  </li>;
                })}
              </ul>
            )}
            {hasMoreTasks ? <Action type="button" variant="outline" className="mt-3 w-full" onClick={() => void setSize(size + 1)}>{copy('load_more')}</Action> : null}
          </CardContent>
        </Card>
      </div>
      <AssetPreviewDialog asset={preview} onClose={() => setPreview(null)} />
      <ResultDetailHost />
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return <div className="rounded-lg border bg-muted/20 p-3">
    <p className="text-xs text-muted-foreground">{label}</p>
    <p className="mt-1 text-lg font-semibold">{value}</p>
  </div>;
}
