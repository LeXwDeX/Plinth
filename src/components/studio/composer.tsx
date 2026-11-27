'use client';

import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState, type DragEvent, type TextareaHTMLAttributes } from 'react';
import { Box, Frame, GitBranch, Image as ImageIcon, ImagePlus, Maximize2, Paintbrush, Send, Square, Zap } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { HarnessSummary } from '@/components/workspace/harness-summary';
import { useAuth } from '@/hooks/use-auth';
import { useSessionHarness } from '@/hooks/use-session-harness';
import { ContextMeter } from '@/components/studio/context-meter';
import { ChatModelNotice } from '@/components/studio/chat-model-picker';
import { AttachmentThumb } from '@/components/studio/attachment-thumb';
import { ReferenceSizeNotice } from '@/components/canvas/canvas-size-notice';
import { CHIP_CARD_SQUARE_CLASS, ChipSquare, ContextChip } from '@/components/studio/composer-chips';
import { KindBadge } from '@/components/studio/kind-badge';
import { ObjectThumbImage } from '@/components/studio/object-thumb';
import { ComposerSettings } from '@/components/studio/composer-settings';
import { GenerationModelNotice } from '@/components/studio/model-picker';
import type { ParamsPanelProps } from '@/components/studio/params-popover';
import type { SkillPickerProps } from '@/components/studio/skill-picker';
import { StopChoices } from '@/components/studio/stop-choices';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Field } from '@/components/ui/field';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { attachedIndex, insertReference, namesHandle } from '@/lib/chat/composer-reference';
import { activeMention, filterMentions, insertMention, mentionSections, overridingHandle, type MentionCandidate } from '@/lib/chat/mentions';
import { agentUnavailableReason, formatSchemaText, schemaCountMessage, schemaMessage, schemaText } from '@/lib/product-schema';
import { unavailableLabel } from '@/lib/unavailable';
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
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { HandlePreview } from '@/components/chat/handle-preview';

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

/**
 * #644 「以画板为主」 for the message in the box: off, the turn works on the conversation and the board takes no
 * part; on, the board is the turn's input. `forced`: a selection or mask on the board, or `@画布`, turned it on.
 */
export interface ComposerBoardMode {
  on: boolean;
  forced: boolean;
  onToggle: () => void;
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

/** #154: the session style lock as the box shows it, and this turn's exemption. */
export interface ComposerStyleLock {
  /** The session has an enabled lock with text or reference images. */
  active: boolean;
  /** This turn goes out without the lock. */
  skipped: boolean;
  /** Why the lock's references may not all be used before this send; null when they will. */
  notice: { kind: 'unavailable' | 'trimmed'; text: string } | null;
  onToggleSkip: () => void;
  onOpen: () => void;
}

export interface ComposerProps {
  value: string;
  onChange: (value: string) => void;
  /**
   * Sends what the box holds. `onAccepted` runs once the send is accepted (queued, or taken by the server);
   * a refused or failed send never calls it, so the expanded editor stays open with its text (#188).
   */
  onSend: (onAccepted: () => void) => void;
  onStop: () => void;
  /**
   * #137: set while the server says the waiting turn may stop waiting with its Task running on; the
   * stop button then offers "stop and cancel the task" (onStop) and this. Null keeps the single stop.
   */
  onStopInBackground?: (() => void) | null;
  streaming: boolean;
  /** Uploading or preparing a Skill: the box is locked. A running turn does not lock it. */
  locked: boolean;
  canSend: boolean;
  /**
   * The selected object: the turn's default target when the text names no handle (#557). Null when nothing is
   * selected or the person deselected it.
   */
  chip: ComposerChip | null;
  /** The chip's ✕: deselects the object, so the turn has no implicit target until an object is selected again. */
  onDeselectChip: () => void;
  /** Set while the mask tool is on; null otherwise. */
  mask?: ComposerMask | null;
  /** Set while the next result continues from an older version; null otherwise. */
  branch?: ComposerBranch | null;
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
  /** The Skills tab of the settings dialog behind the gear (#405). */
  skill: SkillPickerProps;
  /** The generation parameters tab of the settings dialog: aspect ratio, size and count of the selected models. */
  params: ParamsPanelProps;
  /** The next turn's 3D model uses fewer of the attached pictures than are attached (#329 follow-up); null when not. */
  referenceLimit?: ReferenceLimit | null;
  /** Estimated credits of the next turn, shown on the send button; null when unknown. */
  sendCredits: number | null;
  /** The session's handles for the `@` list. */
  mentions: readonly ComposerMention[];
  /** Called when a candidate is picked from the `@` list, so the send can keep its meaning if names are renumbered. */
  onMentionPicked?: (candidate: MentionCandidate) => void;
  /** #154: the style-lock label and one-turn exemption; null when the session has no lock. */
  styleLock?: ComposerStyleLock | null;
  /** #644: the 以画板为主 toggle; null when the session has no board to send. */
  boardMode?: ComposerBoardMode | null;
}

/** Flat [x0, y0, x1, y1, ...] as the points of an SVG polyline. */
function polylinePoints(flat: readonly number[]): string {
  const pairs: string[] = [];
  for (let at = 0; at + 1 < flat.length; at += 2) pairs.push(`${flat[at]},${flat[at + 1]}`);
  return pairs.join(' ');
}

/**
 * 对话输入区：当前对象芯片、附件、文字框、发送，以及发送键右侧的设置齿轮（#405：生成模型、Skills、生成参数、
 * 对话模型与思考档位都在齿轮打开的设置页里）。Enter 发送，但输入法组词时的 Enter 只是确认候选字。输入 `@` 列出本会话的句柄
 * （带缩略图），选中后把句柄写进文字；句柄由服务端在消息附件里给出，用到时由
 * 服务端按本人和本会话核对。
 */
export function Composer({
  value, onChange, onSend, onStop, onStopInBackground = null, streaming, locked, canSend, chip, onDeselectChip, mask = null, branch = null,
  images, onRemoveImage, pinnedImage, onUnpin, acceptedImages, onFiles, onPasteImages, editing, onCancelEdit,
  skill, params, sendCredits: generationSendCredits, mentions, onMentionPicked, referenceLimit = null,
  styleLock = null, boardMode = null,
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
  const deselectLabel = schemaText(schema, 'ui.studio.composer.chip.deselect');
  const base = schema.chat.context.image_index_base;
  const hasAttachments = images.length > 0 || pinnedImage !== null;
  const hasContextChips = chip !== null || mask !== null || branch !== null || Boolean(skill.selectedId);
  // #405: what the turn is about (stage picture, mask, branch, Skill) and the pictures attached share one wrapping row.
  const hasChipRow = hasContextChips || hasAttachments;
  const attachments = [
    ...images.map((url, index) => ({ url, index, pinned: false, number: index + base })),
    ...(pinnedImage ? [{ url: pinnedImage, index: -1, pinned: true, number: images.length + base }] : []),
  ];
  const overriding = chip ? overridingHandle(value, chip.handle) : '';
  const currentName = chip ? formatSchemaText(schemaText(schema, 'ui.studio.composer.chip.current'), { handle: chip.handle || chip.label }) : '';
  // #670: the Skill is named once its catalog is read; until then the name reads as loading, never as its id.
  const skillName = skill.selectedId
    ? skill.catalog
      ? skill.catalog.skills.find((item) => item.id === skill.selectedId)?.displayName ?? skill.selectedId
      : schemaText(schema, 'ui.workspace.skills.loading')
    : '';
  const stopLabel = schemaText(schema, 'ui.workspace.agent.stop');
  const sendLabel = schemaText(schema, 'ui.workspace.agent.send');
  const uploadLabel = schemaText(schema, 'ui.chat.input.upload_title');
  const textarea = useRef<HTMLTextAreaElement>(null);
  const expandedTextarea = useRef<HTMLTextAreaElement>(null);
  const [expanded, setExpanded] = useState(false);
  // Every way out of the box goes through one submit, so the expanded editor closes only once the parent
  // accepts the send. Never hand `onSend` itself to an event: the MouseEvent would arrive as the callback.
  const submit = () => onSend(() => setExpanded(false));
  const activeTextarea = () => expanded ? expandedTextarea.current : textarea.current;
  const [caret, setCaret] = useState(0);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState<number | null>(null);
  const [activeIndex, setActiveIndex] = useState(0);
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const previewFromPointer = useRef(false);
  const mentionListRef = useRef<HTMLUListElement>(null);
  const mentionListId = useId();
  const [mentionPlacement, setMentionPlacement] = useState<{ side: 'above' | 'below'; offset: number; space: number } | null>(null);
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
  const agentUnavailableLabel = unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.composer.agent_not_configured'));

  // After a pick the text changes under the caret; put the caret back once it has rendered.
  useEffect(() => {
    const field = activeTextarea();
    if (pendingCaret.current === null || !field) return;
    field.setSelectionRange(pendingCaret.current, pendingCaret.current);
    setCaret(pendingCaret.current);
    pendingCaret.current = null;
  }, [value, expanded]);

  useLayoutEffect(() => {
    const field = textarea.current;
    if (!field) return;
    field.style.height = '0px';
    field.style.height = `${Math.min(field.scrollHeight, schema.chat.frontend.textarea_maximum_height_pixels)}px`;
    field.style.overflowY = field.scrollHeight > schema.chat.frontend.textarea_maximum_height_pixels ? 'auto' : 'hidden';
  }, [value, schema.chat.frontend.textarea_maximum_height_pixels]);

  useLayoutEffect(() => {
    if (!expanded || !listOpen) return;
    let popup: HTMLElement | null = null;
    const place = () => {
      const field = expandedTextarea.current;
      const list = mentionListRef.current;
      const dialog = field?.closest<HTMLElement>('[data-slot="dialog-content"]');
      if (!field || !list || !dialog) return;
      if (popup !== dialog) {
        popup?.removeEventListener('animationend', place);
        popup = dialog;
        popup.addEventListener('animationend', place);
      }
      const box = field.getBoundingClientRect();
      const modal = dialog.getBoundingClientRect();
      const gap = 6;
      const margin = 8;
      const above = Math.max(0, modal.top - gap - margin);
      const below = Math.max(0, window.innerHeight - modal.bottom - gap - margin);
      const side = below >= list.scrollHeight || below >= above ? 'below' : 'above';
      // shortcut: textarea caret pixels are unavailable; anchor outside the editor until a caret measurement is available.
      setMentionPlacement({ side, offset: gap + Math.max(0, side === 'above' ? box.top - modal.top : modal.bottom - box.bottom), space: side === 'above' ? above : below });
    };
    place();
    const frame = window.requestAnimationFrame(place);
    window.addEventListener('resize', place);
    return () => {
      window.removeEventListener('resize', place);
      popup?.removeEventListener('animationend', place);
      window.cancelAnimationFrame(frame);
    };
  }, [expanded, listOpen, matches.length]);

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
    const field = activeTextarea();
    if (!namesHandle(valueRef.current, request.handle)) {
      const at = field ? field.selectionStart ?? valueRef.current.length : valueRef.current.length;
      const next = insertReference(valueRef.current, at, request.handle);
      pendingCaret.current = next.caret;
      onChange(next.value);
    }
    // 引用请求常来自正在关闭的对话框：关闭前外面仍被隐藏，交给对话框关闭时聚焦。
    window.requestAnimationFrame(() => focusWhenUnblocked(activeTextarea));
  }, [reference, clearReference, disabled, onChange, expanded]);
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
    setPreviewIndex(null);
    onChange(next.value);
    window.requestAnimationFrame(() => activeTextarea()?.focus());
  };

  const inputEvents: TextareaHTMLAttributes<HTMLTextAreaElement> = {
    value, disabled,
    onFocus: () => setFocused(true),
    onBlur: () => setFocused(false),
    onChange: (event) => {
      setCaret(event.currentTarget.selectionStart ?? event.currentTarget.value.length);
      setActiveIndex(0);
      // Typing refilters an open list and moves the highlight back to the first option, so the list returns to
      // its top too. scrollTop stays inside the list; scrollIntoView could also scroll the page or the dialog.
      if (mentionListRef.current) mentionListRef.current.scrollTop = 0;
      setPreviewIndex(null);
      setDismissed(null);
      onChange(event.currentTarget.value);
    },
    onPaste: (event) => {
      const data = event.clipboardData;
      const paste = planComposerPaste({
        files: data?.files, text: data?.getData('text/plain') ?? '', html: data?.getData('text/html') ?? '', baseURL: document.baseURI,
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
      if (paste.imageUrls.length > 0) onPasteImages(paste.imageUrls);
    },
    onKeyUp: (event) => setCaret(event.currentTarget.selectionStart ?? 0),
    onClick: (event) => setCaret(event.currentTarget.selectionStart ?? 0),
    onKeyDown: (event) => {
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      if (listOpen) {
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault();
          const next = (highlighted + (event.key === 'ArrowDown' ? 1 : -1) + matches.length) % matches.length;
          previewFromPointer.current = false;
          setActiveIndex(next);
          setPreviewIndex(next);
          // Keyboard moves bring the new option into the scrolling list, wrap included. Pointer moves do not,
          // so the list never jumps under the mouse. The guard keeps jsdom (no scrollIntoView) harmless.
          const option = mentionListRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[next];
          // #560: the first option of a section brings its heading along, so the heading never stays scrolled off.
          const section = option?.closest<HTMLElement>('[role="group"]');
          if (section && section.querySelector('[role="option"]') === option) {
            section.querySelector<HTMLElement>('[data-mention-heading]')?.scrollIntoView?.({ block: 'nearest' });
          }
          option?.scrollIntoView?.({ block: 'nearest' });
          return;
        }
        if (event.key === 'Enter' || event.key === 'Tab') { event.preventDefault(); pickMention(matches[highlighted]); return; }
        if (event.key === 'Escape' && mention) { event.preventDefault(); event.stopPropagation(); setDismissed(mention.start); return; }
      }
      if (event.key !== 'Enter' || event.shiftKey) return;
      event.preventDefault();
      if (agentUnavailable || disabled) return;
      submit();
    },
  };

  const mentionOption = (candidate: ComposerMention, index: number) => (
    <li key={candidate.handle} role="option" aria-selected={index === highlighted} data-testid="composer-mention-option" data-handle={candidate.handle}
      onMouseDown={(event) => event.preventDefault()} onMouseEnter={() => { previewFromPointer.current = true; setActiveIndex(index); setPreviewIndex(index); }} onMouseLeave={() => { if (previewFromPointer.current) setPreviewIndex((current) => current === index ? null : current); }} onClick={() => pickMention(candidate)}
      className={cn('flex h-9 cursor-pointer items-center gap-2 rounded-md px-1.5 text-xs', index === highlighted && 'bg-primary/15')}>
      <HandlePreview handle={candidate.handle} url={candidate.kind === 'image' && !candidate.board ? candidate.url || candidate.thumbnail : ''} open={previewIndex === index} avoidRef={mentionListRef}>
        <span className="relative flex size-7 shrink-0 items-center justify-center overflow-hidden rounded-sm bg-muted">
          {candidate.thumbnail ? <img src={candidate.thumbnail} alt={candidate.handle} className="size-full object-cover" />
            : candidate.kind === 'model' ? <Box aria-hidden className="size-3.5 text-muted-foreground" />
              : candidate.board ? <Frame aria-hidden className="size-3.5 text-muted-foreground" /> : <ImageIcon aria-hidden className="size-3.5 text-muted-foreground" />}
        </span>
      </HandlePreview>
      <span className="min-w-0 flex-1 truncate font-medium">{candidate.name ?? candidate.handle}</span>
      {candidate.name ? <span className="shrink-0 text-muted-foreground">{candidate.handle}</span> : null}
      <span className="shrink-0 rounded-sm bg-panel-raised px-1 text-[10px] leading-4 text-muted-foreground" data-testid="composer-mention-source">
        {schemaText(schema, `ui.studio.composer.mention.source_${candidate.source ?? 'history'}`)}
      </span>
    </li>
  );

  const mentionList = listOpen ? (
    <ul ref={mentionListRef} role="listbox" aria-label={schemaText(schema, 'ui.studio.composer.mention.title')} data-testid="composer-mention-list"
      className={cn('absolute inset-x-0 z-float max-h-52 overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-xl ring-1 ring-foreground/10', !expanded && 'bottom-full mb-1.5')}
      style={expanded ? mentionPlacement ? {
        [mentionPlacement.side === 'above' ? 'bottom' : 'top']: `calc(100% + ${mentionPlacement.offset}px)`,
        maxHeight: `min(13rem, ${mentionPlacement.space}px)`,
      } : { visibility: 'hidden' } : undefined}>
      {mentionSections(matches).map((section, sectionIndex) => section.group ? (
        // #560: a heading per section ("画布中", "素材库"). It is not an option: the keyboard moves through the flat
        // matches, and the heading only labels the group for assistive technology.
        <li key={`group:${sectionIndex}`} role="group" aria-labelledby={`${mentionListId}-${sectionIndex}`} data-testid="composer-mention-group" data-group={section.group}>
          <div id={`${mentionListId}-${sectionIndex}`} role="presentation" data-mention-heading data-testid="composer-mention-group-heading"
            className="px-1.5 pb-0.5 pt-1.5 text-[10px] font-medium leading-4 text-muted-foreground">
            {schemaText(schema, `ui.studio.composer.mention.group_${section.group}`)}
          </div>
          <ul role="none">{section.entries.map(({ candidate, index }) => mentionOption(candidate, index))}</ul>
        </li>
      ) : <Fragment key={`plain:${sectionIndex}`}>{section.entries.map(({ candidate, index }) => mentionOption(candidate, index))}</Fragment>)}
    </ul>
  ) : null;

  return (
    <div
      className={cn('@container relative flex flex-col gap-2 border-t border-border bg-panel p-3', dragging && 'ring-2 ring-inset ring-primary')}
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
        <UnavailableNotice
          testId="composer-agent-unavailable"
          className="text-xs"
          label={agentUnavailableLabel}
          detail={agentUnavailable}
        />
      ) : null}
      {editing ? (
        <div className="flex items-center justify-between gap-2 rounded-lg bg-muted px-3 py-1.5 text-xs text-muted-foreground" data-testid="chat-editing-bar">
          <span>{schemaText(schema, 'ui.chat.message.editing')}</span>
          <Action type="button" variant="ghost" size="xs" onClick={onCancelEdit}>
            {schemaText(schema, 'ui.chat.message.edit_cancel')}
          </Action>
        </div>
      ) : null}
      {styleLock?.active ? (
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1" data-testid="composer-style-lock">
          <button
            type="button"
            onClick={styleLock.onOpen}
            data-testid="composer-style-lock-chip"
            className="inline-flex min-w-0 items-center gap-1 rounded-md bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary outline-none focus-visible:ring-1 focus-visible:ring-primary"
          >
            <Paintbrush aria-hidden className="size-3.5 shrink-0" />
            <span className="truncate">{schemaText(schema, 'ui.studio.style_lock.chip')}</span>
          </button>
          <label className={cn('inline-flex min-w-0 items-center gap-1.5 text-xs', styleLock.skipped ? 'text-foreground' : 'text-muted-foreground')}>
            <input
              type="checkbox"
              checked={styleLock.skipped}
              onChange={styleLock.onToggleSkip}
              data-testid="composer-style-lock-skip"
              className="size-3.5 shrink-0"
            />
            <span className="min-w-0">{schemaText(schema, 'ui.studio.style_lock.skip_once')}</span>
          </label>
          {styleLock.notice?.kind === 'unavailable' ? (
            <UnavailableNotice
              testId="composer-style-lock-notice"
              className="text-[11px] leading-4"
              label={unavailableLabel(schema, styleLock.notice.text)}
            />
          ) : styleLock.notice ? (
            <p role="status" data-testid="composer-style-lock-notice" className="min-w-0 text-[11px] leading-4 text-warning">{styleLock.notice.text}</p>
          ) : null}
        </div>
      ) : null}
      {hasChipRow ? (
      // #593: the text chips (mask, branch, Skill) keep the two-column grid of #405; the picture chips (the object and
      // the attachments) are fixed-size cards side by side, wrapping, under them.
      <div className="flex min-w-0 flex-col gap-1.5" data-testid="composer-chips">
        {mask || branch || skillName ? (
        <div className="grid min-w-0 grid-cols-2 gap-1.5" data-testid="composer-chip-grid">
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
                ? `${schemaCountMessage(schema, 'ui.studio.mask.chip_strokes', mask.strokes, { count: mask.strokes })} · ${schemaText(schema, 'ui.studio.mask.chip_selection')}`
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
        {chip || attachments.length > 0 ? (
        <div className="flex min-w-0 flex-wrap gap-1.5" data-testid="composer-chip-cards">
          {chip ? (
            <ContextChip
              testId="composer-object-chip"
              // The selected object is the turn's default target (#557); a handle typed in the box wins over it: the
              // chip says so and turns grey. Its ✕ deselects.
              data={{ skipped: overriding !== '', 'overridden-by': overriding || undefined, handle: chip.handle || undefined }}
              accent
              muted={overriding !== ''}
              card
              square={(
                <ChipSquare className={CHIP_CARD_SQUARE_CLASS}>
                  {/* #624: a 3D version with no preview shows the 3D placeholder, not an empty (black) square. */}
                  <ObjectThumbImage kind={chip.kind} src={chip.thumbnail} />
                </ChipSquare>
              )}
              // #546: the picture and its kind badge only; the name ("当前：画布", "当前：@img5") is the hover hint and the
              // screen-reader text, not a line of its own. An override still says which handle wins this turn.
              label={currentName}
              hint={{ name: currentName }}
              oneLine={overriding === ''}
              title={(
                <span className="flex min-w-0 items-center gap-1">
                  <KindBadge kind={chip.kind} />
                  <span className="sr-only" data-testid="composer-object-name">{currentName}</span>
                </span>
              )}
              subtitle={overriding !== '' ? formatSchemaText(schemaText(schema, 'ui.studio.composer.chip.override'), { handle: overriding }) : undefined}
              remove={{ label: deselectLabel, onClick: onDeselectChip }}
            />
          ) : null}
          {attachments.map((item) => {
            const name = item.pinned
              ? schemaText(schema, 'ui.studio.composer.attachment.pinned')
              : formatSchemaText(schemaText(schema, 'ui.studio.composer.attachment.title'), { index: item.number });
            return (
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
                card
                square={(
                  <AttachmentThumb url={item.url} alt={schemaMessage(schema, 'ui.chat.input.attachment_alt', { index: item.number })} squareClassName={CHIP_CARD_SQUARE_CLASS} />
                )}
                // #593: the card shows the picture and its kind badge; the name is the hint and the screen-reader text.
                label={name}
                title={(
                  <span className="flex min-w-0 items-center gap-1">
                    <KindBadge kind="image" />
                    <span className="sr-only">{name}</span>
                  </span>
                )}
                subtitle={schemaText(schema, item.pinned ? 'ui.studio.composer.attachment.from_board' : 'ui.studio.composer.attachment.upload')}
                remove={{
                  label: schemaMessage(schema, 'ui.chat.input.remove_attachment', { index: item.number }),
                  onClick: item.pinned ? onUnpin : () => onRemoveImage(item.index),
                }}
              />
            );
          })}
        </div>
        ) : null}
      </div>
      ) : null}
      {hasAttachments ? (
        // #697: attached pictures carry no per-picture purpose; only the notices about them remain below the chips.
        <div className="flex flex-col gap-1.5 empty:hidden" data-testid="composer-attachments">
          {/* #296: what an edit of the one attached picture outputs when no size was chosen. */}
          <ReferenceSizeNotice delivery={params.canvas?.delivery} />
          {referenceLimitText ? (
            <UnavailableNotice
              testId="composer-reference-limit"
              className="text-[11px] leading-4"
              label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.composer.reference_unused'))}
              detail={referenceLimitText}
            />
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
        {!expanded ? mentionList : null}
        <Field id={CHAT_COMPOSER_ELEMENT_ID} label={schemaText(schema, 'ui.workspace.agent.placeholder')} labelHidden>
          {(control) => (
            <textarea
              {...control}
              ref={textarea}
              aria-autocomplete="list"
              className="min-h-11 w-full resize-none bg-transparent px-3 pt-2.5 text-sm outline-none placeholder:text-muted-foreground disabled:opacity-50"
              rows={2}
              // The key hint sits in the placeholder: a text box carries no hover hint of its own.
              placeholder={formatSchemaText(schemaText(schema, 'ui.studio.composer.placeholder_with_keys'), {
                placeholder: schemaText(schema, 'ui.workspace.agent.placeholder'),
                keys: schemaText(schema, 'ui.studio.composer.hint'),
              })}
              {...inputEvents}
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
          {boardMode ? (
            <HintTooltip
              name={schemaText(schema, 'ui.studio.composer.board_mode.label')}
              hint={schemaText(schema, boardMode.forced
                ? 'ui.studio.composer.board_mode.hint_forced'
                : boardMode.on ? 'ui.studio.composer.board_mode.hint_on' : 'ui.studio.composer.board_mode.hint_off')}
            >
              <Action
                type="button"
                variant="ghost"
                size="sm"
                aria-pressed={boardMode.on}
                disabled={disabled || boardMode.forced}
                onClick={boardMode.onToggle}
                data-testid="composer-board-mode"
                className={cn('gap-1 px-1.5 text-xs', boardMode.on ? 'bg-primary/15 text-primary' : 'text-muted-foreground')}
              >
                <Frame aria-hidden className="size-3.5" />
                {schemaText(schema, 'ui.studio.composer.board_mode.label')}
              </Action>
            </HintTooltip>
          ) : null}
          <span className="flex-1" />
          <HintTooltip name={schemaText(schema, 'ui.studio.composer.expand_editor')}>
            <Action type="button" variant="ghost" size="icon-sm" aria-label={schemaText(schema, 'ui.studio.composer.expand_editor')} disabled={disabled} onClick={() => setExpanded(true)}>
              <Maximize2 aria-hidden className="size-4" />
            </Action>
          </HintTooltip>
          {streaming && onStopInBackground ? (
            <StopChoices label={stopLabel} onCancel={onStop} onBackground={onStopInBackground} />
          ) : streaming ? (
            <HintTooltip name={stopLabel}>
              <Action type="button" variant="secondary" size="sm" aria-label={stopLabel} onClick={onStop} data-testid="composer-stop">
                <Square className="size-3.5" />
                {stopLabel}
              </Action>
            </HintTooltip>
          ) : null}
          <HintTooltip
            name={sendLabel}
            reason={agentUnavailable ? schemaText(schema, 'ui.unavailable.reason.composer.agent_not_configured') : undefined}
            hint={agentUnavailable ?? undefined}
            cost={sendCredits === null ? undefined : { text: formatSchemaText(schemaText(schema, 'ui.studio.composer.send_cost_hint'), { credits: sendCredits }), ai: true }}
          >
            <Action
              type="button"
              variant="send"
              size={sendCredits === null ? 'icon-sm' : 'sm'}
              aria-label={sendTitle}
              disabled={!canSend || locked || agentUnavailable !== null}
              onClick={submit}
              data-testid="composer-send"
            >
              <Send className="size-4" />
              {sendCredits === null ? null : <span className="tabular-nums" data-testid="composer-send-cost">{formatSchemaText(schemaText(schema, 'ui.studio.composer.send_cost'), { credits: sendCredits })}</span>}
            </Action>
          </HintTooltip>
          {/* #405: the one entry to the generation models, Skills, parameters and chat model; the settings dialog is its own layer. */}
          <ComposerSettings skill={skill} params={params} locked={disabled} />
        </div>
      </div>
      <Dialog open={expanded} onOpenChange={setExpanded}>
        <DialogContent closeLabel={schemaText(schema, 'ui.studio.composer.close_editor')} finalFocus={textarea} initialFocus={expandedTextarea} className="sm:max-w-3xl">
          <DialogTitle>{schemaText(schema, 'ui.studio.composer.expand_editor')}</DialogTitle>
          <div className="relative">
            {expanded ? mentionList : null}
            <textarea ref={expandedTextarea} aria-label={schemaText(schema, 'ui.workspace.agent.placeholder')} aria-autocomplete="list"
              className="h-[min(60vh,32rem)] min-h-44 w-full resize-none rounded-md border border-border bg-surface p-3 text-sm outline-none focus:border-ring"
              {...inputEvents} />
          </div>
          <div className="flex justify-end">
            <Action type="button" variant="send" size="sm" aria-label={sendTitle} disabled={!canSend || locked || agentUnavailable !== null} onClick={submit}>
              <Send aria-hidden className="size-4" />
              {sendCredits === null ? sendLabel : formatSchemaText(schemaText(schema, 'ui.studio.composer.send_cost'), { credits: sendCredits })}
            </Action>
          </div>
        </DialogContent>
      </Dialog>
      <ChatModelNotice />
      <GenerationModelNotice />
      {/* The status row under the box: the context usage and the compact action on the left, the tool call counter on the
          right edge, all one control high (#275, #322). The four pickers that shared this area moved into the settings
          dialog behind the gear (#405). */}
      <ContextMeter />
    </div>
  );
}
