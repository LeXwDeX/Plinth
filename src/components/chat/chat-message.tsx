import { memo, useCallback, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useShallow } from 'zustand/react/shallow';
import { cn } from '@/lib/utils';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Bot, Trash2, RefreshCw, Pencil, Ellipsis, CornerDownLeft } from 'lucide-react';
import { type ChatAttachment, type ChatMessage as ChatMessageType, type ChatImage } from '@/store/chat-store';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import { BrailleSpinner } from '@/components/ui/braille-spinner';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useProductSchema } from '@/components/product-schema-provider';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { PaidStepNotes } from '@/components/chat/paid-step-notes';
import { GaussianSplatResultCard, ImageResultCard, ModelResultCard, SentImages } from '@/components/chat/result-card';
import { paidStepFailed, stepCreditsText, stepFailedText } from '@/lib/chat/approval-modes';
import { fetchSourceRetries, regenerateBlockedBy, sourceExpiredSteps, sourceRetryText } from '@/lib/chat/source-retry';
import { messageTaskIds, modelCardsOf, modelTaskIdsOf, splitMessagePictures, type MessagePictures, type ModelCardModel } from '@/lib/chat/message-pictures';
import { chatGaussianSplatResults, chatModelResults } from '@/lib/chat/model-results';
import type { ComposerReference } from '@/lib/chat/composer-reference';
import { domainOfCapability } from '@/lib/workspace/versions';
import { imageGroupOfPictures } from '@/lib/workspace/image-result-group';
import { candidateAttachment } from '@/lib/chat/pick';
import { MarkdownText, MessageText } from '@/components/chat/markdown-text';

interface ChatMessageProps {
  message: ChatMessageType;
  sessionId?: string | null;
  onDelete?: (msgId: string) => void;
  /** 更早的用户消息：把文字和图片放回输入框，不直接发送。 */
  onCopyToInput?: (msg: ChatMessageType) => void;
  /** 只给最后一轮的最后一条助手回复：丢弃这一轮的回复后重新生成。 */
  onRegenerate?: (msg: ChatMessageType) => void;
  /** 重新生成大约要花的积分（沿用上一轮付费步骤的估算）；未知时不写。 */
  regenerateCredits?: number;
  /** 只给最后一条用户消息：把它放回输入框，改完再发。 */
  onEdit?: (msg: ChatMessageType) => void;
  onImageClick?: (messageId: string, localIndex: number) => void;
  /** 会话当前选用的候选图；点击候选图上的“选用”时调用 onPick。 */
  pick?: ChatAttachment | null;
  pickBusy?: boolean;
  onPick?: (claim: ChatAttachmentClaim) => Promise<boolean> | void;
  /** The session's pictures by handle: a handle written in the text becomes a tag that attaches it. */
  handleReferences?: ReadonlyMap<string, ComposerReference>;
}

/**
 * Lightbox 状态（支持多图轮播）
 */
export interface LightboxState {
  images: ChatImage[];
  index: number;
  proxyUrls: string[];
}

function stripDelimitedContent(
  content: string,
  prefix: string,
  suffix: string,
): string {
  let remaining = content;
  let visible = '';
  while (remaining) {
    const start = remaining.indexOf(prefix);
    if (start < 0) {
      visible += remaining;
      break;
    }
    const end = remaining.indexOf(suffix, start + prefix.length);
    if (end < 0) {
      visible += remaining;
      break;
    }
    const visibleEnd = start > 0 && remaining[start - 1] === '\n'
      ? start - 1
      : start;
    visible += remaining.slice(0, visibleEnd);
    remaining = remaining.slice(end + suffix.length);
  }
  return visible.trim();
}

/** A user message beyond the last one; its text and pictures can go back to the input. */
function MessageActions({
  message, onCopyToInput, onEdit, onRegenerate, regenerateCredits, onDelete,
}: {
  message: ChatMessageType;
  onCopyToInput?: (msg: ChatMessageType) => void;
  onEdit?: (msg: ChatMessageType) => void;
  onRegenerate?: (msg: ChatMessageType) => void;
  regenerateCredits?: number;
  onDelete?: (msgId: string) => void;
}) {
  const { schema } = useProductSchema();
  const [confirming, setConfirming] = useState(false);
  const isUser = message.role === 'user';
  const text = (key: string) => schemaText(schema, key);
  if (confirming) {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-1.5 text-[11px] text-warning" data-testid="chat-delete-confirm">
        <span>{text('ui.studio.message.delete_confirm')}</span>
        <Action type="button" variant="destructive" size="xs" onClick={() => { setConfirming(false); onDelete?.(message.id); }}>{text('ui.studio.message.delete_action')}</Action>
        <Action type="button" variant="ghost" size="xs" onClick={() => setConfirming(false)}>{text('ui.studio.message.delete_keep')}</Action>
      </div>
    );
  }
  return (
    <div className={cn('flex flex-wrap items-center gap-0.5 text-muted-foreground', isUser && 'justify-end')} data-testid="chat-message-actions">
      {onEdit && isUser ? (
        <Action type="button" variant="ghost" size="xs" onClick={() => onEdit(message)} data-testid="chat-edit">
          <Pencil />{text('ui.chat.message.edit_resend')}
        </Action>
      ) : null}
      {onCopyToInput && isUser && !onEdit ? (
        <Action type="button" variant="ghost" size="xs" onClick={() => onCopyToInput(message)} data-testid="chat-copy-to-input">
          <CornerDownLeft />{text('ui.studio.message.copy_to_input')}
        </Action>
      ) : null}
      {onRegenerate && !isUser ? (
        <Action type="button" variant="ghost" size="xs" onClick={() => onRegenerate(message)} data-testid="chat-regenerate">
          <RefreshCw />
          {regenerateCredits && regenerateCredits > 0
            ? schemaCountMessage(schema, 'ui.studio.message.regenerate_cost', regenerateCredits, { credits: regenerateCredits })
            : text('ui.chat.message.regenerate')}
        </Action>
      ) : null}
      {onDelete ? (
        <DropdownMenu>
          <HintTooltip name={text('ui.studio.message.more')}>
            <DropdownMenuTrigger
              className="flex h-6 items-center gap-1 rounded-md px-2 text-xs hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
              aria-label={text('ui.studio.message.more')}
              data-testid="chat-more"
            >
              <Ellipsis className="size-3.5" aria-hidden />
            </DropdownMenuTrigger>
          </HintTooltip>
          <DropdownMenuContent align={isUser ? 'end' : 'start'} className="w-36">
            <DropdownMenuItem variant="destructive" onClick={() => setConfirming(true)}>
              <Trash2 />{text('ui.chat.message.delete')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  );
}

/** The 3D results of this message: a card per model, each with its picture as the cover. */
function ChatModelCards({ cards, sessionId }: { cards: ModelCardModel[]; sessionId?: string | null }) {
  return (
    <div className="flex w-full flex-col gap-2">
      {cards.map((card) => (
        <ModelResultCard
          key={card.taskId}
          taskId={card.taskId}
          result={card.result}
          cover={card.cover?.image ?? null}
          handle={card.handle}
          sessionId={sessionId}
        />
      ))}
    </div>
  );
}

/**
 * One message of the conversation. Memoized (#291): while a turn streams, the chat column redraws on every
 * chunk, and only the streaming message (and a message whose own tasks changed) receives new props.
 */
export const ChatMessage = memo(function ChatMessage({
  message, sessionId, onDelete, onCopyToInput, onRegenerate, regenerateCredits, onEdit, onImageClick, pick, pickBusy, onPick, handleReferences,
}: ChatMessageProps) {
  const { schema } = useProductSchema();
  const isUser = message.role === 'user';
  const settled = !message.isStreaming && !message.id.startsWith(schema.chat.frontend.optimistic_message_id_prefix);

  // 过滤掉服务端注入的图片 URL 标注（仅用于 LLM 上下文）
  const displayContent = stripDelimitedContent(
    message.content,
    schema.chat.frontend.legacy_hidden_content_prefix,
    schema.chat.frontend.legacy_hidden_content_suffix,
  );

  /** 处理图片点击：传递消息 ID 和本地索引给父组件 */
  const handleImageClick = useCallback((localIndex: number) => {
    if (onImageClick) {
      onImageClick(message.id, localIndex);
    }
  }, [message.id, onImageClick]);

  const hasNativeStep = (message.paidSteps ?? []).some((step) => step.nativeQuote !== undefined);
  const stepCredits = hasNativeStep ? 0 : (message.paidSteps ?? []).reduce((total, step) => total + step.credits, 0);
  // Only this message's tasks: a progress update of another turn's task must not redraw every message (#291).
  // Every use below looks tasks up by an ID of this message (its task IDs, paid steps and pictures).
  const taskIds = useMemo(() => new Set(messageTaskIds(message)), [message]);
  const trackedTasks = useWorkspaceStore(useShallow((state) => state.trackedTasks.filter((task) => taskIds.has(task.taskId))));
  // A result is what its card is: the 2D pictures of this message are candidates, a 3D task's pictures are the cover of its model card.
  // A 3D task counts as soon as the tracker knows it (a streaming message gets its pictures before the model can be downloaded).
  const modelResults = isUser ? [] : chatModelResults(trackedTasks, messageTaskIds(message), sessionId);
  const splatResults = isUser ? [] : chatGaussianSplatResults(trackedTasks, messageTaskIds(message), sessionId);
  const { candidates, previews }: MessagePictures = isUser
    ? { candidates: [], previews: [] }
    : splitMessagePictures(message, modelTaskIdsOf(message, modelResults, trackedTasks, sessionId, (capability) => domainOfCapability(schema, capability) === 'model'));
  const modelCards = modelCardsOf(message, previews, modelResults);
  // #326: a paid step that failed because its source's original task is gone is judged by the server with the
  // rule plans use; a regenerate certain to fail again is not offered, and one that takes the saved file says so.
  const expiredSteps = isUser ? [] : sourceExpiredSteps((message.paidSteps ?? []).map((step) => step.taskId), trackedTasks);
  const { data: sourceRetries, mutate: judgeSourceRetries } = useSWR(
    expiredSteps.length > 0 ? ['chat-source-retry', ...expiredSteps] : null,
    ([, ...ids]: string[]) => fetchSourceRetries(schema, ids),
    { revalidateOnFocus: false },
  );
  const regenerateBlocked = regenerateBlockedBy(sourceRetries);
  const checkedRegenerate = useCallback(async (msg: ChatMessageType) => {
    // Judged again when clicked: the answer shown may be older than the rules.
    if (expiredSteps.length > 0 && regenerateBlockedBy(await judgeSourceRetries())) return;
    onRegenerate?.(msg);
  }, [expiredSteps.length, judgeSourceRetries, onRegenerate]);
  // A declared image group (四视角) is shown as one named group of labelled views, never as anonymous candidates.
  const imageGroup = isUser ? null : imageGroupOfPictures(
    schema, candidates,
    (entry) => ({ taskId: entry.image.taskId, assetId: candidateAttachment({ attachments: message.attachments }, entry.index)?.assetId, url: entry.image.url }),
    trackedTasks.filter((task) => task.sessionId === sessionId),
  );

  const spinner = message.isStreaming ? (
    <BrailleSpinner
      className="ml-1 text-inherit"
      speed={schema.runtime.frontend.spinner_frame_milliseconds}
      aria-label={schemaText(schema, 'ui.chat.message.generating')}
    />
  ) : null;

  return (
    <div
      className={cn('group/msg flex flex-col gap-1.5 px-3 py-2', isUser ? 'items-end' : 'items-start')}
      data-testid="chat-message"
      data-role={message.role}
    >
      {/* Text: the user's words sit in a quiet block on the right, as typed; the Agent's reply shows its Markdown subset (#333). */}
      {displayContent && (
        <div
          className={cn(
            'max-w-[92%] text-[13px] leading-relaxed [overflow-wrap:anywhere]',
            isUser ? 'whitespace-pre-wrap rounded-lg bg-panel-raised px-3 py-2' : 'w-full text-foreground',
          )}
        >
          {isUser ? (
            <>
              <MessageText text={displayContent} references={handleReferences} />
              {spinner}
            </>
          ) : (
            <MarkdownText text={displayContent} streaming={message.isStreaming === true} references={handleReferences} tail={spinner} />
          )}
        </div>
      )}

      {isUser && message.images && message.images.length > 0 && (
        <SentImages
          images={message.images}
          attachments={message.attachments}
          labelPrefix={schemaText(schema, 'ui.chat.message.uploaded_image')}
          onPreview={handleImageClick}
        />
      )}

      {candidates.length > 0 && (
        <ImageResultCard
          candidates={candidates}
          group={imageGroup}
          attachments={message.attachments}
          sessionId={sessionId}
          pick={pick}
          pickBusy={pickBusy}
          onPick={onPick}
          onPreview={handleImageClick}
          labelPrefix={schemaText(schema, 'ui.chat.message.generated_image')}
        />
      )}

      {modelCards.length > 0 && <ChatModelCards cards={modelCards} sessionId={sessionId} />}
      {splatResults.map((target) => <GaussianSplatResultCard key={target.taskId} target={target} />)}

      {/* 已提交的付费步骤：估算积分与批准来源 */}
      {!isUser && message.paidSteps && message.paidSteps.length > 0 && (
        <PaidStepNotes
          steps={message.paidSteps}
          creditsText={(credits) => stepCreditsText(schema, credits)}
          failedText={(credits) => stepFailedText(schema, credits)}
          failed={(taskId) => paidStepFailed(schema, trackedTasks.find((task) => task.taskId === taskId))}
          note={(taskId) => sourceRetryText(schema, sourceRetries?.[taskId] ?? '')}
          autoApprovedText={schemaText(schema, 'ui.workspace.approval.auto_approved_note')}
          nativeText={(amount) => formatSchemaText(schemaText(schema, 'ui.pricing.frozen_estimate'), { amount })}
          unknownPriceText={schemaText(schema, 'ui.pricing.unknown')}
        />
      )}

      {settled && (onDelete || onCopyToInput || onEdit || (onRegenerate && !regenerateBlocked)) ? (
        <MessageActions
          message={message}
          onCopyToInput={onCopyToInput}
          onEdit={onEdit}
          onRegenerate={onRegenerate && !regenerateBlocked ? (msg) => void checkedRegenerate(msg) : undefined}
          regenerateCredits={regenerateCredits ?? (stepCredits > 0 ? stepCredits : undefined)}
          onDelete={onDelete}
        />
      ) : null}
    </div>
  );
});

/**
 * 生成中的状态提示（含进度条）；模型思考时显示 Schema 的「思考中…」（#285）。
 */
export function ChatStatusBubble({ text, progress, thinking = false }: { text: string; progress?: number | null; thinking?: boolean }) {
  const { schema } = useProductSchema();
  return (
    <div
      className="flex gap-3 px-4 py-2 justify-start"
      role="status"
      aria-live="polite"
      data-testid="chat-status-bubble"
      data-thinking={thinking ? 'true' : undefined}
    >
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/20">
        <Bot className="h-4 w-4 text-primary" />
      </div>
      <div className="flex flex-col gap-1.5 min-w-[180px]">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <BrailleSpinner
            className="text-primary"
            speed={schema.runtime.frontend.spinner_frame_milliseconds}
            aria-label={schemaText(schema, 'ui.chat.message.processing')}
          />
          <span>{text}</span>
          {typeof progress === 'number' && (
            <span className="tabular-nums font-medium text-primary">
              {progress}
              {schema.runtime.frontend.percentage_suffix}
            </span>
          )}
        </div>
        {typeof progress === 'number' && (
          <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
            <div
              className="h-full rounded-full bg-primary transition-all duration-150 ease-out"
              style={{
                width: `${Math.min(
                  progress,
                  schema.chat.frontend.progress_maximum,
                )}${schema.runtime.frontend.percentage_suffix}`,
              }}
            />
          </div>
        )}
      </div>
    </div>
  );
}
