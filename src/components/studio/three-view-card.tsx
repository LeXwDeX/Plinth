'use client';

import { useState } from 'react';
import { Box, Check, Image as ImageIcon, Layers, RotateCcw, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useThreeViews } from '@/hooks/use-three-views';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import { slotAction, succeededCount, type ThreeViewGroup, type ThreeViewSlot } from '@/lib/workspace/three-view';
import { cn } from '@/lib/utils';
import { formatNativePrice } from '@/lib/pricing';
import { useChatStore } from '@/store/chat-store';
import type { CurrentObject } from '@/store/workspace-store';

const text = (schema: ProductSchema, key: string) => schemaText(schema, `ui.studio.three_view.${key}`);

function SlotThumb({ src, status, label }: { src: string; status: ThreeViewSlot['status']; label: string }) {
  const [failed, setFailed] = useState(false);
  const working = status === 'starting' || status === 'running';
  return (
    <span className="relative flex aspect-square w-full items-center justify-center overflow-hidden rounded-md bg-muted" data-testid="three-view-thumb" data-state={src && !failed ? 'image' : 'placeholder'}>
      {src && !failed
        ? <img src={src} alt={label} onError={() => setFailed(true)} className="size-full object-cover" />
        : <ImageIcon aria-hidden className="size-5 text-muted-foreground" />}
      {working ? <span className="absolute inset-0 animate-pulse bg-primary/10" aria-hidden /> : null}
      {status === 'failed' || status === 'denied' ? (
        <span className="absolute inset-0 flex items-center justify-center bg-destructive/15"><TriangleAlert aria-hidden className="size-4 text-destructive" /></span>
      ) : null}
    </span>
  );
}

export interface ThreeViewCardViewProps {
  schema: ProductSchema;
  group: ThreeViewGroup;
  /** `${runId}:${slotKey}` of the view being asked right now. */
  busy: string | null;
  thumbnail: (slot: ThreeViewSlot) => string;
  onAdvance: (slot: ThreeViewSlot) => void;
  onView: (slot: ThreeViewSlot) => void;
  onToThreeD: () => void;
}

/**
 * 三视图卡片：一组三个视图，每个视图有自己的状态、第几次尝试和一个按钮。
 * 能不能开始 / 重试由服务端的 startable / retryable 决定，这里只照着画；
 * 做 3D 只用正面图，卡片上直接写明。
 */
export function ThreeViewCardView({ schema, group, busy, thumbnail, onAdvance, onView, onToThreeD }: ThreeViewCardViewProps) {
  const done = succeededCount(group);
  const total = group.slots.length;
  const front = group.slots.find((slot) => slot.key === group.modelSource.slot);
  return (
    <article
      data-testid="three-view-card"
      data-run={group.runId}
      data-complete={group.complete}
      aria-label={group.label || text(schema, 'title')}
      className="flex flex-col gap-2 rounded-lg border border-border bg-panel-raised p-3 text-xs"
    >
      <header className="flex items-center gap-2">
        <Layers aria-hidden className="size-4 shrink-0 text-primary" />
        <span className="text-[13px] font-semibold">{group.label || text(schema, 'title')}</span>
        <span className="ml-auto text-muted-foreground" data-testid="three-view-progress">
          {group.complete ? text(schema, 'complete') : formatSchemaText(text(schema, 'progress'), { done, total })}
        </span>
      </header>
      <ul className="grid grid-cols-3 gap-2" aria-label={text(schema, 'slots')}>
        {group.slots.map((slot) => {
          const action = slotAction(slot);
          const isBusy = busy === `${group.runId}:${slot.key}`;
          const status = text(schema, `status.${slot.status}`);
          const native = slot.nativeQuote !== undefined;
          const nativeAmount = slot.nativeQuote ? formatNativePrice(slot.nativeQuote.total_amount, slot.nativeQuote.unit_label) : null;
          const nativePrice = formatSchemaText(schemaText(schema, slot.priceSource === 'default' ? 'ui.pricing.default_estimate' : 'ui.pricing.frozen_estimate'), { amount: nativeAmount ?? schemaText(schema, 'ui.pricing.unknown') });
          return (
            <li key={slot.key} className="flex min-w-0 flex-col gap-1" data-testid="three-view-slot" data-slot={slot.key} data-status={slot.status}>
              <SlotThumb src={thumbnail(slot)} status={slot.status} label={slot.label} />
              <div className="flex min-w-0 items-center gap-1">
                <span className="truncate font-medium">{slot.label}</span>
                {slot.attempt > 1 ? (
                  <span className="shrink-0 rounded-sm bg-muted px-1 text-[10px] text-muted-foreground" data-testid="three-view-attempt">
                    {formatSchemaText(text(schema, 'attempt'), { attempt: slot.attempt })}
                  </span>
                ) : null}
              </div>
              <span
                className={cn(
                  'flex items-center gap-1 text-[11px]',
                  slot.status === 'failed' || slot.status === 'denied' ? 'text-destructive'
                    : slot.status === 'succeeded' ? 'text-success' : 'text-muted-foreground',
                )}
                data-testid="three-view-status"
              >
                {slot.status === 'succeeded' ? <Check aria-hidden className="size-3" /> : null}
                {status}
              </span>
              {slot.status === 'awaiting_approval' ? (
                <span className="text-[11px] leading-4 text-muted-foreground">{text(schema, 'waiting_approval')}</span>
              ) : null}
              {action ? (
                <Action
                  type="button"
                  size="xs"
                  variant={action === 'start' ? 'default' : 'outline'}
                  loading={isBusy}
                  disabled={busy !== null || (native && nativeAmount === null)}
                  data-testid={action === 'start' ? 'three-view-start' : 'three-view-retry'}
                  onClick={() => onAdvance(slot)}
                >
                  {action === 'retry' ? <RotateCcw /> : null}
                  {!native && slot.creditsMetered && slot.estimatedCredits > 0
                    ? formatSchemaText(text(schema, `${action}_with_cost`), { credits: slot.estimatedCredits })
                    : text(schema, action)}
                </Action>
              ) : slot.status === 'succeeded' ? (
                <Action type="button" size="xs" variant="ghost" data-testid="three-view-open" onClick={() => onView(slot)}>
                  {text(schema, 'view')}
                </Action>
              ) : slot.status === 'failed' ? (
                <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-no-retry">{text(schema, 'no_retry')}</span>
              ) : null}
              {action && native ? <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-native-price" data-price-source={slot.priceSource}>{nativePrice}</span> : null}
            </li>
          );
        })}
      </ul>
      <footer className="flex flex-col gap-1.5 border-t border-border pt-2">
        <div className="flex items-center gap-2">
          <Action
            type="button"
            size="xs"
            variant="outline"
            disabled={!group.modelSource.available || !front}
            data-testid="three-view-to-3d"
            onClick={onToThreeD}
          >
            <Box />{text(schema, 'to_3d')}
          </Action>
          {!group.modelSource.available ? <span className="text-[11px] text-muted-foreground">{text(schema, 'to_3d_wait')}</span> : null}
        </div>
        {group.modelSource.note ? (
          <p className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-source-note">{group.modelSource.note}</p>
        ) : null}
      </footer>
    </article>
  );
}

export function ThreeViewCard({ group }: { group: ThreeViewGroup }) {
  const { schema } = useProductSchema();
  const { busy, advance } = useThreeViews();
  const { openOnStage, toThreeD } = useObjectActions();
  const sessionId = useChatStore((state) => state.activeSessionId);

  const urlOf = (taskId: string, assetId: string) => (taskId && assetId ? safeAssetURL(taskAssetRoute(schema, taskId, assetId)) : '');
  const objectOf = (taskId: string, assetId: string): CurrentObject | null => {
    const url = urlOf(taskId, assetId);
    return url && sessionId ? { taskId, kind: 'image', url, assetId, imageUrls: [url], sessionId } : null;
  };

  return (
    <ThreeViewCardView
      schema={schema}
      group={group}
      busy={busy}
      thumbnail={(slot) => urlOf(slot.taskId, slot.outputAssetId)}
      onAdvance={(slot) => {
        void advance(group, slot).then((message) => { if (message) toast.error(message); });
      }}
      onView={(slot) => {
        const object = objectOf(slot.taskId, slot.outputAssetId);
        if (object) openOnStage(object);
      }}
      onToThreeD={() => {
        // The model is made from the front view only; the card says so next to the button.
        const object = objectOf(group.modelSource.taskId, group.modelSource.assetId);
        if (object) toThreeD(object);
      }}
    />
  );
}
