'use client';

import { useEffect, useState, type DragEvent } from 'react';
import useSWR from 'swr';
import { Box, Camera, Check, Download, Image as ImageIcon, Info } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { KindBadge, KindBadgeFace, kindBadgeClass } from '@/components/studio/kind-badge';
import { Action, buttonVariants } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { openResultDetail } from '@/components/result/result-card-parts';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useModelCaptureBlock } from '@/hooks/use-model-capture-block';
import { useObjectActions } from '@/hooks/use-object-actions';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import { activePicturePosition, pictureKey, type MessagePicture } from '@/lib/chat/message-pictures';
import type { ChatModelResult } from '@/lib/chat/model-results';
import { canvasDragItem, startCanvasDrag, uploadDragItem } from '@/lib/workspace/canvas-drag';
import { candidateAttachment, isPickedCandidate, pickClaim } from '@/lib/chat/pick';
import { safeAssetURL, safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import type { ChatAttachment, ChatImage } from '@/store/chat-store';
import type { ResultTarget } from '@/lib/workspace/result-target';
import type { CurrentObject } from '@/store/workspace-store';
import { HandleTag } from '@/components/chat/handle-tag';
import type { ComposerReference } from '@/lib/chat/composer-reference';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';
import { ModelArchiveLink } from '@/components/workspace/model-formats';
import { useInlineModelPreviewStore } from '@/store/inline-model-preview-store';
import { fetchCapabilityOfferings } from '@/lib/agent-offerings';
import { canCreateGaussianSplat, gaussianSplatAvailableCount, gaussianSplatTool } from '@/lib/workspace/gaussian-splat-action';
import type { ImageGroupPresentation } from '@/lib/workspace/image-result-group';

export interface ImageResultCardProps {
  /** The message's 2D pictures. Each keeps its index in the message's images: attachments, handle tags, drags, picks and detail targets are all keyed by it. */
  candidates: ReadonlyArray<MessagePicture>;
  /** The same pictures as one declared image group (e.g. 四视角): shown in the group's slot order with each view's label. */
  group?: ImageGroupPresentation<MessagePicture> | null;
  attachments?: ChatAttachment[];
  sessionId?: string | null;
  pick?: ChatAttachment | null;
  pickBusy?: boolean;
  /** Resolves true when the server accepted the pick. */
  onPick?: (claim: ChatAttachmentClaim) => Promise<boolean> | void;
  /** Shows a picture that cannot go on the stage (it has no task of its own): the index in the message's images. */
  onPreview: (index: number) => void;
  /** Accessible name of one picture, e.g. "生成图片". */
  labelPrefix: string;
}

function columnsFor(count: number): string {
  if (count <= 1) return 'grid-cols-1 max-w-64';
  return count <= 4 ? 'grid-cols-2' : 'grid-cols-3';
}

/**
 * 一次出图的结果：候选并排成一个组（不是轮播），每张都有"选用"；点一张图把它放上舞台，
 * 卡片按钮作用于当前高亮的那一张。选用随时可改，不随后续步骤禁用。
 */
export function ImageResultCard({
  candidates: delivered, group: imageGroup, attachments, sessionId, pick, pickBusy, onPick, onPreview, labelPrefix,
}: ImageResultCardProps) {
  const { schema } = useProductSchema();
  const { proxyAssetUrl, previewAssetUrl } = useAssetURLRewriter();
  const { openOnStage, toThreeD, toGaussianSplat } = useObjectActions();
  const splatTool = gaussianSplatTool(schema);
  const { data: splatOfferings, isLoading: splatOfferingsLoading, error: splatOfferingsError } = useSWR(
    splatTool ? ['gaussian-splat-offerings', schema.revision, splatTool.capability] : null,
    () => fetchCapabilityOfferings(schema, splatTool!.capability),
    { revalidateOnFocus: false },
  );
  const splatAvailableCount = gaussianSplatAvailableCount(splatOfferings);
  const splatCanRun = canCreateGaussianSplat(splatTool, splatOfferings);
  const base = schema.chat.context.image_index_base;
  const candidates = imageGroup ? imageGroup.views.map((view) => view.item) : delivered;
  const viewLabelOf = (entry: MessagePicture) => imageGroup?.views.find((view) => view.item === entry)?.label ?? '';
  const pictureLabel = (entry: MessagePicture) => {
    const view = viewLabelOf(entry);
    return view ? `${imageGroup?.title} · ${view}` : `${labelPrefix} ${entry.index + base}`;
  };
  const attachmentOf = (entry: MessagePicture) => candidateAttachment({ attachments }, entry.index);
  const pickedPosition = candidates.findIndex((entry) => isPickedCandidate(pick, entry.image, attachmentOf(entry)));
  // The card's current picture is remembered by identity, so a re-sort (a group's slot order arriving later) keeps it.
  const [chosen, setChosen] = useState<string | null>(null);
  const active = activePicturePosition(candidates, chosen, pickedPosition);
  if (candidates.length === 0) return null;

  const objectAt = (entry: MessagePicture): CurrentObject | null => {
    const attachment = attachmentOf(entry);
    if (!entry.image.taskId || !sessionId) return null;
    return {
      sessionId, taskId: entry.image.taskId, kind: 'image', url: entry.image.url,
      ...(attachment?.assetId ? { assetId: attachment.assetId } : {}),
      ...(attachment?.handle ? { handle: attachment.handle } : {}),
      imageUrls: candidates.filter((item) => item.image.taskId !== '').map((item) => item.image.url),
    };
  };
  const detailTarget = (entry: MessagePicture, withGroup = true): ResultTarget | null => {
    const object = objectAt(entry);
    const group = withGroup ? candidates.map((other) => detailTarget(other, false)).filter((item): item is ResultTarget => item !== null) : [];
    return object ? {
      ...(group.length > 1 ? { group } : {}),
      taskId: object.taskId, sessionId: object.sessionId, kind: 'image', url: object.url, status: 'ready',
      ...(object.assetId ? { assetId: object.assetId } : {}),
      ...(object.imageUrls ? { imageUrls: object.imageUrls } : {}),
      title: pictureLabel(entry),
    } : null;
  };
  const activeEntry = candidates[active];
  const activeObject = objectAt(activeEntry);
  const activeHandle = attachmentOf(activeEntry)?.handle;
  // What a click on a handle tag brings into the composer: the picture by its platform IDs.
  const referenceAt = (entry: MessagePicture): ComposerReference | null => {
    const attachment = attachmentOf(entry);
    if (!attachment?.handle) return null;
    const taskId = entry.image.taskId || attachment.taskId;
    return {
      handle: attachment.handle, url: attachment.url ?? entry.image.url,
      ...(attachment.assetId ? { assetId: attachment.assetId } : {}),
      ...(taskId ? { taskId } : {}),
      ...(attachment.captureId ? { captureId: attachment.captureId } : {}),
    };
  };
  const group = candidates.length > 1;
  const downloadURL = proxyAssetUrl(activeEntry.image.url);
  const detailText = schemaText(schema, 'ui.result.card.detail');

  return (
    <div className="flex w-full flex-col gap-2 rounded-lg border border-border bg-surface p-2" data-testid="chat-image-result" data-count={candidates.length}
      {...(imageGroup ? { 'data-image-group': imageGroup.capability } : {})}>
      <div className="flex select-none items-center gap-1.5 px-0.5 text-[11px] text-muted-foreground">
        <KindBadge kind="image" />
        <span>{imageGroup
          ? schemaCountMessage(schema, 'ui.studio.result.image_group_title', candidates.length, { label: imageGroup.title, count: candidates.length })
          : group ? schemaCountMessage(schema, 'ui.studio.result.group_title', candidates.length, { count: candidates.length }) : schemaText(schema, 'ui.studio.result.single_title')}</span>
      </div>
      <ul className={cn('grid gap-1.5', columnsFor(candidates.length))}>
        {candidates.map((entry, position) => {
          const attachment = attachmentOf(entry);
          const claim = pickClaim(entry.image, attachment);
          const picked = isPickedCandidate(pick, entry.image, attachment);
          const url = proxyAssetUrl(entry.image.url);
          const label = pictureLabel(entry);
          const viewLabel = viewLabelOf(entry);
          return (
            <li key={pictureKey(entry)} className="flex flex-col gap-1">
              <div
                className={cn('group relative overflow-hidden rounded-md border bg-muted', position === active ? 'border-primary ring-1 ring-primary' : 'border-border')}
                {...(() => {
                  // A finished picture can be dragged onto the canvas; the click (putting it on the stage) is untouched.
                  const object = objectAt(entry);
                  const item = object?.assetId ? canvasDragItem({ taskId: object.taskId, assetId: object.assetId, url: url, label }) : null;
                  return item ? { draggable: true, onDragStart: (event: DragEvent<HTMLElement>) => startCanvasDrag(event.dataTransfer, item, event.currentTarget.querySelector('img') ?? event.currentTarget) } : {};
                })()}
              >
                <button
                  type="button"
                  aria-pressed={position === active}
                  aria-label={label}
                  data-testid="chat-result-picture"
                  // A click makes it the card's current picture and puts it on the stage; a picture with no task of its own just opens large.
                  onClick={() => {
                    setChosen(pictureKey(entry));
                    const object = objectAt(entry);
                    if (!object || !openOnStage(object)) onPreview(entry.index);
                  }}
                  className="block aspect-square w-full focus-visible:outline-2 focus-visible:outline-ring"
                >
                  {url
                    // #290: the card shows the downscaled preview; copying and dragging still carry the original.
                    ? <img src={previewAssetUrl(entry.image.url)} alt={label} loading="lazy" decoding="async" className="size-full object-cover" data-chat-picture={url} />
                    : <span role="status" className="flex size-full items-center justify-center p-2 text-center text-[11px] text-muted-foreground">{schemaText(schema, 'error.unavailable')}</span>}
                </button>
                {(() => {
                  const reference = referenceAt(entry);
                  return reference ? <HandleTag reference={reference} wrapperClassName="absolute left-1 top-1" className="select-none bg-black/60 text-white hover:bg-black/80" /> : null;
                })()}
              </div>
              {viewLabel ? <span className="truncate select-none px-0.5 text-center text-[11px] text-muted-foreground" data-testid="chat-result-view-label">{viewLabel}</span> : null}
              {onPick ? (
                <HintTooltip
                  name={picked ? schemaText(schema, 'ui.chat.image.picked') : schemaText(schema, 'ui.chat.image.pick')}
                  reason={!claim ? schemaText(schema, 'ui.chat.image.pick_unavailable') : undefined}
                  hint={!picked && claim ? schemaText(schema, 'ui.chat.image.pick_hint') : undefined}
                  className="w-full"
                >
                  <Action
                    type="button"
                    variant={picked ? 'secondary' : 'outline'}
                    size="xs"
                    data-testid="candidate-pick"
                    data-picked={picked ? 'true' : 'false'}
                    aria-pressed={picked}
                    aria-label={picked ? schemaText(schema, 'ui.chat.image.picked') : claim ? schemaText(schema, 'ui.chat.image.pick') : schemaText(schema, 'ui.chat.image.pick_unavailable')}
                    disabled={!claim || !sessionId || pickBusy || picked}
                    onClick={() => {
                      if (!claim) return;
                      setChosen(pictureKey(entry));
                      void Promise.resolve(onPick(claim)).then((ok) => {
                        // Picking makes this candidate the one on the stage.
                        const object = objectAt(entry);
                        if (ok !== false && object) openOnStage(object);
                      });
                    }}
                    className={cn('w-full', picked && 'text-primary')}
                  >
                    {picked ? <Check /> : null}
                    {picked ? schemaText(schema, 'ui.chat.image.picked') : schemaText(schema, 'ui.chat.image.pick')}
                  </Action>
                </HintTooltip>
              ) : null}
            </li>
          );
        })}
      </ul>
      {imageGroup?.partialMessage ? (
        <p className="px-0.5 text-[11px] text-muted-foreground" data-testid="chat-image-group-partial">{schemaText(schema, imageGroup.partialMessage)}</p>
      ) : null}
      {activeObject ? (
        <div className="flex flex-wrap items-center gap-1" role="group" aria-label={activeHandle ? formatSchemaText(schemaText(schema, 'ui.studio.result.actions_for'), { handle: activeHandle }) : schemaText(schema, 'ui.studio.result.actions')}>
          <Action type="button" variant="outline" size="xs" onClick={() => toThreeD(activeObject)} data-testid="chat-result-to3d">
            <Box />{schemaText(schema, 'ui.studio.result.to_3d')}
          </Action>
          {splatTool ? (
            <HintTooltip
              name={schemaText(schema, 'ui.studio.action.to_splat')}
              reason={splatOfferingsLoading ? schemaText(schema, 'ui.workspace.models.loading')
                : splatOfferingsError ? schemaText(schema, 'ui.workspace.models.load_failed')
                  : splatCanRun ? undefined
                    : schemaText(schema, splatAvailableCount === 0 ? 'ui.studio.action.splat_unavailable' : 'ui.studio.action.splat_ambiguous')}
            >
              <Action type="button" variant="outline" size="xs" disabled={splatOfferingsLoading || Boolean(splatOfferingsError) || !splatCanRun}
                onClick={() => toGaussianSplat(activeObject)} data-testid="chat-result-to-splat">
                <Box />{schemaText(schema, 'ui.studio.action.to_splat')}
              </Action>
            </HintTooltip>
          ) : null}
          {downloadURL ? (
            <a href={downloadURL} download className={buttonVariants({ variant: 'outline', size: 'xs' })} data-testid="chat-result-download">
              <Download />{schemaText(schema, 'ui.studio.result.download')}
            </a>
          ) : null}
          <HintTooltip name={detailText}>
            <Action type="button" variant="outline" size="icon-xs" aria-label={detailText} onClick={() => { const target = detailTarget(activeEntry); if (target) openResultDetail(target); }} data-testid="chat-result-detail">
              <Info />
            </Action>
          </HintTooltip>
          {(() => {
            const reference = referenceAt(activeEntry);
            return reference ? <HandleTag reference={reference} testId="chat-result-active-handle" wrapperClassName="ml-auto" className="select-none text-[11px] text-muted-foreground hover:text-foreground" /> : null;
          })()}
        </div>
      ) : (
        <p className="px-0.5 text-[11px] text-muted-foreground">{schemaText(schema, 'ui.chat.image.send_to_canvas_unavailable')}</p>
      )}
    </div>
  );
}

/**
 * Pictures the user sent: thumbnails with their handle tags, no pick and no object actions. A click opens
 * the result detail; a drag puts the picture on the canvas (a referenced result keeps its task, an upload
 * goes as an upload).
 *
 * #292: a sent upload is an input asset and shows its preview. A message sent before #292 holds the picture
 * itself as a data address; only a base64 PNG/JPEG/WebP/GIF one is shown (as it is: it has no preview), it is
 * not draggable, and it is never copied into a key or an attribute, since it can be tens of megabytes.
 */
export function SentImages({ images, attachments, labelPrefix, onPreview }: { images: ChatImage[]; attachments?: ChatAttachment[]; labelPrefix: string; onPreview: (index: number) => void }) {
  const { schema } = useProductSchema();
  const { proxyAssetUrl, previewAssetUrl } = useAssetURLRewriter();
  const base = schema.chat.context.image_index_base;
  return (
    <ul className="flex flex-wrap justify-end gap-1.5">
      {images.map((image, index) => {
        const url = proxyAssetUrl(image.url);
        const inline = url ? '' : safePreviewURL(image.url);
        const attachment = candidateAttachment({ attachments }, index);
        const label = `${labelPrefix} ${index + base}`;
        const taskId = attachment?.taskId || image.taskId;
        const item = canvasDragItem({ taskId, assetId: attachment?.assetId, url, label }) ?? uploadDragItem({ url, assetId: attachment?.assetId, label });
        return (
          <li
            key={index}
            className="relative"
            {...(item ? { draggable: true, onDragStart: (event: DragEvent<HTMLElement>) => startCanvasDrag(event.dataTransfer, item, event.currentTarget.querySelector('img') ?? event.currentTarget) } : {})}
          >
            <button
              type="button"
              onClick={() => onPreview(index)}
              aria-label={`${labelPrefix} ${index + base}`}
              className="block size-16 overflow-hidden rounded-md border border-border bg-muted focus-visible:outline-2 focus-visible:outline-ring"
            >
              {url
                ? <img src={previewAssetUrl(image.url)} alt={`${labelPrefix} ${index + base}`} loading="lazy" decoding="async" className="size-full object-cover" data-chat-picture={url} />
                : inline
                  ? <img src={inline} alt={`${labelPrefix} ${index + base}`} loading="lazy" decoding="async" className="size-full object-cover" data-chat-picture-inline="" />
                  : <ImageIcon aria-hidden className="m-auto size-4 text-muted-foreground" />}
            </button>
            {(() => {
              // The server names a sent picture by its slot in the message; the tag brings it back into the next one.
              const attachment = attachments?.find((item) => item.kind === 'image' && item.imageSlot === index + 1 && item.handle);
              return attachment?.handle
                ? <HandleTag reference={{ handle: attachment.handle, url: attachment.url ?? image.url, ...(attachment.assetId ? { assetId: attachment.assetId } : {}), ...(attachment.taskId ? { taskId: attachment.taskId } : {}), ...(attachment.captureId ? { captureId: attachment.captureId } : {}) }} testId="chat-sent-handle" wrapperClassName="absolute left-0.5 top-0.5" className="select-none bg-black/60 text-white hover:bg-black/80" />
                : null;
            })()}
          </li>
        );
      })}
    </ul>
  );
}

export interface ModelResultCardProps {
  taskId: string;
  /** The finished model; null while the tracker has none for the task (an older task, or not ready yet): the card then shows only its picture. */
  result: ChatModelResult | null;
  /** The picture that stands for the model. */
  cover: ChatImage | null;
  handle?: string;
  sessionId?: string | null;
}

/**
 * A 3D result in the chat. The model's picture is the cover: a click puts the model on the stage, and the
 * corner button turns it in a viewer inside the card (one card at a time holds a live viewer). Below: capture to 2D,
 * the ZIP of every format, and the task details.
 */
export function ModelResultCard({ taskId, result, cover, handle, sessionId }: ModelResultCardProps) {
  const { schema } = useProductSchema();
  const { proxyAssetUrl, previewAssetUrl } = useAssetURLRewriter();
  const { openOnStage, captureToTwoD } = useObjectActions();
  const captureBlock = useModelCaptureBlock(taskId);
  const live = useInlineModelPreviewStore((state) => state.taskId === taskId);
  // A card that goes away gives its viewer back.
  useEffect(() => () => useInlineModelPreviewStore.getState().close(taskId), [taskId]);

  const object: CurrentObject | null = result
    ? { ...result.selection, ...(handle ? { handle } : {}), sessionId: sessionId ?? result.selection.sessionId }
    : null;
  const modelUrl = result ? safeAssetURL(result.selection.url) : '';
  const coverUrl = cover ? proxyAssetUrl(cover.url) : '';
  const coverPreview = cover ? previewAssetUrl(cover.url) : '';
  const title = schemaText(schema, 'ui.studio.result.model_title');
  const detailText = schemaText(schema, 'ui.result.card.detail');
  const toggleText = schemaText(schema, live ? 'ui.studio.result.preview_3d_close' : 'ui.studio.result.preview_3d');
  const owner = sessionId ?? result?.selection.sessionId;
  // The dialog's title is the card's name, like the version label elsewhere; the prompt travels in its own field.
  const detail: ResultTarget | null = taskId ? {
    taskId, kind: 'model', url: modelUrl, title,
    ...(owner ? { sessionId: owner } : {}),
    ...(result ? {
      status: 'ready' as const, capability: result.capability,
      ...(result.selection.assetId ? { assetId: result.selection.assetId } : {}),
      ...(result.prompt ? { prompt: result.prompt } : {}),
    } : {}),
  } : null;
  const coverFrame = 'block aspect-square w-full overflow-hidden rounded-md border border-border bg-muted';
  const coverFace = coverUrl
    ? <img src={coverPreview} alt={title} loading="lazy" decoding="async" className="size-full object-cover" data-chat-picture={coverUrl} />
    : <span className="flex size-full items-center justify-center text-muted-foreground"><Box aria-hidden className="size-8" /></span>;

  return (
    <div className="flex w-full flex-col gap-2 rounded-lg border border-border bg-surface p-2" data-testid="chat-model-results" data-available={result ? 'true' : 'false'}>
      <div className="flex select-none items-center justify-between gap-2 px-0.5 text-[11px] text-muted-foreground">
        <span className="flex items-center gap-1.5"><KindBadge kind="model" />{title}</span>
        {handle ? <HandleTag reference={{ handle, kind: 'model', url: '', taskId }} className="text-[11px] text-muted-foreground hover:text-foreground" /> : null}
      </div>
      <div className="relative w-full max-w-64">
        {live && result && modelUrl ? (
          <div data-testid="chat-model-preview">
            <ModelViewer3D
              key={`${taskId}:${result.selection.assetId}`}
              src={result.selection.url}
              alt={schemaText(schema, 'ui.workspace.canvas.model_alt')}
              taskId={taskId}
              autoRotate
            />
          </div>
        ) : object ? (
          <button type="button" aria-label={title} onClick={() => openOnStage(object)} className={cn(coverFrame, 'focus-visible:outline-2 focus-visible:outline-ring')} data-testid="chat-model-cover">
            {coverFace}
          </button>
        ) : (
          <div className={coverFrame} data-testid="chat-model-cover">{coverFace}</div>
        )}
        {modelUrl ? (
          // The corner toggle wears the 3D badge (same colours, size and radius); only its behaviour differs.
          <HintTooltip name={toggleText} className="absolute right-1 top-1">
            <button
              type="button"
              data-active={live ? 'true' : 'false'}
              aria-label={toggleText}
              onClick={() => { const previews = useInlineModelPreviewStore.getState(); if (live) previews.close(taskId); else previews.open(taskId); }}
              className={cn(kindBadgeClass('model'), 'cursor-pointer backdrop-blur-md transition-colors hover:bg-kind-3d/25 focus-visible:outline-2 focus-visible:outline-ring data-[active=true]:ring-1 data-[active=true]:ring-kind-3d')}
              data-testid="chat-model-preview-toggle"
            >
              <KindBadgeFace kind="model" />
            </button>
          </HintTooltip>
        ) : null}
      </div>
      {object || detail ? (
        <div className="flex flex-wrap items-center gap-1" role="group" aria-label={schemaText(schema, 'ui.studio.result.actions')}>
          {object && result?.selection.assetId ? (
            <HintTooltip name={schemaText(schema, 'ui.studio.result.capture_2d')} reason={captureBlock ? schemaText(schema, captureBlock) : undefined}>
              <Action type="button" variant="outline" size="xs" disabled={captureBlock !== null} onClick={() => captureToTwoD(object)} data-testid="chat-result-capture">
                <Camera />{schemaText(schema, 'ui.studio.result.capture_2d')}
              </Action>
            </HintTooltip>
          ) : null}
          {modelUrl ? <ModelArchiveLink taskId={taskId} className="h-6 gap-1 px-2 text-xs" /> : null}
          {detail ? (
            <HintTooltip name={detailText}>
              <Action type="button" variant="outline" size="icon-xs" aria-label={detailText} onClick={() => openResultDetail(detail)} data-testid="chat-result-detail">
                <Info />
              </Action>
            </HintTooltip>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

export function GaussianSplatResultCard({ target }: { target: ResultTarget }) {
  const { schema, text } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const file = proxyAssetUrl(target.url, target.taskId);
  return (
    <div className="flex w-full flex-col gap-2 rounded-lg border border-border bg-surface p-2" data-testid="chat-splat-result">
      <div className="flex items-center gap-1.5 px-0.5 text-[11px] text-muted-foreground">
        <KindBadge kind="model" />
        <span>{schemaText(schema, 'ui.studio.result.splat_title')}</span>
      </div>
      <button type="button" className="flex aspect-video w-full items-center justify-center rounded-md border border-border bg-[#1c1d1f] text-sm text-white focus-visible:outline-2 focus-visible:outline-ring" onClick={() => openResultDetail(target)} data-testid="chat-splat-open">
        {schemaText(schema, 'ui.studio.result.splat_preview')}
      </button>
      <div className="flex items-center gap-1" role="group" aria-label={schemaText(schema, 'ui.studio.result.actions')}>
        <Action type="button" variant="outline" size="xs" onClick={() => openResultDetail(target)} data-testid="chat-splat-detail">
          <Info />{schemaText(schema, 'ui.result.card.detail')}
        </Action>
        {file ? <a href={file} download className={buttonVariants({ variant: 'outline', size: 'xs' })} data-testid="chat-splat-download"><Download />{schemaText(schema, 'ui.studio.result.download_splat')}</a> : null}
      </div>
      <p className="px-0.5 text-[11px] text-muted-foreground">{text('ui.result.detail.splat_scope')}</p>
    </div>
  );
}
