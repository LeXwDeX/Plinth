'use client';

import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Image as ImageIcon } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import type { MessagePicture } from '@/lib/chat/message-pictures';
import {
  selfCheckCaveat,
  selfCheckChecksCount,
  selfCheckCorrectionsText,
  selfCheckLine,
  selfCheckMetricText,
  selfCheckRendersText,
  selfCheckRoundsText,
  selfCheckStatus,
  selfCheckStatusLabel,
  type SelfCheckCheck,
  type SelfCheckRecord,
} from '@/lib/chat/self-check';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { useStudioStore } from '@/store/studio-store';
import { cn } from '@/lib/utils';

interface SelfCheckRecordCardProps {
  /** The persisted records of the Tasks this reply names, in record order. */
  records: readonly SelfCheckRecord[];
  /** The reply's render pictures; a check's @handle claims its own thumbnail. */
  renders: readonly MessagePicture[];
  /** Folded by default so the chat stays readable; tests open it to read the records. */
  defaultOpen?: boolean;
}

/**
 * #246: the persisted self-check records of one reply, folded into one card.
 * Each record shows its delivery state (from the public Schema's roles), the
 * server counters, the written conclusion and the evidence the server stored.
 * A result without a record is unchecked; this card only renders records.
 */
export function SelfCheckRecordCard({ records, renders, defaultOpen = false }: SelfCheckRecordCardProps) {
  const { schema } = useProductSchema();
  const [open, setOpen] = useState(defaultOpen);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const text = (key: string) => schemaText(schema, key);
  const toggle = (taskId: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(taskId)) next.delete(taskId);
      else next.add(taskId);
      return next;
    });
  };
  if (records.length === 0) return null;
  const checks = records.reduce((total, record) => total + record.checks.length, 0);
  return (
    <section
      className="w-full overflow-hidden rounded-lg border border-border/70 bg-panel-raised/40"
      data-testid="chat-self-check-record"
    >
      <button
        type="button"
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-xs text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        data-testid="chat-self-check-record-toggle"
      >
        {open ? <ChevronDown className="size-3.5 shrink-0" aria-hidden /> : <ChevronRight className="size-3.5 shrink-0" aria-hidden />}
        <span className="font-medium text-foreground">{text('ui.chat.self_check.title')}</span>
        <span className="truncate">{selfCheckChecksCount(schema, checks)}</span>
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-t border-border/60 px-2.5 py-2">
          {records.map((record) => (
            <SelfCheckRecordBlock
              key={record.taskId}
              schema={schema}
              record={record}
              renders={renders}
              expanded={expanded.has(record.taskId)}
              onToggle={() => toggle(record.taskId)}
            />
          ))}
          {selfCheckCaveat(schema, records) && (
            <p className="text-[11px] leading-relaxed text-muted-foreground/80" data-testid="chat-self-check-caveat">
              {text('ui.chat.self_check.caveat')}
            </p>
          )}
        </div>
      )}
    </section>
  );
}

function SelfCheckRecordBlock({
  schema, record, renders, expanded, onToggle,
}: {
  schema: ProductSchema;
  record: SelfCheckRecord;
  renders: readonly MessagePicture[];
  expanded: boolean;
  onToggle: () => void;
}) {
  const text = (key: string) => schemaText(schema, key);
  const status = selfCheckStatus(schema, record);
  const label = selfCheckStatusLabel(schema, record);
  const role = status.role;
  const { pictures, metrics } = useMemo(() => checkPicturesAndMetrics(schema, record, renders), [schema, record, renders]);
  return (
    <div className="flex flex-col gap-1.5" data-testid="chat-self-check-record-item" data-task-id={record.taskId} data-status={status.id}>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <span
          className={cn(
            'rounded border px-1.5 py-0.5 text-[11px] font-medium',
            role === 'passed' && 'border-success/50 bg-success/10 text-success',
            role === 'issues' && 'border-warning/50 bg-warning/10 text-warning',
            role !== 'passed' && role !== 'issues' && 'border-border bg-muted text-muted-foreground',
          )}
          data-testid="chat-self-check-status"
          data-role={role}
        >
          {label}
        </span>
        <span className="text-[11px] text-muted-foreground" data-testid="chat-self-check-rounds">{selfCheckRoundsText(schema, record)}</span>
        <span className="text-[11px] text-muted-foreground">{selfCheckCorrectionsText(schema, record)}</span>
        <span className="text-[11px] text-muted-foreground">{selfCheckRendersText(schema, record)}</span>
        {record.checks.length > 0 && (
          <button
            type="button"
            className="ml-auto text-[11px] text-primary hover:underline focus-visible:outline-2 focus-visible:outline-ring"
            aria-expanded={expanded}
            onClick={onToggle}
            data-testid="chat-self-check-expand"
          >
            {text(expanded ? 'ui.chat.self_check.collapse' : 'ui.chat.self_check.expand')}
          </button>
        )}
      </div>
      {record.conclusion && (
        <div className="flex flex-col gap-1 text-[11px] leading-relaxed" data-testid="chat-self-check-conclusion">
          {record.conclusion.summary && <p className="text-foreground/90">{record.conclusion.summary}</p>}
          {record.conclusion.purposeLabel && (
            <p className="text-muted-foreground" data-testid="chat-self-check-purpose">
              {text('ui.chat.self_check.purpose').replaceAll('{purpose}', record.conclusion.purposeLabel)}
            </p>
          )}
          {record.conclusion.waiver && (
            <p className="text-warning" data-testid="chat-self-check-waiver">
              {text('ui.chat.self_check.waiver').replaceAll('{waiver}', record.conclusion.waiver)}
            </p>
          )}
          <ConclusionList schema={schema} labelKey="ui.chat.self_check.findings"
            emptyKey="ui.chat.self_check.no_findings" values={record.conclusion.findings} />
          <ConclusionList schema={schema} labelKey="ui.chat.self_check.fixes"
            emptyKey="ui.chat.self_check.no_findings" values={record.conclusion.fixes} />
          <ConclusionList schema={schema} labelKey="ui.chat.self_check.remaining"
            emptyKey="ui.chat.self_check.no_remaining" values={record.conclusion.remaining} />
        </div>
      )}
      {expanded && (
        <div className="flex flex-col gap-1" data-testid="chat-self-check-details">
          {record.checks.length === 0
            ? <p className="text-[11px] text-muted-foreground">{text('ui.chat.self_check.record_empty')}</p>
            : record.checks.map((check, index) => (
              <SelfCheckLine
                key={`${check.tool}-${index}`}
                schema={schema}
                check={check}
                taskId={record.taskId}
                pictures={pictures[index] ?? []}
                metrics={metrics[index] ?? []}
              />
            ))}
        </div>
      )}
    </div>
  );
}

function ConclusionList({
  schema, labelKey, emptyKey, values,
}: {
  schema: ProductSchema;
  labelKey: string;
  emptyKey: string;
  values: readonly string[];
}) {
  return (
    <p className="text-muted-foreground">
      <span className="text-foreground/70">{schemaText(schema, labelKey)}</span>
      {' '}
      {values.length > 0 ? values.join(schemaText(schema, 'ui.chat.review.item_separator')) : schemaText(schema, emptyKey)}
    </p>
  );
}

function SelfCheckLine({
  schema, check, taskId, pictures, metrics,
}: {
  schema: ProductSchema;
  check: SelfCheckCheck;
  taskId: string;
  pictures: MessagePicture[];
  metrics: string[];
}) {
  return (
    <div className="flex min-w-0 items-start gap-1.5" data-testid="chat-self-check-line" data-kind={check.kind} data-ok={check.ok ? 'true' : 'false'}>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className={cn('min-w-0 truncate text-xs', check.ok ? 'text-foreground' : 'text-warning')}>
          {selfCheckLine(schema, check, taskId)}
        </p>
        {check.error && <p className="text-[11px] text-warning">{check.error}</p>}
        {metrics.length > 0 && (
          <p className="text-[11px] text-muted-foreground" data-testid="chat-self-check-metrics">
            {metrics.join(schemaText(schema, 'ui.chat.review.item_separator'))}
          </p>
        )}
      </div>
      {pictures.length > 0 && <SelfCheckThumbnails schema={schema} pictures={pictures} label={selfCheckLine(schema, check, taskId)} />}
    </div>
  );
}

function SelfCheckThumbnails({ schema, pictures, label }: {
  schema: ProductSchema;
  pictures: readonly MessagePicture[];
  label: string;
}) {
  const { previewAssetUrl } = useAssetURLRewriter();
  const openStageImage = useStudioStore((state) => state.openStageImage);
  const size = schema.chat.frontend.review_thumbnail_pixels;
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-1" data-testid="chat-self-check-thumbnails">
      {pictures.map((picture) => {
        const src = previewAssetUrl(picture.image.url);
        const name = picture.handle ? `${label} ${picture.handle}` : label;
        return (
          <HintTooltip key={`${picture.image.url}-${picture.index}`} name={name} className="shrink-0">
            <button
              type="button"
              className="overflow-hidden rounded border border-border/70 bg-muted focus-visible:outline-2 focus-visible:outline-ring"
              style={{ width: size, height: size }}
              aria-label={name}
              onClick={(event) => {
                event.stopPropagation();
                if (picture.image.url) openStageImage({ url: picture.image.url, label: name });
              }}
              data-testid="chat-self-check-thumbnail"
            >
              {src
                ? <img src={src} alt={name} className="size-full object-cover" loading="lazy" />
                : <ImageIcon className="m-auto size-4 text-muted-foreground" aria-hidden />}
            </button>
          </HintTooltip>
        );
      })}
    </div>
  );
}

/**
 * Binds each check to the reply's render pictures by @handle; a check without
 * handles (an older record or compare_models) falls back to the next unused
 * pictures in view order, as the review card does.
 */
function checkPicturesAndMetrics(
  schema: ProductSchema,
  record: SelfCheckRecord,
  renders: readonly MessagePicture[],
): { pictures: MessagePicture[][]; metrics: string[][] } {
  const used = new Set<number>();
  const pictures: MessagePicture[][] = [];
  const metrics: string[][] = [];
  for (const check of record.checks) {
    const claimed: MessagePicture[] = [];
    if (check.handles.length > 0) {
      for (const handle of check.handles) {
        const at = renders.findIndex((entry, index) => !used.has(index) && entry.handle === handle);
        if (at < 0) continue;
        used.add(at);
        claimed.push(renders[at]);
      }
    } else {
      for (let at = 0; at < renders.length && claimed.length < check.views.length; at += 1) {
        if (used.has(at)) continue;
        used.add(at);
        claimed.push(renders[at]);
      }
    }
    pictures.push(claimed);
    metrics.push(selfCheckMetricText(schema, check));
  }
  return { pictures, metrics };
}