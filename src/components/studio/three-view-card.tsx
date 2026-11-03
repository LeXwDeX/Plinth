'use client';

import { useRef, useState, type DragEvent } from 'react';
import { Box, Check, Image as ImageIcon, Layers, RotateCcw, TriangleAlert, Upload } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useThreeViews } from '@/hooks/use-three-views';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import {
  groupMultiview, pickMultiviewModel, remainingCredits, remainingSlots, slotAction, slotReplaceable, succeededCount,
  type MultiviewModel, type ThreeViewGroup, type ThreeViewSlot,
} from '@/lib/workspace/three-view';
import { cn } from '@/lib/utils';
import { formatNativePrice } from '@/lib/pricing';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { useWorkspaceStore } from '@/store/workspace-store';
import type { CurrentObject } from '@/store/workspace-store';

const text = (schema: ProductSchema, key: string) => schemaText(schema, `ui.studio.three_view.${key}`);

function SlotThumb({ src, status, label, onDropPicture }: {
  src: string;
  status: ThreeViewSlot['status'];
  label: string;
  /** Set while the view may be replaced: a picture dropped on the thumbnail replaces it (#65). */
  onDropPicture?: (picture: File) => void;
}) {
  const [failed, setFailed] = useState(false);
  const [over, setOver] = useState(false);
  const working = status === 'starting' || status === 'running';
  const drop = onDropPicture ? {
    onDragOver: (event: DragEvent) => { event.preventDefault(); setOver(true); },
    onDragLeave: () => setOver(false),
    onDrop: (event: DragEvent) => {
      event.preventDefault();
      setOver(false);
      const picture = event.dataTransfer.files[0];
      if (picture) onDropPicture(picture);
    },
  } : {};
  return (
    <span
      className={cn('relative flex aspect-square w-full items-center justify-center overflow-hidden rounded-md bg-muted', over && 'ring-2 ring-primary')}
      data-testid="three-view-thumb"
      data-state={src && !failed ? 'image' : 'placeholder'}
      {...drop}
    >
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
  /** `${runId}:${slotKey}` of views queued behind the in-flight limit; they resubmit by themselves. */
  queued?: ReadonlySet<string>;
  thumbnail: (slot: ThreeViewSlot) => string;
  onAdvance: (slot: ThreeViewSlot) => void;
  onView: (slot: ThreeViewSlot) => void;
  /** 只用正面转 3D: the existing single-picture path. */
  onToThreeD: () => void;
  /** The multiview model the card offers (#65); null or absent when no available Offering takes several views. */
  model?: MultiviewModel | null;
  /** 用三视图生成 3D: the structured request of the group's finished views. */
  onMultiview?: () => void;
  /** 生成剩余视图: starts each required view not started yet, one approval each. */
  onStartRemaining?: () => void;
  /** A picture the person chose or dropped replaces one view. */
  onReplace?: (slot: ThreeViewSlot, picture: File) => void;
}

/**
 * 三视图卡片：一组视图（正 / 左 / 背，右可选，视角键与多视图模型一致，#65），每个视图有自己的状态、第几次尝试、
 * 按钮，并可拖入或上传图片替换。能不能开始 / 重试由服务端的 startable / retryable 决定，这里只照着画。
 * 「用三视图生成 3D」把已完成视图按视角交给 Schema 声明了多视角输入的模型；「只用正面转 3D」沿用单图转 3D。
 */
export function ThreeViewCardView({
  schema, group, busy, queued, thumbnail, onAdvance, onView, onToThreeD, model, onMultiview, onStartRemaining, onReplace,
}: ThreeViewCardViewProps) {
  const done = succeededCount(group);
  const total = group.slots.filter((slot) => !slot.optional).length;
  const front = group.slots.find((slot) => slot.key === group.modelSource.slot);
  const remaining = remainingSlots(group);
  const restCredits = remainingCredits(remaining);
  const multiview = model ? groupMultiview(group, model) : null;
  const separator = text(schema, 'view_separator');
  const usedViews = multiview?.slots.map((slot) => slot.label).join(separator) ?? '';
  const picker = useRef<HTMLInputElement>(null);
  const [replacing, setReplacing] = useState<ThreeViewSlot | null>(null);
  const accept = model?.mediaTypes.length ? model.mediaTypes.join(',') : 'image/png,image/jpeg';
  const replaceWith = (slot: ThreeViewSlot, picture: File) => {
    if (!onReplace) return;
    if (!accept.split(',').includes(picture.type)) {
      toast.error(text(schema, 'replace_type'));
      return;
    }
    onReplace(slot, picture);
  };
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
      {onReplace ? (
        <input
          ref={picker}
          type="file"
          accept={accept}
          className="hidden"
          data-testid="three-view-replace-input"
          onChange={(event) => {
            const picture = event.target.files?.[0];
            if (picture && replacing) replaceWith(replacing, picture);
            event.target.value = '';
            setReplacing(null);
          }}
        />
      ) : null}
      <ul className={cn('grid gap-2', group.slots.length > 3 ? 'grid-cols-4' : 'grid-cols-3')} aria-label={text(schema, 'slots')}>
        {group.slots.map((slot) => {
          const action = slotAction(slot);
          const isQueued = queued?.has(`${group.runId}:${slot.key}`) ?? false;
          const isBusy = busy === `${group.runId}:${slot.key}` || isQueued;
          const status = text(schema, `status.${slot.status}`);
          const native = slot.nativeQuote !== undefined;
          const nativeAmount = slot.nativeQuote ? formatNativePrice(slot.nativeQuote.total_amount, slot.nativeQuote.unit_label) : null;
          const nativePrice = formatSchemaText(schemaText(schema, slot.priceSource === 'default' ? 'ui.pricing.default_estimate' : 'ui.pricing.frozen_estimate'), { amount: nativeAmount ?? schemaText(schema, 'ui.pricing.unknown') });
          return (
            <li key={slot.key} className="flex min-w-0 flex-col gap-1" data-testid="three-view-slot" data-slot={slot.key} data-status={slot.status} data-replaced={slot.replaced}>
              <SlotThumb
                src={thumbnail(slot)}
                status={slot.status}
                label={slot.label}
                {...(onReplace && slotReplaceable(slot) && busy === null ? { onDropPicture: (picture: File) => replaceWith(slot, picture) } : {})}
              />
              <div className="flex min-w-0 items-center gap-1">
                <span className="truncate font-medium">{slot.label}</span>
                {slot.optional ? (
                  <span className="shrink-0 rounded-sm bg-muted px-1 text-[10px] text-muted-foreground" data-testid="three-view-optional">{text(schema, 'optional')}</span>
                ) : null}
                {slot.replaced ? (
                  <span className="shrink-0 rounded-sm bg-muted px-1 text-[10px] text-muted-foreground" data-testid="three-view-replaced">{text(schema, 'replaced')}</span>
                ) : null}
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
              {isQueued ? (
                <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-queued">{text(schema, 'queued')}</span>
              ) : null}
              {action ? (
                <Action
                  type="button"
                  size="xs"
                  variant={action === 'start' ? 'default' : 'outline'}
                  loading={isBusy}
                  disabled={busy !== null || isQueued || (native && nativeAmount === null)}
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
              {onReplace && slotReplaceable(slot) ? (
                <Action
                  type="button"
                  size="xs"
                  variant="ghost"
                  disabled={busy !== null || isQueued}
                  aria-description={text(schema, 'replace_hint')}
                  data-testid="three-view-replace"
                  onClick={() => { setReplacing(slot); picker.current?.click(); }}
                >
                  <Upload />{text(schema, 'replace')}
                </Action>
              ) : null}
            </li>
          );
        })}
      </ul>
      <footer className="flex flex-col gap-1.5 border-t border-border pt-2">
        {onStartRemaining && remaining.length > 0 ? (
          <Action
            type="button"
            size="xs"
            variant="default"
            className="self-start"
            disabled={busy !== null || remaining.some((slot) => queued?.has(`${group.runId}:${slot.key}`))}
            data-testid="three-view-start-remaining"
            onClick={onStartRemaining}
          >
            {restCredits > 0 ? formatSchemaText(text(schema, 'generate_rest_with_cost'), { credits: restCredits }) : text(schema, 'generate_rest')}
          </Action>
        ) : null}
        {model && multiview && onMultiview ? (
          <div className="flex flex-wrap items-center gap-2">
            <Action
              type="button"
              size="xs"
              variant="default"
              disabled={!multiview.ready}
              data-testid="three-view-multiview"
              onClick={onMultiview}
            >
              <Box />{formatSchemaText(text(schema, 'to_3d_multiview'), { model: model.modelName })}
            </Action>
            <span className="text-[11px] text-muted-foreground" data-testid="three-view-multiview-views">
              {multiview.ready ? formatSchemaText(text(schema, 'multiview_uses'), { views: usedViews }) : text(schema, 'multiview_wait')}
            </span>
          </div>
        ) : null}
        <div className="flex items-center gap-2">
          <Action
            type="button"
            size="xs"
            variant="outline"
            disabled={!group.modelSource.available || !front}
            data-testid="three-view-to-3d"
            onClick={onToThreeD}
          >
            <Box />{text(schema, 'to_3d_front_only')}
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
  const { busy, queued, advance, startRemaining, replace } = useThreeViews();
  const { openOnStage, toThreeD, multiviewToThreeD } = useObjectActions();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const { offeringsByCapability } = useWorkspaceOfferings(schema, 'model-3d');
  const picked = useWorkspaceStore((state) => state.modelByDomain['model-3d']);
  const model = pickMultiviewModel(offeringsByCapability, picked ?? undefined);

  const urlOf = (taskId: string, assetId: string) => (taskId && assetId ? safeAssetURL(taskAssetRoute(schema, taskId, assetId)) : '');
  /** A view the person replaced is an input asset of theirs, with no Task. */
  const inputURL = (assetId: string) => (assetId ? safeAssetURL(schema.runtime.http.routes.asset_item.replace('{assetID}', encodeURIComponent(assetId))) : '');
  const report = (message: string | null) => { if (message) toast.error(message); };
  const objectOf = (taskId: string, assetId: string): CurrentObject | null => {
    const url = urlOf(taskId, assetId);
    return url && sessionId ? { taskId, kind: 'image', url, assetId, imageUrls: [url], sessionId } : null;
  };

  return (
    <ThreeViewCardView
      schema={schema}
      group={group}
      busy={busy}
      queued={queued}
      thumbnail={(slot) => (slot.replaced ? inputURL(slot.outputAssetId) : urlOf(slot.taskId, slot.outputAssetId))}
      model={model}
      onAdvance={(slot) => { void advance(group, slot).then(report); }}
      onStartRemaining={() => { void startRemaining(group).then(report); }}
      onReplace={(slot, picture) => { void replace(group, slot, picture).then(report); }}
      onMultiview={() => { if (model) multiviewToThreeD(group, model); }}
      onView={(slot) => {
        if (slot.replaced) {
          const url = inputURL(slot.outputAssetId);
          if (url) window.open(url, '_blank', 'noopener');
          return;
        }
        const object = objectOf(slot.taskId, slot.outputAssetId);
        if (object) openOnStage(object);
      }}
      onToThreeD={() => {
        // 只用正面转 3D: the front view alone; the card says so next to the button.
        const object = group.modelSource.taskId
          ? objectOf(group.modelSource.taskId, group.modelSource.assetId)
          : null;
        if (object) toThreeD(object);
      }}
    />
  );
}
