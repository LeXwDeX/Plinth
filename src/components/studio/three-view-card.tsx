'use client';

import { useRef, useState, type DragEvent } from 'react';
import { Box, Check, Image as ImageIcon, Layers, RotateCcw, TriangleAlert, Upload } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useThreeViews } from '@/hooks/use-three-views';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { unavailableLabel } from '@/lib/unavailable';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import {
  fourViewRequested, groupMultiview, groupProgress, multiviewPickState, pickMultiviewModel, remainingCredits, remainingSlots, requestedView, slotAction, slotReplaceable, threeViewAutoContinuing,
  type MultiviewModel, type ThreeViewGroup, type ThreeViewSlot,
} from '@/lib/workspace/three-view';
import { cn } from '@/lib/utils';
import { formatNativePrice } from '@/lib/pricing';
import { defaultApprovalMode } from '@/lib/chat/approval-modes';
import { useChatStore } from '@/store/chat-store';
import { useModel3DConfirmStore } from '@/store/model3d-confirm-store';
import { stageTurnPending, useStudioStore } from '@/store/studio-store';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { useWorkspaceStore } from '@/store/workspace-store';
import type { CurrentObject } from '@/store/workspace-store';

const text = (schema: ProductSchema, key: string) => schemaText(schema, `ui.studio.three_view.${key}`);

/**
 * #618: a view's buttons fill their own column and wrap their words there. Buttons keep to one line by default, so
 * a long 「继续生成（约 9 积分）」 in a narrow column ran over the next view's 「查看」.
 */
const SLOT_BUTTON = 'h-auto min-h-6 w-full min-w-0 whitespace-normal py-0.5 text-center leading-4';

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
      {status === 'failed' || status === 'denied' || status === 'rejected' ? (
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
  /** 只用正面转 3D: the front view alone. False when nothing was asked (the button stays usable). */
  onToThreeD: () => boolean | void;
  /** The multiview model the card offers (#65); null or absent when no available Offering takes several views. */
  model?: MultiviewModel | null;
  /**
   * #691: how the person's 3D pick stands to the multiview model: `picked` runs it, `none` asks first (the press
   * still calls onMultiview), `single` (a pick that takes one picture) keeps the button off with the reason.
   */
  multiviewState?: 'picked' | 'none' | 'single';
  /** 用三视图生成 3D: the structured request of the group's finished views. False when nothing was asked. */
  onMultiview?: () => boolean | void;
  /** The session's turn is running: the two buttons that ask for a turn wait for it (#691: no double turns). */
  turnRunning?: boolean;
  /**
   * #691: a turn a stage action asked for is not sent yet or is still being sent, or the 3D model confirmation is open
   * (`useTurnButtonsPending`). The two turn buttons wait for it too; nothing else keeps them off.
   */
  turnPending?: boolean;
  /**
   * #693: the server is carrying the group on by itself (an automatic-approval session whose turn or views are in
   * flight): the required views not started yet say they come later, and no manual start is offered for them.
   */
  autoContinuing?: boolean;
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
  schema, group, busy, queued, thumbnail, onAdvance, onView, onToThreeD, model, multiviewState = 'picked', onMultiview, onStartRemaining, onReplace,
  turnRunning = false, turnPending = false, autoContinuing = false,
}: ThreeViewCardViewProps) {
  // #633: the same count as the version strip; a started right view (a four-view request) is part of it.
  const { done, total } = groupProgress(group.slots);
  // #633: a four-view request names itself; the group's label is the one fixed three-view name.
  const cardTitle = fourViewRequested(group.slots) ? text(schema, 'title_four') : group.label || text(schema, 'title');
  const front = group.slots.find((slot) => slot.key === group.modelSource.slot);
  const remaining = remainingSlots(group);
  const restCredits = remainingCredits(remaining);
  const multiview = model ? groupMultiview(group, model) : null;
  const separator = text(schema, 'view_separator');
  const usedViews = multiview?.slots.map((slot) => slot.label).join(separator) ?? '';
  const picker = useRef<HTMLInputElement>(null);
  // #691: a press of 用三视图生成 3D or 只用正面转 3D asks for a turn; while that request waits, is sent or runs (or its
  // model confirmation is open) neither may be pressed again, so a double click never queues two paid turns. The
  // state is read from the request itself: a request dropped or refused before a turn started leaves them usable.
  const turnButtonsOff = turnPending || turnRunning;
  const askTurn = (run: () => boolean | void) => {
    if (turnButtonsOff) return;
    run();
  };
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
      aria-label={cardTitle}
      className="flex flex-col gap-2 rounded-lg border border-border bg-panel-raised p-3 text-xs"
    >
      <header className="flex items-center gap-2">
        <Layers aria-hidden className="size-4 shrink-0 text-primary" />
        <span className="text-[13px] font-semibold">{cardTitle}</span>
        <span className="ml-auto text-muted-foreground" data-testid="three-view-progress">
          {group.complete && done === total ? text(schema, 'complete') : formatSchemaText(text(schema, 'progress'), { done, total })}
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
          // #693: a required view the automatic continuation will start has nothing to press.
          const autoLater = autoContinuing && !slot.optional && slot.status === 'not_started';
          const action = autoLater ? null : slotAction(slot);
          // #691: starting the optional right view has its own words and a quieter button: a three-view does not need it.
          const optionalStart = action === 'start' && slot.optional;
          const actionKey = optionalStart ? 'start_optional' : action;
          const actionText = !action || !actionKey ? ''
            : slot.nativeQuote === undefined && slot.creditsMetered && slot.estimatedCredits > 0
              ? formatSchemaText(text(schema, `${actionKey}_with_cost`), { credits: slot.estimatedCredits, view: slot.label })
              : formatSchemaText(text(schema, actionKey), { view: slot.label });
          // #691: a screen reader hears which view a button acts on.
          const named = (label: string) => [slot.label, label].join(' · ');
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
                {!requestedView(slot) ? (
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
                  slot.status === 'failed' || slot.status === 'denied' || slot.status === 'rejected' ? 'text-destructive'
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
              {autoLater ? (
                <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-auto-later">{text(schema, 'auto_later')}</span>
              ) : action ? (
                <HintTooltip
                  name={optionalStart ? formatSchemaText(text(schema, 'start_optional'), { view: slot.label }) : text(schema, action)}
                  reason={native && nativeAmount === null ? schemaText(schema, 'ui.unavailable.reason.three_view.price_unknown') : undefined}
                  hint={optionalStart ? text(schema, 'optional_hint') : undefined}
                  className="w-full min-w-0"
                >
                  <Action
                    type="button"
                    size="xs"
                    className={SLOT_BUTTON}
                    variant={action === 'start' && !optionalStart ? 'default' : 'outline'}
                    loading={isBusy}
                    disabled={busy !== null || isQueued || (native && nativeAmount === null)}
                    data-testid={action === 'start' ? 'three-view-start' : 'three-view-retry'}
                    data-optional={optionalStart ? 'true' : undefined}
                    aria-label={named(actionText)}
                    onClick={() => onAdvance(slot)}
                  >
                    {action === 'retry' ? <RotateCcw /> : null}
                    {actionText}
                  </Action>
                </HintTooltip>
              ) : slot.status === 'succeeded' ? (
                <Action type="button" size="xs" variant="ghost" className={SLOT_BUTTON} data-testid="three-view-open" aria-label={named(text(schema, 'view'))} onClick={() => onView(slot)}>
                  {text(schema, 'view')}
                </Action>
              ) : slot.status === 'failed' ? (
                <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-no-retry">{text(schema, 'no_retry')}</span>
              ) : null}
              {optionalStart ? <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-optional-hint">{text(schema, 'optional_hint')}</span> : null}
              {action && native ? <span className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-native-price" data-price-source={slot.priceSource}>{nativePrice}</span> : null}
              {onReplace && slotReplaceable(slot) ? (() => {
                // #691: replacing the front view leaves the other views made from the old one; the hint says so.
                const replaceHint = text(schema, slot.key === group.modelSource.slot ? 'replace_front_hint' : 'replace_hint');
                return (
                  <HintTooltip name={text(schema, 'replace')} hint={replaceHint} className="w-full min-w-0">
                    <Action
                      type="button"
                      size="xs"
                      variant="ghost"
                      className={SLOT_BUTTON}
                      disabled={busy !== null || isQueued}
                      aria-label={named(text(schema, 'replace'))}
                      aria-description={replaceHint}
                      data-testid="three-view-replace"
                      onClick={() => { setReplacing(slot); picker.current?.click(); }}
                    >
                      <Upload />{text(schema, 'replace')}
                    </Action>
                  </HintTooltip>
                );
              })() : null}
            </li>
          );
        })}
      </ul>
      <footer className="flex flex-col gap-1.5 border-t border-border pt-2">
        {onStartRemaining && remaining.length > 0 && !autoContinuing ? (
          <Action
            type="button"
            size="xs"
            variant="default"
            className="self-start"
            disabled={busy !== null || remaining.some((slot) => queued?.has(`${group.runId}:${slot.key}`))}
            data-testid="three-view-start-remaining"
            onClick={onStartRemaining}
          >
            {restCredits > 0 ? schemaCountMessage(schema, 'ui.studio.three_view.generate_rest_with_cost', restCredits, { credits: restCredits }) : text(schema, 'generate_rest')}
          </Action>
        ) : null}
        {model && multiview && onMultiview ? (
          <div className="flex flex-wrap items-center gap-2">
            <Action
              type="button"
              size="xs"
              variant="default"
              disabled={!multiview.ready || multiviewState === 'single' || turnButtonsOff}
              data-testid="three-view-multiview"
              onClick={() => askTurn(onMultiview)}
            >
              <Box />{formatSchemaText(text(schema, 'to_3d_multiview'), { model: model.modelName })}
            </Action>
            {multiviewState === 'single' ? (
              <UnavailableNotice
                testId="three-view-multiview-views"
                className="text-[11px]"
                label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.three_view.multiview_single_model'))}
                detail={text(schema, 'multiview_single_model')}
              />
            ) : multiview.ready ? (
              <span className="text-[11px] text-muted-foreground" data-testid="three-view-multiview-views">
                {formatSchemaText(text(schema, 'multiview_uses'), { views: usedViews })}
              </span>
            ) : (
              <UnavailableNotice
                testId="three-view-multiview-views"
                className="text-[11px]"
                label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.three_view.multiview_wait'))}
                detail={text(schema, 'multiview_wait')}
              />
            )}
          </div>
        ) : null}
        <div className="flex items-center gap-2">
          <Action
            type="button"
            size="xs"
            variant="outline"
            disabled={!group.modelSource.available || !front || turnButtonsOff}
            data-testid="three-view-to-3d"
            onClick={() => askTurn(onToThreeD)}
          >
            <Box />{text(schema, 'to_3d_front_only')}
          </Action>
          {!group.modelSource.available || !front ? (
            <UnavailableNotice
              testId="three-view-to-3d-wait"
              className="text-[11px]"
              label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.three_view.front_wait'))}
              detail={text(schema, 'to_3d_wait')}
            />
          ) : null}
        </div>
        {group.modelSource.note ? (
          <p className="text-[11px] leading-4 text-muted-foreground" data-testid="three-view-source-note">{group.modelSource.note}</p>
        ) : null}
      </footer>
    </article>
  );
}

/**
 * #691: what keeps the card's two turn buttons off besides a running turn: a stage request not sent yet or still
 * being sent (the chat column counts it until the send is over), or the 3D model confirmation waiting for an answer.
 */
export function useTurnButtonsPending(): boolean {
  const requested = useStudioStore(stageTurnPending);
  const confirming = useModel3DConfirmStore((state) => state.request !== null);
  return requested || confirming;
}

export function ThreeViewCard({ group }: { group: ThreeViewGroup }) {
  const { schema } = useProductSchema();
  const { busy, queued, advance, startRemaining, replace, turnRunning } = useThreeViews();
  const approvalMode = useChatStore((state) => state.sessions.find((item) => item.id === state.activeSessionId)?.approvalMode) ?? defaultApprovalMode(schema);
  // #693: in an automatic-approval session the server starts the remaining views itself, inside the session's turn
  // (continueThreeView). Without a turn nobody carries the group on, even while a view the person started runs.
  const autoContinuing = threeViewAutoContinuing(approvalMode, turnRunning);
  const turnPending = useTurnButtonsPending();
  const { viewOnStage, toThreeD, inputPictureToThreeD, multiviewToThreeD } = useObjectActions();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const { offeringsByCapability } = useWorkspaceOfferings(schema, 'model-3d');
  const picked = useWorkspaceStore((state) => state.modelByDomain['model-3d']);
  const model = pickMultiviewModel(offeringsByCapability, picked ?? undefined);
  const multiviewState = multiviewPickState(model, picked);

  const urlOf = (taskId: string, assetId: string) => (taskId && assetId ? safeAssetURL(taskAssetRoute(schema, taskId, assetId)) : '');
  /** A view the person replaced is an input asset of theirs, with no Task. */
  const inputURL = (assetId: string) => (assetId ? safeAssetURL(schema.runtime.http.routes.asset_item.replace('{assetID}', encodeURIComponent(assetId))) : '');
  const report = (message: string | null) => { if (message) toast.error(message); };
  // #691: the address the stage and the board use for this picture (the stage header's front view does the same).
  const objectOf = (taskId: string, assetId: string): CurrentObject | null => {
    const url = taskId && assetId ? proxyAssetUrl(taskAssetRoute(schema, taskId, assetId), taskId) : '';
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
      multiviewState={multiviewState}
      turnRunning={turnRunning}
      turnPending={turnPending}
      autoContinuing={autoContinuing}
      onAdvance={(slot) => { void advance(group, slot).then(report); }}
      onStartRemaining={() => { void startRemaining(group).then(report); }}
      onReplace={(slot, picture) => { void replace(group, slot, picture).then(report); }}
      onMultiview={() => {
        // #691/#678: never a model the person did not pick: without a pick the person confirms this one first
        // (multiviewToThreeD asks); a single-picture pick is off.
        if (!model || multiviewState === 'single') return false;
        return multiviewToThreeD(group, model);
      }}
      onView={(slot) => {
        if (slot.replaced) {
          const url = inputURL(slot.outputAssetId);
          if (url) window.open(url, '_blank', 'noopener');
          return;
        }
        // #691: 查看 only looks at the view (#681): it never puts it on the board.
        const object = objectOf(slot.taskId, slot.outputAssetId);
        if (object) viewOnStage(object);
      }}
      onToThreeD={() => {
        // 只用正面转 3D: the front view alone; the card says so next to the button. A front view the person
        // replaced has no Task: its input asset is the turn's picture. #691: never the board, whatever it holds.
        if (!group.modelSource.taskId) {
          const url = inputURL(group.modelSource.assetId);
          if (!url) {
            toast.error(text(schema, 'replace_failed'));
            return false;
          }
          return inputPictureToThreeD(url);
        }
        const object = objectOf(group.modelSource.taskId, group.modelSource.assetId);
        return object ? toThreeD(object, { pictureOnly: true }) : false;
      }}
    />
  );
}
