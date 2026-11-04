'use client';

import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Image as ImageIcon } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import type { MessagePicture } from '@/lib/chat/message-pictures';
import {
  reviewCheckName,
  reviewLine,
  reviewRounds,
  reviewViewName,
  type ReviewCallModel,
  type ReviewCheckItem,
  type ReviewRound,
} from '@/lib/chat/model-review';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { useStudioStore } from '@/store/studio-store';
import { cn } from '@/lib/utils';

interface ReviewRecordCardProps {
  /** The reply's review calls in execution order. */
  calls: ReviewCallModel[];
  /** Rendered pictures no call could claim; shown with the record rather than as 2D results. */
  leftover: MessagePicture[];
}

/**
 * #103: the read-only review tools of one reply, folded into one card. Each
 * call keeps exactly one line, rendered pictures sit beside the call that made
 * them at thumbnail size, and the card can be folded away. Details (check
 * results, view names) open per line; a picture opens large on the stage.
 */
export function ReviewRecordCard({ calls, leftover }: ReviewRecordCardProps) {
  const { schema } = useProductSchema();
  const [open, setOpen] = useState(true);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set());
  const rounds = useMemo(() => reviewRounds(calls), [calls]);
  const indexOf = (call: ReviewCallModel) => calls.indexOf(call);
  const text = (key: string) => schemaText(schema, key);
  const count = calls.length + (leftover.length > 0 ? 1 : 0);
  const toggleLine = (index: number) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  };
  return (
    <section
      className="w-full overflow-hidden rounded-lg border border-border/70 bg-panel-raised/40"
      data-testid="chat-review-record"
    >
      <button
        type="button"
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-xs text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        data-testid="chat-review-record-toggle"
      >
        {open ? <ChevronDown className="size-3.5 shrink-0" aria-hidden /> : <ChevronRight className="size-3.5 shrink-0" aria-hidden />}
        <span className="font-medium text-foreground">{text('ui.chat.review.record_title')}</span>
        <span className="truncate">{schemaCountMessage(schema, 'ui.chat.review.summary_count', count, { count })}</span>
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-t border-border/60 px-2.5 py-2">
          {rounds.map((round) => (
            <ReviewRoundBlock
              key={round.index}
              schema={schema}
              round={round}
              indexOf={indexOf}
              expanded={expanded}
              onToggle={toggleLine}
            />
          ))}
          {leftover.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5" data-testid="chat-review-leftover">
              <Thumbnails
                schema={schema}
                pictures={leftover}
                labelOf={(index) => `${text('ui.chat.review.record_title')} ${index + 1}`}
              />
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function ReviewRoundBlock({
  schema, round, indexOf, expanded, onToggle,
}: {
  schema: ProductSchema;
  round: ReviewRound;
  indexOf: (call: ReviewCallModel) => number;
  expanded: ReadonlySet<number>;
  onToggle: (index: number) => void;
}) {
  const text = (key: string) => schemaText(schema, key);
  return (
    <div className="flex flex-col gap-1" data-testid="chat-review-round">
      <p className="text-[11px] font-medium text-muted-foreground">
        {formatSchemaText(text('ui.chat.review.record_step'), { index: round.index })}
      </p>
      {round.inspect && (
        <ReviewLine
          schema={schema}
          call={round.inspect}
          index={indexOf(round.inspect)}
          expanded={expanded.has(indexOf(round.inspect))}
          onToggle={onToggle}
        />
      )}
      <p className="text-[11px] leading-relaxed text-muted-foreground" data-testid="chat-review-found">
        <span className="text-foreground/70">{text('ui.chat.review.found')}</span>
        {' '}
        {round.findings.length > 0
          ? round.findings.map((check) => reviewCheckName(schema, check.check)).join(text('ui.chat.review.item_separator'))
          : text('ui.chat.review.no_findings')}
      </p>
      <div className="flex flex-col gap-1" data-testid="chat-review-handled">
        <p className="text-[11px] text-foreground/70">{text('ui.chat.review.handled')}</p>
        {round.follow.length > 0 ? round.follow.map((call) => (
          <ReviewLine
            key={indexOf(call)}
            schema={schema}
            call={call}
            index={indexOf(call)}
            expanded={expanded.has(indexOf(call))}
            onToggle={onToggle}
          />
        )) : <p className="text-[11px] text-muted-foreground">{text('ui.chat.review.no_actions')}</p>}
      </div>
      <ReviewResultLine schema={schema} round={round} />
    </div>
  );
}

function ReviewResultLine({ schema, round }: { schema: ProductSchema; round: ReviewRound }) {
  const text = (key: string) => schemaText(schema, key);
  const result = round.result.kind === 'ok'
    ? text('ui.chat.review.recheck_ok')
    : round.result.kind === 'rest'
      ? schemaCountMessage(schema, 'ui.chat.review.recheck_rest', round.result.count, { count: round.result.count })
      : text('ui.chat.review.pending_recheck');
  return (
    <p className="text-[11px] leading-relaxed text-muted-foreground" data-testid="chat-review-result">
      <span className="text-foreground/70">{text('ui.chat.review.result')}</span>
      {' '}
      {result}
    </p>
  );
}

/** One review call: one line, its thumbnails, and its details when opened. */
function ReviewLine({
  schema, call, index, expanded, onToggle,
}: {
  schema: ProductSchema;
  call: ReviewCallModel;
  index: number;
  expanded: boolean;
  onToggle: (index: number) => void;
}) {
  const text = (key: string) => schemaText(schema, key);
  return (
    <div className="flex min-w-0 flex-col gap-1" data-testid="chat-review-line" data-kind={call.kind}>
      <div className="flex min-w-0 items-start gap-1.5">
        <HintTooltip name={reviewLine(schema, call.summary)} className="min-w-0 flex-1">
          <button
            type="button"
            className="w-full min-w-0 truncate text-left text-xs text-foreground hover:text-primary focus-visible:outline-2 focus-visible:outline-ring"
            aria-expanded={expanded}
            onClick={() => onToggle(index)}
            data-testid="chat-review-line-toggle"
          >
            {reviewLine(schema, call.summary)}
          </button>
        </HintTooltip>
        {call.pictures.length > 0 && (
          <Thumbnails
            schema={schema}
            pictures={call.pictures}
            labelOf={(index) => {
              const view = call.summary.views[index];
              return view ? reviewViewName(schema, view) : reviewLine(schema, call.summary);
            }}
          />
        )}
      </div>
      {expanded && <ReviewDetails schema={schema} call={call} />}
    </div>
  );
}

function ReviewDetails({ schema, call }: { schema: ProductSchema; call: ReviewCallModel }) {
  const text = (key: string) => schemaText(schema, key);
  const checks = call.summary.usage?.checks ?? [];
  if (call.kind === 'inspect' && checks.length > 0) {
    return (
      <ul className="flex flex-col gap-0.5 pl-1 text-[11px] text-muted-foreground" data-testid="chat-review-details">
        {checks.map((check) => (
          <li key={check.check} className="flex flex-wrap items-baseline gap-1">
            <span className={cn(check.status === 'fail' && 'text-warning')}>{reviewCheckName(schema, check.check)}</span>
            <span>·</span>
            <span>{checkStatusText(schema, check)}</span>
            {check.fixHint && check.status === 'fail' && <span className="text-muted-foreground/80">{check.fixHint}</span>}
          </li>
        ))}
        <li className="pt-0.5 text-muted-foreground/80">
          {call.summary.usage?.purposeLabel || call.summary.usage?.purpose}
          {call.summary.usage?.note ? ` · ${call.summary.usage.note}` : ''}
        </li>
      </ul>
    );
  }
  return (
    <div className="flex flex-col gap-0.5 pl-1 text-[11px] text-muted-foreground" data-testid="chat-review-details">
      {call.summary.views.length > 0 && (
        <p>{call.summary.views.map((view) => reviewViewName(schema, view)).join(text('ui.chat.review.views_separator'))}</p>
      )}
      {call.summary.failedViews.length > 0 && (
        <p className="text-warning">
          {schemaCountMessage(schema, 'ui.chat.review.failed_views', call.summary.failedViews.length, { count: call.summary.failedViews.length })}
          {' · '}
          {call.summary.failedViews.map((view) => reviewViewName(schema, view)).join(text('ui.chat.review.views_separator'))}
        </p>
      )}
    </div>
  );
}

function checkStatusText(schema: ProductSchema, check: ReviewCheckItem): string {
  const key = check.status === 'pass'
    ? 'ui.chat.review.check_pass'
    : check.status === 'fail' ? 'ui.chat.review.check_fail' : 'ui.chat.review.check_unknown';
  return formatSchemaText(schemaText(schema, key), { label: reviewCheckName(schema, check.check) });
}

/** Rendered pictures at thumbnail size; a click opens the original large on the stage. */
function Thumbnails({
  schema, pictures, labelOf,
}: {
  schema: ProductSchema;
  pictures: readonly MessagePicture[];
  labelOf: (index: number) => string;
}) {
  const { previewAssetUrl } = useAssetURLRewriter();
  const openStageImage = useStudioStore((state) => state.openStageImage);
  const size = schema.chat.frontend.review_thumbnail_pixels;
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-1" data-testid="chat-review-thumbnails">
      {pictures.map((picture, index) => {
        const src = previewAssetUrl(picture.image.url);
        const label = labelOf(index);
        return (
          <HintTooltip key={`${picture.image.url}-${picture.index}`} name={label} className="shrink-0">
            <button
              type="button"
              className="overflow-hidden rounded border border-border/70 bg-muted focus-visible:outline-2 focus-visible:outline-ring"
              style={{ width: size, height: size }}
              aria-label={label}
              onClick={(event) => {
                event.stopPropagation();
                if (picture.image.url) openStageImage({ url: picture.image.url, label });
              }}
              data-testid="chat-review-thumbnail"
            >
              {src
                ? <img src={src} alt={label} className="size-full object-cover" loading="lazy" />
                : <ImageIcon className="m-auto size-4 text-muted-foreground" aria-hidden />}
            </button>
          </HintTooltip>
        );
      })}
    </div>
  );
}
