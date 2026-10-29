'use client';

import { useEffect, useId, useRef, useState, type DragEvent } from 'react';
import { Ban, Box, BotOff, Frame, GitBranch, Image as ImageIcon, ImagePlus, Paintbrush, RotateCcw, Send, Square, Zap } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { HarnessSummary } from '@/components/workspace/harness-summary';
import { useAuth } from '@/hooks/use-auth';
import { useSessionHarness } from '@/hooks/use-session-harness';
import { AgentScopePicker } from '@/components/studio/agent-scope-picker';
import { ContextMeter } from '@/components/studio/context-meter';
import { ChatModelNotice, ChatModelPicker } from '@/components/studio/chat-model-picker';
import { ReasoningPicker } from '@/components/studio/reasoning-picker';
import { AttachmentThumb } from '@/components/studio/attachment-thumb';
import { ReferenceSizeNotice } from '@/components/canvas/canvas-size-notice';
import { ChipSquare, ContextChip } from '@/components/studio/composer-chips';
import { KindBadge } from '@/components/studio/kind-badge';
import { GenerationModelNotice, ModelPicker } from '@/components/studio/model-picker';
import { ParamsPopover, type ParamsPopoverProps } from '@/components/studio/params-popover';
import { SkillPicker, type SkillPickerProps } from '@/components/studio/skill-picker';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Field } from '@/components/ui/field';
import { ATTACHMENT_PURPOSES, type AttachmentPurpose } from '@/lib/chat/attachment-purpose';
import { attachedIndex, insertReference, namesHandle } from '@/lib/chat/composer-reference';
import { activeMention, filterMentions, insertMention, overridingHandle, type MentionCandidate } from '@/lib/chat/mentions';
import { agentUnavailableReason, formatSchemaText, schemaCountMessage, schemaMessage, schemaText } from '@/lib/product-schema';
import { CHAT_COMPOSER_ELEMENT_ID } from '@/lib/workspace/chat-composer';
import { openResultDetail } from '@/components/result/result-card-parts';
import { composerDragKind, dropFiles, insertPastedText, planComposerPaste } from '@/lib/chat/composer-input';
import { canvasDragItem, decodeCanvasDrag, startCanvasDrag, uploadDragItem, CANVAS_ITEM_MIME } from '@/lib/workspace/canvas-drag';
import { useComposerReferenceStore } from '@/store/composer-reference-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { useChatStore } from '@/store/chat-store';
import { cn } from '@/lib/utils';
import { referenceLimitNote, type ReferenceLimit } from '@/lib/workspace/reference-limit';
import { focusWhenUnblocked } from '@/components/ui/focus-handoff';

/** The object on the stage, as the composer shows it. */
export interface ComposerChip {
  kind: 'image' | 'model';
  thumbnail: string;
  /** Version label such as "2D v3". */
  label: string;
  handle: string;
}

/** The mask being painted on the stage's picture: shown above the box until it is used or dropped. */
export interface ComposerMask {
  /** Strokes painted so far. */
  strokes: number;
  onClear: () => void;
  /** The handle of the picture being painted ("@img5"); empty when it has none yet. */
  handle?: string;
  /** The picture the mask is on, and what has been painted on it (in the picture's own coordinates). */
  picture?: { url: string; width: number; height: number; strokes: readonly (readonly number[])[]; brush: number };
}

/** "Continue from this older version": the next result is branched from it. */
export interface ComposerBranch {
  /** Version label such as "2D v1". */
  label: string;
  onClear: () => void;
}

/** One entry of the `@` list: a handle of the session with the picture that shows it. */
export interface ComposerMention extends MentionCandidate {
  thumbnail: string;
}

export interface ComposerProps {
  value: string;
  onChange: (value: string) => void;
  onSend: () => void;
  onStop: () => void;
  streaming: boolean;
  /** Uploading or preparing a Skill: the box is locked. A running turn does not lock it. */
  locked: boolean;
  canSend: boolean;
  chip: ComposerChip | null;
  chipSkipped: boolean;
  onToggleChip: () => void;
  /** Set while the mask tool is on; null otherwise. */
  mask?: ComposerMask | null;
  /** Set while the next result continues from an older version; null otherwise. */
  branch?: ComposerBranch | null;
  /** What the attached pictures are for; null until the person says. Written into the message. */
  attachmentPurpose?: AttachmentPurpose | null;
  onAttachmentPurpose?: (purpose: AttachmentPurpose | null) => void;
  images: readonly string[];
  onRemoveImage: (index: number) => void;
  /** A canvas image pinned as the next turn's reference. */
  pinnedImage: string | null;
  onUnpin: () => void;
  acceptedImages: string | undefined;
  onFiles: (files: File[]) => void;
  /** Pictures addressed by a pasted rich copy: the parent reads them and locks the send until they are attached. */
  onPasteImages: (urls: string[]) => void;
  editing: boolean;
  onCancelEdit: () => void;
  skill: SkillPickerProps;
  /** The 参数 popover: aspect ratio, size and count of the selected model. */
  params: ParamsPopoverProps;
  /** The next turn's 3D model uses fewer of the attached pictures than are attached (#329 follow-up); null when not. */
  referenceLimit?: ReferenceLimit | null;
  /** Estimated credits of the next turn, shown on the send button; null when unknown. */
  sendCredits: number | null;
  /** The session's handles for the `@` list. */
  mentions: readonly ComposerMention[];
  /** Called when a candidate is picked from the `@` list, so the send can keep its meaning if names are renumbered. */
  onMentionPicked?: (candidate: MentionCandidate) => void;
}

/** Flat [x0, y0, x1, y1, ...] as the points of an SVG polyline. */
function polylinePoints(flat: readonly number[]): string {
  const pairs: string[] = [];
  for (let at = 0; at + 1 < flat.length; at += 2) pairs.push(`${flat[at]},${flat[at + 1]}`);
  return pairs.join(' ');
}

/**
 * 对话输入区：当前对象芯片、附件、文字框、2D / 3D 合一的模型选择、Skill 选择和发送。
 * Enter 发送，但输入法组词时的 Enter 只是确认候选字。输入 `@` 列出本会话的句柄
 * （带缩略图），选中后把句柄写进文字；句柄由服务端在消息附件里给出，用到时由
 * 服务端按本人和本会话核对。
 */
export function Composer({
  value, onChange, onSend, onStop, streaming, locked, canSend, chip, chipSkipped, onToggleChip, mask = null, branch = null,
  attachmentPurpose = null, onAttachmentPurpose,
  images, onRemoveImage, pinnedImage, onUnpin, acceptedImages, onFiles, onPasteImages, editing, onCancelEdit,
  skill, params, sendCredits: generationSendCredits, mentions, onMentionPicked, referenceLimit = null,
}: ComposerProps) {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { user } = useAuth();
  const harness = useSessionHarness(activeSessionId, user?.id ?? '');
  // A generation-model estimate describes free conversation only. Saved function settings
  // have their own execution estimate; loading/error states cannot establish that scope.
  const showGenerationCost = !schema.chat.harness || Boolean(
    user && activeSessionId && !harness.busy && !harness.error
    && harness.view?.selection.session_id === activeSessionId
    && harness.view.status === 'free' && !harness.view.selection.operation_id,
  );
  const sendCredits = showGenerationCost ? generationSendCredits : null;
  const fileInput = useRef<HTMLInputElement>(null);
  // A running turn does not lock the box: what is sent meanwhile waits its turn in the chat.
  const disabled = locked;
  const chipLabel = schemaText(schema, chipSkipped ? 'ui.studio.composer.chip.restore' : 'ui.studio.composer.chip.skip');
  const base = schema.chat.context.image_index_base;
  const hasAttachments = images.length > 0 || pinnedImage !== null;
  const hasContextChips = chip !== null || mask !== null || branch !== null || Boolean(skill.selectedId);
  const attachments = [
    ...images.map((url, index) => ({ url, index, pinned: false, number: index + base })),
    ...(pinnedImage ? [{ url: pinnedImage, index: -1, pinned: true, number: images.length + base }] : []),
  ];
  const overriding = chip && !chipSkipped ? overridingHandle(value, chip.handle) : '';
  const skillName = skill.selectedId ? skill.catalog?.skills.find((item) => item.id === skill.selectedId)?.displayName ?? skill.selectedId : '';
  const purposeLabelId = useId();
  const stopLabel = schemaText(schema, 'ui.workspace.agent.stop');
  const sendLabel = schemaText(schema, 'ui.workspace.agent.send');
  const uploadLabel = schemaText(schema, 'ui.chat.input.upload_title');
  const textarea = useRef<HTMLTextAreaElement>(null);
  const [caret, setCaret] = useState(0);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState<number | null>(null);
  const [activeIndex, setActiveIndex] = useState(0);
  const pendingCaret = useRef<number | null>(null);
  const [flashUrl, setFlashUrl] = useState<string | null>(null);
  const reference = useComposerReferenceStore((state) => state.request);
  const clearReference = useComposerReferenceStore((state) => state.clear);
  const valueRef = useRef(value);
  valueRef.current = value;
  const imagesRef = useRef(images);
  imagesRef.current = images;
  const mention = focused ? activeMention(value, caret) : null;
  const matches = mention ? filterMentions(mentions, mention.query) as ComposerMention[] : [];
  const listOpen = mention !== null && matches.length > 0 && dismissed !== mention.start;
  const highlighted = Math.min(activeIndex, Math.max(0, matches.length - 1));
  const sendTitle = sendCredits === null ? sendLabel : formatSchemaText(schemaText(schema, 'ui.studio.composer.send_with_cost'), { credits: sendCredits });
  // #329 follow-up: a single-picture 3D model uses only the first pictures; the rest stay attached. Same Schema
  // words as the task API, the approval card and the finished message (#329 note 99647).
  const referenceLimitText = referenceLimit ? referenceLimitNote(schema, referenceLimit) : '';
  // No API key or no model on the server: every send would be an Agent turn that cannot answer, so the
  // send is off and the reason stays in view. The draft can still be written (#253).
  const agentUnavailable = agentUnavailableReason(schema);

  // After a pick the text changes under the caret; put the caret back once it has rendered.
  useEffect(() => {
    if (pendingCaret.current === null || !textarea.current) return;
    textarea.current.setSelectionRange(pendingCaret.current, pendingCaret.current);
    setCaret(pendingCaret.current);
    pendingCaret.current = null;
  }, [value]);

  // A click on a picture's handle tag in the chat: attach the picture to the next message (or flash the
  // attachment that already is it) and write its handle at the caret.
  useEffect(() => {
    if (!reference) return;
    const request = reference;
    clearReference(request.nonce);
    if (disabled) return;
    const workspace = useWorkspaceStore.getState();
    // A model's handle only goes into the text; there is no picture to attach.
    const existing = request.kind === 'model' ? -2 : attachedIndex(imagesRef.current, workspace.attachmentSources, request);
    if (existing >= 0) {
      setFlashUrl(imagesRef.current[existing]);
    } else if (existing === -1) {
      workspace.queueChatAttachments([{
        url: request.url,
        ...(request.assetId ? { assetId: request.assetId } : {}),
        ...(request.taskId ? { taskId: request.taskId } : {}),
        ...(request.captureId ? { captureId: request.captureId } : {}),
      }]);
    }
    const field = textarea.current;
    if (!namesHandle(valueRef.current, request.handle)) {
      const at = field ? field.selectionStart ?? valueRef.current.length : valueRef.current.length;
      const next = insertReference(valueRef.current, at, request.handle);
      pendingCaret.current = next.caret;
      onChange(next.value);
    }
    // 引用请求常来自正在关闭的对话框：关闭前外面仍被隐藏，交给对话框关闭时聚焦。
    window.requestAnimationFrame(() => focusWhenUnblocked(() => textarea.current));
  }, [reference, clearReference, disabled, onChange]);
  useEffect(() => {
    if (flashUrl === null) return undefined;
    const timer = window.setTimeout(() => setFlashUrl(null), 1200);
    return () => window.clearTimeout(timer);
  }, [flashUrl]);

  // An attachment chip dragged onto the canvas: a result keeps its task; an upload goes as an uploaded picture.
  const dragAttachment = (event: DragEvent<HTMLElement>, url: string, number: number) => {
    const source = useWorkspaceStore.getState().attachmentSources[url];
    const label = formatSchemaText(schemaText(schema, 'ui.studio.composer.attachment.title'), { index: number });
    const item = canvasDragItem({ taskId: source?.taskId, assetId: source?.assetId, url, label }) ?? uploadDragItem({ url, assetId: source?.assetId, label });
    if (item) startCanvasDrag(event.dataTransfer, item, event.currentTarget.querySelector('img') ?? event.currentTarget);
  };

  // Click on an attachment: the shared result detail shows it (a generated picture reads its task there).
  const previewAttachment = (url: string, number: number) => {
    const source = useWorkspaceStore.getState().attachmentSources[url];
    openResultDetail({
      kind: 'image', url, title: formatSchemaText(schemaText(schema, 'ui.studio.composer.attachment.title'), { index: number }),
      ...(source?.taskId ? { taskId: source.taskId } : {}),
      ...(source?.assetId ? { assetId: source.assetId } : {}),
      ...(source?.captureId ? { captureId: source.captureId } : {}),
    });
  };

  // A result card dragged here becomes an attachment, like a click on its handle tag; files go the upload path.
  const [dragging, setDragging] = useState(false);
  const attachCanvasItem = (raw: string) => {
    const item = decodeCanvasDrag(raw);
    if (!item) return;
    const workspace = useWorkspaceStore.getState();
    // An uploaded picture carries no task (and maybe no asset id): only the ids it has are kept.
    const source = { url: item.url, ...(item.assetId ? { assetId: item.assetId } : {}), ...(item.taskId ? { taskId: item.taskId } : {}) };
    const existing = attachedIndex(imagesRef.current, workspace.attachmentSources, source);
    if (existing >= 0) { setFlashUrl(imagesRef.current[existing]); return; }
    workspace.queueChatAttachments([source]);
  };

  const pickMention = (candidate: ComposerMention) => {
    if (!mention) return;
    const next = insertMention(value, mention, caret, candidate.handle);
    onMentionPicked?.(candidate);
    pendingCaret.current = next.caret;
    setDismissed(null);
    onChange(next.value);
    window.requestAnimationFrame(() => textarea.current?.focus());
  };

  return (
    <div
      className={cn('relative flex flex-col gap-2 border-t border-border bg-panel p-3', dragging && 'ring-2 ring-inset ring-primary')}
      data-testid="studio-composer"
      onDragOver={(event) => {
        if (disabled || !composerDragKind(event.dataTransfer?.types)) return;
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false); }}
      onDrop={(event) => {
        setDragging(false);
        const kind = composerDragKind(event.dataTransfer?.types);
        if (!kind) return;
        // Taken even while locked: the browser's own drop would type the card's url into the box.
        event.preventDefault();
        if (disabled) return;
        if (kind === 'card') attachCanvasItem(event.dataTransfer.getData(CANVAS_ITEM_MIME));
        else onFiles(dropFiles(event.dataTransfer.files));
      }}
    >
      {dragging ? (
        <span className="pointer-events-none absolute inset-x-0 top-1 z-float mx-auto w-fit rounded-md bg-primary px-2 py-0.5 text-xs text-primary-foreground" data-testid="composer-drop-hint">
          {schemaText(schema, 'ui.studio.composer.drop_hint')}
        </span>
      ) : null}
      {agentUnavailable ? (
        <p role="alert" className="flex items-start gap-1.5 rounded-lg bg-warning/15 px-3 py-1.5 text-xs leading-4 text-foreground" data-testid="composer-agent-unavailable">
          <BotOff aria-hidden className="mt-px size-3.5 shrink-0 text-warning" />
          <span>{agentUnavailable}</span>
        </p>
      ) : null}
      {editing ? (
        <div className="flex items-center justify-between gap-2 rounded-lg bg-muted px-3 py-1.5 text-xs text-muted-foreground" data-testid="chat-editing-bar">
          <span>{schemaText(schema, 'ui.chat.message.editing')}</span>
          <Action type="button" variant="ghost" size="xs" onClick={onCancelEdit}>
            {schemaText(schema, 'ui.chat.message.edit_cancel')}
          </Action>
        </div>
      ) : null}
      {hasContextChips ? (
      <div className="flex flex-wrap items-center gap-1.5" data-testid="composer-chips">
        {chip ? (
          chipSkipped ? (
            <ContextChip
              testId="composer-object-chip"
              data={{ skipped: true }}
              muted
              square={<ChipSquare><Ban aria-hidden className="size-4" /></ChipSquare>}
              title={schemaText(schema, 'ui.studio.composer.chip.off_title')}
              subtitle={schemaText(schema, 'ui.studio.composer.chip.off_sub')}
              remove={{ label: chipLabel, onClick: onToggleChip, icon: <RotateCcw aria-hidden className="size-3" />, pressed: true }}
            />
          ) : (
            <ContextChip
              testId="composer-object-chip"
              // A handle typed in the box wins this turn: the chip says so and turns grey.
              data={{ skipped: overriding !== '', 'overridden-by': overriding || undefined }}
              accent
              muted={overriding !== ''}
              square={(
                <ChipSquare>
                  {chip.thumbnail ? <img src={chip.thumbnail} alt={''} className="size-full object-cover" /> : null}
                </ChipSquare>
              )}
              title={(
                <span className="flex min-w-0 items-center gap-1.5">
                  <KindBadge kind={chip.kind} />
                  <span className="truncate">{formatSchemaText(schemaText(schema, 'ui.studio.composer.chip.current'), { handle: chip.handle || chip.label })}</span>
                </span>
              )}
              subtitle={overriding !== '' ? formatSchemaText(schemaText(schema, 'ui.studio.composer.chip.override'), { handle: overriding }) : chip.label}
              remove={{ label: chipLabel, onClick: onToggleChip }}
            />
          )
        ) : null}
        {mask ? (
          <ContextChip
            testId="composer-mask-chip"
            data={{ strokes: mask.strokes }}
            accent
            hint={{ name: schemaText(schema, 'ui.studio.mask.chip_title_hint') }}
            square={(
              <ChipSquare>
                {mask.picture ? (
                  <>
                    <img src={mask.picture.url} alt={''} className="size-full object-cover" />
                    <svg aria-hidden viewBox={`0 0 ${mask.picture.width} ${mask.picture.height}`} preserveAspectRatio="xMidYMid slice" className="absolute inset-0 size-full">
                      <rect width={mask.picture.width} height={mask.picture.height} className="fill-black/50" />
                      {mask.picture.strokes.map((points, index) => (
                        <polyline
                          key={index}
                          points={polylinePoints(points)}
                          fill="none"
                          className="stroke-primary"
                          strokeWidth={Math.max(mask.picture!.brush, 1)}
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      ))}
                    </svg>
                  </>
                ) : <Paintbrush aria-hidden className="size-4 text-primary" />}
              </ChipSquare>
            )}
            title={mask.handle
              ? formatSchemaText(schemaText(schema, 'ui.studio.mask.chip_title'), { handle: mask.handle })
              : schemaText(schema, 'ui.studio.mask.chip')}
            subtitle={mask.strokes > 0
              ? schemaCountMessage(schema, 'ui.studio.mask.chip_strokes', mask.strokes, { count: mask.strokes })
              : schemaText(schema, 'ui.studio.mask.chip_idle')}
            remove={{ label: schemaText(schema, 'ui.studio.mask.chip_clear'), onClick: mask.onClear, testId: 'composer-mask-clear' }}
          />
        ) : null}
        {branch ? (
          <ContextChip
            testId="composer-branch-chip"
            accent
            square={<ChipSquare><GitBranch aria-hidden className="size-4" /></ChipSquare>}
            title={formatSchemaText(schemaText(schema, 'ui.studio.composer.chip.branch'), { label: branch.label })}
            subtitle={schemaText(schema, 'ui.studio.composer.chip.branch_sub')}
            remove={{ label: schemaText(schema, 'ui.studio.composer.chip.branch_clear'), onClick: branch.onClear }}
          />
        ) : null}
        {skillName ? (
          <ContextChip
            testId="composer-skill-chip"
            square={<ChipSquare><Zap aria-hidden className="size-4" /></ChipSquare>}
            title={schemaText(schema, 'ui.studio.composer.chip.skill')}
            subtitle={skillName}
            remove={{ label: schemaText(schema, 'ui.studio.composer.chip.skill_clear'), onClick: () => skill.onSelect(null) }}
          />
        ) : null}
      </div>
      ) : null}
      {hasAttachments ? (
        <div className="flex flex-col gap-1.5" data-testid="composer-attachments">
          <div className="flex flex-wrap items-center gap-1.5" data-testid="composer-attachment-list">
          {attachments.map((item) => (
            <ContextChip
              key={`${item.index}-${item.url.slice(0, 32)}`}
              testId="composer-attachment-chip"
              flash={item.url === flashUrl}
              onDragStart={item.pinned ? undefined : (event) => dragAttachment(event, item.url, item.number)}
              open={{
                label: schemaMessage(schema, 'ui.studio.composer.attachment.preview', { index: item.number }),
                onClick: () => previewAttachment(item.url, item.number),
                testId: 'composer-attachment-open',
              }}
              square={(
                <AttachmentThumb url={item.url} alt={schemaMessage(schema, 'ui.chat.input.attachment_alt', { index: item.number })} />
              )}
              title={item.pinned
                ? schemaText(schema, 'ui.studio.composer.attachment.pinned')
                : formatSchemaText(schemaText(schema, 'ui.studio.composer.attachment.title'), { index: item.number })}
              subtitle={[
                schemaText(schema, item.pinned ? 'ui.studio.composer.attachment.from_board' : 'ui.studio.composer.attachment.upload'),
                attachmentPurpose ? schemaText(schema, `ui.studio.composer.attachment.purpose_${attachmentPurpose}`) : '',
              ].filter(Boolean).join(' · ')}
              remove={{
                label: schemaMessage(schema, 'ui.chat.input.remove_attachment', { index: item.number }),
                onClick: item.pinned ? onUnpin : () => onRemoveImage(item.index),
              }}
            />
          ))}
          </div>
          {/* #296: what an edit of the one attached picture outputs when no size was chosen. */}
          <ReferenceSizeNotice delivery={params.canvas?.delivery} />
          {referenceLimitText ? (
            <div role="status" data-testid="composer-reference-limit" className="rounded-md border border-reminder/40 bg-reminder/10 px-2 py-1 text-[11px] leading-4 text-foreground [overflow-wrap:anywhere]">
              {referenceLimitText}
            </div>
          ) : null}
          {onAttachmentPurpose ? (
        <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground" data-testid="composer-attachment-purpose">
            <span id={purposeLabelId}>{schemaText(schema, 'ui.studio.composer.attachment.purpose')}</span>
            <div role="radiogroup" aria-labelledby={purposeLabelId} className="flex items-center rounded-md border border-border bg-surface p-0.5">
              {ATTACHMENT_PURPOSES.map((purpose) => (
                <button
                  key={purpose}
                  type="button"
                  role="radio"
                  aria-checked={attachmentPurpose === purpose}
                  disabled={disabled}
                  onClick={() => onAttachmentPurpose(attachmentPurpose === purpose ? null : purpose)}
                  data-testid={`composer-attachment-purpose-${purpose}`}
                  className={cn(
                    'h-6 rounded px-2 text-xs transition-colors focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50',
                    attachmentPurpose === purpose ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {schemaText(schema, `ui.studio.composer.attachment.purpose_${purpose}`)}
                </button>
              ))}
            </div>
            <span>{schemaText(schema, 'ui.studio.composer.attachment.purpose_note')}</span>
          </div>
          ) : null}
        </div>
      ) : null}
      <input
        ref={fileInput}
        type="file"
        accept={acceptedImages}
        multiple
        className="hidden"
        onChange={(event) => {
          const files = Array.from(event.currentTarget.files ?? []);
          event.currentTarget.value = '';
          onFiles(files);
        }}
      />
      <HarnessSummary sessionId={activeSessionId} />
      <div className="relative rounded-lg border border-border bg-surface transition-colors focus-within:border-ring/60">
        {listOpen ? (
          <ul
            role="listbox"
            aria-label={schemaText(schema, 'ui.studio.composer.mention.title')}
            data-testid="composer-mention-list"
            className="absolute inset-x-0 bottom-full z-float mb-1.5 max-h-52 overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-xl ring-1 ring-foreground/10"
          >
            {matches.map((candidate, index) => (
              <li
                key={candidate.handle}
                role="option"
                aria-selected={index === highlighted}
                data-testid="composer-mention-option"
                data-handle={candidate.handle}
                onMouseDown={(event) => event.preventDefault()}
                onMouseEnter={() => setActiveIndex(index)}
                onClick={() => pickMention(candidate)}
                className={cn('flex h-9 cursor-pointer items-center gap-2 rounded-md px-1.5 text-xs', index === highlighted && 'bg-primary/15')}
              >
                <span className="relative flex size-7 shrink-0 items-center justify-center overflow-hidden rounded-sm bg-muted">
                  {candidate.thumbnail
                    ? <img src={candidate.thumbnail} alt={candidate.handle} className="size-full object-cover" />
                    : candidate.kind === 'model'
                      ? <Box aria-hidden className="size-3.5 text-muted-foreground" />
                      : candidate.board
                        ? <Frame aria-hidden className="size-3.5 text-muted-foreground" />
                        : <ImageIcon aria-hidden className="size-3.5 text-muted-foreground" />}
                </span>
                <span className="min-w-0 flex-1 truncate font-medium">{candidate.name ?? candidate.handle}</span>
                {candidate.name ? <span className="shrink-0 text-muted-foreground">{candidate.handle}</span> : null}
                <span className="shrink-0 rounded-sm bg-panel-raised px-1 text-[10px] leading-4 text-muted-foreground" data-testid="composer-mention-source">
                  {schemaText(schema, `ui.studio.composer.mention.source_${candidate.source ?? 'history'}`)}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
        <Field id={CHAT_COMPOSER_ELEMENT_ID} label={schemaText(schema, 'ui.workspace.agent.placeholder')} labelHidden>
          {(control) => (
            <textarea
              {...control}
              ref={textarea}
              aria-autocomplete="list"
              className="max-h-32 min-h-11 w-full resize-none bg-transparent px-3 pt-2.5 text-sm outline-none placeholder:text-muted-foreground disabled:opacity-50"
              rows={2}
              // The key hint sits in the placeholder: a text box carries no hover hint of its own.
              placeholder={formatSchemaText(schemaText(schema, 'ui.studio.composer.placeholder_with_keys'), {
                placeholder: schemaText(schema, 'ui.workspace.agent.placeholder'),
                keys: schemaText(schema, 'ui.studio.composer.hint'),
              })}
              value={value}
              disabled={disabled}
              onFocus={() => setFocused(true)}
              onBlur={() => setFocused(false)}
              onChange={(event) => {
                setCaret(event.target.selectionStart ?? event.target.value.length);
                setActiveIndex(0);
                setDismissed(null);
                onChange(event.target.value);
              }}
              onPaste={(event) => {
                // Plain text pastes as usual. With pictures (a screenshot, or chat text and pictures copied
                // together) the words go in at the caret and each picture becomes an attachment through the
                // upload path; a canvas marker is never written as words (#281). The parent reads HTML pictures
                // and keeps the send locked until they are attached; files still go straight to that path. The
                // canvas handles Ctrl+V itself only when this box is not the target (it skips text fields).
                const data = event.clipboardData;
                const paste = planComposerPaste({
                  files: data?.files,
                  text: data?.getData('text/plain') ?? '',
                  html: data?.getData('text/html') ?? '',
                  baseURL: document.baseURI,
                });
                if (!paste.handled) return;
                event.preventDefault();
                if (paste.text) {
                  const field = event.currentTarget;
                  const next = insertPastedText(value, field.selectionStart ?? value.length, field.selectionEnd ?? value.length, paste.text);
                  pendingCaret.current = next.caret;
                  onChange(next.value);
                }
                if (paste.files.length > 0) onFiles(paste.files);
                // The parent starts reading before this event returns and holds the send lock until every
                // picture is attached (or the read failed), so Enter and the button cannot send first.
                if (paste.imageUrls.length > 0) onPasteImages(paste.imageUrls);
              }}
              onKeyUp={(event) => setCaret(event.currentTarget.selectionStart ?? 0)}
              onClick={(event) => setCaret(event.currentTarget.selectionStart ?? 0)}
              onKeyDown={(event) => {
                // The Enter that confirms an IME candidate is not a send: it reports
                // isComposing, or keyCode 229 on browsers that clear the flag first.
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                if (listOpen) {
                  if (event.key === 'ArrowDown') { event.preventDefault(); setActiveIndex((highlighted + 1) % matches.length); return; }
                  if (event.key === 'ArrowUp') { event.preventDefault(); setActiveIndex((highlighted - 1 + matches.length) % matches.length); return; }
                  if (event.key === 'Enter' || event.key === 'Tab') { event.preventDefault(); pickMention(matches[highlighted]); return; }
                  if (event.key === 'Escape' && mention) { event.preventDefault(); setDismissed(mention.start); return; }
                }
                if (event.key !== 'Enter' || event.shiftKey) return;
                event.preventDefault();
                if (agentUnavailable || disabled) return;
                onSend();
              }}
            />
          )}
        </Field>
        <div className="flex items-center gap-1 px-1.5 pb-1.5">
          <HintTooltip name={uploadLabel}>
            <Action
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={uploadLabel}
              disabled={disabled}
              onClick={() => fileInput.current?.click()}
              className="text-muted-foreground"
            >
              <ImagePlus className="size-4" />
            </Action>
          </HintTooltip>
          <span className="flex-1" />
          {streaming ? (
            <HintTooltip name={stopLabel}>
              <Action type="button" variant="secondary" size="sm" aria-label={stopLabel} onClick={onStop} data-testid="composer-stop">
                <Square className="size-3.5" />
                {stopLabel}
              </Action>
            </HintTooltip>
          ) : null}
          <HintTooltip
            name={agentUnavailable ?? sendLabel}
            cost={sendCredits === null ? undefined : { text: formatSchemaText(schemaText(schema, 'ui.studio.composer.send_cost_hint'), { credits: sendCredits }), ai: true }}
          >
            <Action
              type="button"
              variant="send"
              size={sendCredits === null ? 'icon-sm' : 'sm'}
              aria-label={sendTitle}
              disabled={!canSend || locked || agentUnavailable !== null}
              onClick={onSend}
              data-testid="composer-send"
            >
              <Send className="size-4" />
              {sendCredits === null ? null : <span className="tabular-nums" data-testid="composer-send-cost">{formatSchemaText(schemaText(schema, 'ui.studio.composer.send_cost'), { credits: sendCredits })}</span>}
            </Action>
          </HintTooltip>
        </div>
      </div>
      {streaming ? (
        <p role="note" className="text-[11px] leading-4 text-muted-foreground" data-testid="composer-stop-note">{schemaText(schema, 'ui.studio.composer.stop_note')}</p>
      ) : null}
      <ChatModelNotice />
      <GenerationModelNotice />
      {/* Fixed column tracks, so a cell's width depends only on the track, never on the chosen name, and every
          cell is one picker row high (h-7). The DOM order is the reading order in both widths, and pickers pair
          up left to right: two tracks when the column is narrow, four when it is wide (>= 40rem).
            narrow: models | Skill / Agent mode | parameters / chat model | thinking level / context usage (full row)
            wide:   models | Skill | Agent mode | parameters / chat model | thinking level | context usage (2 tracks)
          The chat model picker only shows when the administrator offers two or more models; without it the
          thinking level moves up into its track (narrow: alone in the left track of that row, the context usage
          still takes the full row below), so no row ever has a hole in front of a picker. The context usage is
          not a picker: it spans the row so the figure and the compact action sit on the grid's left and right
          edges. The short names carry the full name in their tooltip. */}
      <div className="@container min-w-0">
        <div className="grid min-w-0 grid-cols-2 items-center gap-1.5 @[40rem]:grid-cols-4" data-testid="composer-pickers">
          <ModelPicker />
          <SkillPicker {...skill} />
          <AgentScopePicker />
          <div className="min-w-0" data-testid="composer-params-slot">
            <ParamsPopover {...params} disabled={disabled} />
          </div>
          <ChatModelPicker />
          <ReasoningPicker />
          <ContextMeter />
        </div>
      </div>
    </div>
  );
}
