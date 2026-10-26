'use client';

import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import { ArrowDown, Bot, ChevronsLeft, ChevronsRight, ImagePlus } from 'lucide-react';
import { toast } from 'sonner';

import {
  ChatMessage,
  ChatStatusBubble,
} from '@/components/chat/chat-message';
import { markdownSourceOf } from '@/components/chat/markdown-text';
import { useProductSchema } from '@/components/product-schema-provider';
import { ApprovalCard } from '@/components/studio/approval-card';
import { ChoiceCard } from '@/components/studio/choice-card';
import { QueuedTurns } from '@/components/studio/queued-turns';
import { TaskStatusCards } from '@/components/studio/task-status-cards';
import { Composer, type ComposerBranch, type ComposerChip, type ComposerMask, type ComposerMention } from '@/components/studio/composer';
import { SessionNotesPanel, sessionNotesText } from '@/components/workspace/session-notes-panel';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAgentEffectiveConfiguration } from '@/hooks/use-agent-effective-configuration';
import { useAgentSkillCatalog } from '@/hooks/use-agent-skill-catalog';
import { useGenerationParams } from '@/hooks/use-generation-params';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { stageDomainOf } from '@/hooks/use-stage-domain';
import { useSessionAgentConsent } from '@/hooks/use-session-agent-consent';
import { useSessionApproval } from '@/hooks/use-session-approval';
import { useSessionNotes } from '@/hooks/use-session-notes';
import { useSessionPendingApprovals } from '@/hooks/use-session-pending-approvals';
import { useSessionChoices } from '@/hooks/use-session-choices';
import { useSessionPick } from '@/hooks/use-session-pick';
import { useSessionState } from '@/hooks/use-session-state';
import { useThreeViews } from '@/hooks/use-three-views';
import { interleaveGroups } from '@/lib/workspace/three-view';
import { ThreeViewCard } from '@/components/studio/three-view-card';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { useRestoreSessionModels } from '@/hooks/use-restore-session-models';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { sessionSkillId } from '@/lib/agent-configuration-resource';
import { createAgentConfigurationClient, resolveSessionSkill } from '@/lib/agent-configuration';
import { AgentHarnessError, createAgentHarnessClient } from '@/lib/agent-params';
import { isAutoTitlePending, pollAutoTitle } from '@/lib/chat/session-pages';
import { nextStreamStatus } from '@/lib/chat/stream-status';
import { useCanvasArtboard } from '@/hooks/use-canvas-artboard';
import { useReferenceSize } from '@/hooks/use-reference-size';
import { canvasSnapshotClaim, currentObjectSource } from '@/lib/canvas/current-object';
import { exportArtboardToBlob } from '@/lib/canvas/export-canvas';
import { flushPendingEdits } from '@/lib/canvas/pending-edits';
import { exportAndUploadSnapshot, reportSnapshotFailure, snapshotFilename } from '@/lib/canvas/send-snapshot';
import { uploadWorkspaceInputAsset, uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import { inlineImageFilename, isInlineImage, reportInlineImageFailure, uploadInlineImages } from '@/lib/chat/inline-images';
import { buildChatSelection, type ChatAttachmentClaim, type ChatMentionClaim } from '@/lib/chat/attachments';
import { formatSizeLimit } from '@/lib/size-text';
import { cardFromConsent, cardFromPending, type ApprovalCardModel } from '@/lib/chat/approval-card';
import { withPurposeNote, type AttachmentPurpose } from '@/lib/chat/attachment-purpose';
import { handleReferences } from '@/lib/chat/composer-reference';
import { boardMentionCandidate, buildMentionCandidates, mentionsBoard, mentionsForTurn, overridingHandle, mentionCandidates, withBoardMention, type MentionCandidate } from '@/lib/chat/mentions';
import { useComposerDraft } from '@/hooks/use-composer-draft';
import { clearDraft } from '@/lib/chat/composer-draft';
import { useChatScrollFollow } from '@/hooks/use-chat-scroll-follow';
import { useElementHeight } from '@/hooks/use-element-height';
import { useConnectivityStore } from '@/store/connectivity-store';
import { createChatBrowserClient, paidStepFromTaskRegistered, type ChatSemanticEvent, type ChatTurnOptions } from '@/lib/chat/browser-client';
import { choiceCardPlacement, heldChoiceRelease, type ChoiceCardModel, type ChoiceSelection } from '@/lib/chat/choice-cards';
import { readPastedImages } from '@/lib/chat/composer-input';
import { withSessionSendLock } from '@/lib/chat/send-preparation';
import { InFlightGuard } from '@/lib/chat/in-flight-guard';
import { PendingApprovalError, type PendingApprovalInput } from '@/lib/chat/pending-approvals';
import { cancelQueuedTurn, enqueueTurn, queuedFor, takeQueuedTurn, type QueueBySession } from '@/lib/chat/send-queue';
import { ChatHistorySkeleton } from '@/components/chat/chat-history-skeleton';
import { recoverChatSession } from '@/lib/chat/session-recovery';
import { resolveInitialSession, sessionFromAddress, writeSessionToAddress } from '@/lib/chat/session-url';
import { editAttachments, editTarget, regenerateTarget, restoreAttachments, turnPayload } from '@/lib/chat/turn-actions';
import { selectedTranscriptText, transcriptClipboard } from '@/lib/chat/transcript-copy';
import { compactionPlacement } from '@/lib/chat/context-compaction';
import { ContextCompactedDivider } from '@/components/studio/context-compacted-divider';
import { useSessionCompactions } from '@/hooks/use-session-context';
import { imageUploadAccept, ImageUploadError, prepareImageUpload } from '@/lib/image-upload';
import { agentUnavailableReason, formatSchemaText, schemaCountMessage, schemaMessage, schemaText } from '@/lib/product-schema';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import { skillSizeDefault } from '@/lib/workspace/generation-params';
import { hydrateSessionTasks } from '@/lib/workspace/task-hydration';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import { handleOfObject } from '@/lib/workspace/object-handle';
import { captureSessionScope, isCurrentSessionScope, sameSessionScope, selectionForSession, type SessionScope } from '@/lib/workspace/session-scope';
import { versionLabel } from '@/lib/workspace/version-label';
import type { ResultTarget } from '@/lib/workspace/result-target';
import { cn } from '@/lib/utils';
import { focusWhenUnblocked } from '@/components/ui/focus-handoff';
import {
  useChatStore,
  type ChatImage,
  type ChatMessage as ChatMessageType,
} from '@/store/chat-store';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { maskStrokeCount, useMaskStore } from '@/store/mask-store';
import { CHAT_RAIL_WIDTH, CHAT_WIDTH_MAX, CHAT_WIDTH_MIN, useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { withErrorReference } from '@/lib/problem-reference';
import { referenceLimit } from '@/lib/workspace/reference-limit';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { sameData } from '@/lib/core/keep-unchanged';

const RESIZE_KEY_STEP = 16;
const SCROLL_SETTLE_MILLISECONDS = 400;
const APPROVAL_CLOCK_MILLISECONDS = 30_000;
// A late approval whose task id is not in the answer yet: look the task up again after these delays.
const LATE_TASK_HYDRATE_MILLISECONDS = [1_500, 4_000, 8_000];
// The server writes a late task's result message when its sweep (every 2 s) sees the task end.
const LATE_RESULT_RELOAD_MILLISECONDS = [2_500, 5_000];

/** Text selection on or off for the whole page; Safari reads only the -webkit- property. */
function setPageSelectable(selectable: boolean) {
  const value = selectable ? '' : 'none';
  document.body.style.userSelect = value;
  document.body.style.setProperty('-webkit-user-select', value);
}

function ResizeHandle({ width, label, onResize }: { width: number; label: string; onResize: (width: number) => void }) {
  const drag = useRef<{ startX: number; startWidth: number } | null>(null);
  const endDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    drag.current = null;
    setPageSelectable(true);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  };
  // Leaving the page mid-drag must not leave text selection switched off.
  useEffect(() => () => { setPageSelectable(true); }, []);
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuemin={CHAT_WIDTH_MIN}
      aria-valuemax={CHAT_WIDTH_MAX}
      aria-valuenow={width}
      tabIndex={0}
      data-testid="chat-resize-handle"
      onPointerDown={(event) => {
        // A drag must not start a text selection: the browser would highlight the stage and its title.
        event.preventDefault();
        event.currentTarget.setPointerCapture(event.pointerId);
        drag.current = { startX: event.clientX, startWidth: width };
        setPageSelectable(false);
      }}
      onPointerMove={(event) => {
        if (drag.current) onResize(drag.current.startWidth + event.clientX - drag.current.startX);
      }}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onLostPointerCapture={endDrag}
      onKeyDown={(event) => {
        if (event.key === 'ArrowLeft') { event.preventDefault(); onResize(width - RESIZE_KEY_STEP); }
        if (event.key === 'ArrowRight') { event.preventDefault(); onResize(width + RESIZE_KEY_STEP); }
      }}
      className="absolute inset-y-0 -right-1 z-sticky w-2 cursor-col-resize select-none outline-none after:absolute after:inset-y-0 after:left-1/2 after:w-px after:bg-transparent hover:after:bg-primary/60 focus-visible:after:bg-primary"
    />
  );
}

/** 对话列：会话设定、消息与批准卡片、输入区。折叠成窄条时事件流照常接收。 */
const NO_NODES: CanvasNode[] = [];

/** The attachments being read or prepared for one visit. A count, so one paste finishing cannot unlock another. */
interface PendingAttachmentReads {
  scope: SessionScope;
  count: number;
}

function beginAttachmentRead(reads: { current: PendingAttachmentReads }, scope: SessionScope): void {
  if (!sameSessionScope(reads.current.scope, scope)) reads.current = { scope, count: 0 };
  reads.current.count += 1;
}

/** False when another read of this visit is still in flight, or when the visit is no longer current. */
function endAttachmentRead(reads: { current: PendingAttachmentReads }, scope: SessionScope): boolean {
  if (!sameSessionScope(reads.current.scope, scope)) return false;
  reads.current.count = Math.max(0, reads.current.count - 1);
  return reads.current.count === 0;
}

function attachmentsPending(reads: { current: PendingAttachmentReads }, scope: SessionScope): boolean {
  return sameSessionScope(reads.current.scope, scope) && reads.current.count > 0;
}

export function ChatColumn() {
  const { schema } = useProductSchema();
  const chatClient = useMemo(() => createChatBrowserClient(schema), [schema]);
  const harness = useMemo(
    () =>
      createAgentHarnessClient({
        routes: {
          params: schema.runtime.http.routes.agent_params,
          bind: schema.runtime.http.routes.agent_bind,
          consent: schema.runtime.http.routes.agent_consent,
        },
      }),
    [schema],
  );
  const configurationClient = useMemo(() => createAgentConfigurationClient(schema), [schema]);
  const chatWidth = useStudioStore((state) => state.chatWidth);
  const collapsed = useStudioStore((state) => state.chatCollapsed);
  const setChatWidth = useStudioStore((state) => state.setChatWidth);
  const setCollapsed = useStudioStore((state) => state.setChatCollapsed);
  const composerPrefill = useStudioStore((state) => state.composerPrefill);
  const clearComposerPrefill = useStudioStore((state) => state.clearComposerPrefill);
  const turnRequest = useStudioStore((state) => state.turnRequest);
  const continueRequest = useStudioStore((state) => state.continueRequest);
  const clearContinueRequest = useStudioStore((state) => state.clearContinueRequest);
  const clearTurnRequest = useStudioStore((state) => state.clearTurnRequest);
  const prefillComposer = useStudioStore((state) => state.prefillComposer);

  const currentObject = useWorkspaceStore((state) => state.currentObject);
  const stageDomain = stageDomainOf(currentObject);
  const selectedGenerationModel = useWorkspaceStore((state) => state.modelByDomain[stageDomain]);
  const [skillBySession, setSkillBySession] = useState<Record<string, string | null>>({});
  const [skillRefreshVersion, setSkillRefreshVersion] = useState(0);
  const preflightRef = useRef<{ sessionId: string; controller: AbortController } | null>(null);
  const [preparingSession, setPreparingSession] = useState<string | null>(null);
  const skillBySessionRef = useRef(skillBySession);
  skillBySessionRef.current = skillBySession;
  const { modelChoices, isLoading: modelsLoading, error: modelsError } =
    useWorkspaceOfferings(schema, stageDomain);
  useRestoreSessionModels();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const applyTaskLive = useWorkspaceStore((state) => state.applyTaskLive);
  const clearTrackedTasks = useWorkspaceStore((state) => state.clearTrackedTasks);

  const {
    activeSessionId,
    sessionsLoaded,
    messagesBySession,
    olderCursorBySession,
    streamingBuffers,
    addSession,
    setSessions,
    setActiveSession,
    setMessages,
    applyNewestMessagePage,
    applyOlderMessagePage,
    updateSessionTitle,
    initStreaming,
    setAssistantDbId,
    appendStreamText,
    addStreamImages,
    addStreamPaidStep,
    updateStreamGenState,
    finishStreaming,
  } = useChatStore();

  const approval = useSessionApproval(
    chatClient,
    activeSessionId,
    schema.chat.approval_policy.default_mode === 'auto' ? 'auto' : 'manual',
  );
  const refreshApproval = approval.refresh;
  const sessionNotes = useSessionNotes(chatClient, activeSessionId);
  const refreshNotes = sessionNotes.refresh;
  const notesText = useMemo(() => sessionNotesText(schema), [schema]);
  const sessionPick = useSessionPick(chatClient, activeSessionId);
  const chooseCandidate = sessionPick.choose;
  const pending = useSessionPendingApprovals(activeSessionId);
  const sessionChoices = useSessionChoices(activeSessionId);
  const refreshChoices = sessionChoices.refresh;
  const [answeringChoice, setAnsweringChoice] = useState<string | null>(null);
  const versions = useSessionVersions();

  const effective = useAgentEffectiveConfiguration(schema, activeSessionId, stageDomain, selectedGenerationModel, skillRefreshVersion);
  const skillCatalog = useAgentSkillCatalog(schema, effective.data?.revision, skillRefreshVersion);
  const selectedSkillId = sessionSkillId(skillBySession, activeSessionId);
  const skillSelection = resolveSessionSkill(selectedSkillId, skillCatalog.catalog?.skills ?? null, effective.data);
  const currentSkill = skillCatalog.catalog?.skills.find((item) => item.id === selectedSkillId) ?? null;

  const preparing = preparingSession === activeSessionId && preparingSession !== null && preflightRef.current?.sessionId === activeSessionId;
  useEffect(() => () => {
    if (preflightRef.current?.sessionId === activeSessionId) { preflightRef.current.controller.abort(); preflightRef.current = null; }
  }, [activeSessionId]);

  const [input, setInput] = useSessionState('');
  // 正在编辑最后一条用户消息：发送时才替换那一轮，放弃编辑不丢任何东西。
  const [editing, setEditing] = useSessionState<{ deleteIds: string[] } | null>(null);
  const [attachedImages, setAttachedImages] = useSessionState<string[]>([]);
  // 芯片上的 ✕：这一个对象本轮不随消息发出。换了对象就重新带上。
  const [skippedObject, setSkippedObject] = useSessionState<string | null>(null);
  // What the attached pictures are for; written into the message as one line.
  const [attachmentPurpose, setAttachmentPurpose] = useSessionState<AttachmentPurpose | null>(null);
  // 「从这里继续」之后、发出之前：下一个版本接在这个版本后面（芯片可取消）。
  const [branchFrom, setBranchFrom] = useSessionState<{ key: string; label: string } | null>(null);
  const chipHandleRef = useRef('');
  const mentionsRef = useRef<MentionCandidate[]>([]);
  // What the `@` list entries were when picked: numbered names shift when a layer or attachment is removed.
  // Scoped like the draft: a pick belongs to the session (and visit) it was made in and never reaches another.
  const pickedMentionsRef = useRef<{ scope: SessionScope; picks: MentionCandidate[] }>({ scope: captureSessionScope(), picks: [] });
  const pickedMentions = useCallback((scope: SessionScope): MentionCandidate[] =>
    (sameSessionScope(pickedMentionsRef.current.scope, scope) ? pickedMentionsRef.current.picks : []), []);
  const canvasReference = useWorkspaceStore((state) => state.canvasReference);
  const setCanvasReference = useWorkspaceStore((state) => state.setCanvasReference);
  const pendingChatAttachments = useWorkspaceStore((state) => state.pendingChatAttachments);
  const consumeChatAttachments = useWorkspaceStore((state) => state.consumeChatAttachments);
  const sessionSelection = selectionForSession(currentObject, activeSessionId);
  const hasCanvasReference = canvasReference !== null && canvasReference !== '';
  const draftSources = useWorkspaceStore((state) => state.attachmentSources);
  useComposerDraft({
    sessionId: activeSessionId, text: input, setText: setInput, images: attachedImages, sources: draftSources,
    purpose: attachmentPurpose, setPurpose: setAttachmentPurpose,
    droppedNote: useCallback((count: number) => schemaCountMessage(schema, 'ui.studio.composer.draft.local_dropped', count, { count }), [schema]),
  });
  // Back from offline: the event stream of the open session is joined again.
  const reconnectTick = useConnectivityStore((state) => state.reconnectTick);
  const [uploading, setUploading] = useSessionState(false);
  // Synchronous send guard for pictures that are still being read or prepared; state alone is one render late.
  const attachmentReads = useRef<PendingAttachmentReads>({ scope: captureSessionScope(), count: 0 });
  // A draft replaced by send, edit, clear or a prefill in the same visit: a read started against the old draft is spent.
  const draftEpoch = useRef(0);
  // Messages sent while a turn runs wait here ("排队中") and go out when it ends. Kept per
  // session, so switching away and back does not lose them.
  const [queue, setQueue] = useState<QueueBySession>({});
  const queuedTurns = queuedFor(queue, activeSessionId);
  const [drainTick, setDrainTick] = useState(0);
  const drainingRef = useRef(false);
  const startedTurnsRef = useRef(0);
  // The turn whose start the server has not answered yet: the box still holds its words.
  const startingRef = useRef<{ sessionId: string; text: string } | null>(null);
  const [decisionError, setDecisionError] = useState<Record<string, string>>({});
  // The next turn's settings and estimate. A turn that carries a picture (the
  // object on the stage, an upload or a pinned canvas image) runs the Offering
  // that takes references, so that is the one priced.
  const objectSkipped = sessionSelection != null && skippedObject === objectKey(sessionSelection);
  const hasReference = attachedImages.length > 0 || hasCanvasReference || (sessionSelection?.kind === 'image' && !objectSkipped);
  useEffect(() => {
    if (!activeSessionId) return;
    const context = { sessionId: activeSessionId, hasReference: Boolean(hasReference), objectSkipped };
    useStudioStore.getState().setGenerationReferenceContext(context);
    return () => {
      if (useStudioStore.getState().generationReferenceContext === context) {
        useStudioStore.getState().setGenerationReferenceContext(null);
      }
    };
  }, [activeSessionId, hasReference, objectSkipped]);
  const imageGeneration = useGenerationParams('image', activeSessionId, hasReference);
  const modelGeneration = useGenerationParams('model-3d', activeSessionId, hasReference);
  const generation = stageDomain === 'image' ? imageGeneration : modelGeneration;
  // #329 follow-up: when the 3D model the Agent would use takes fewer pictures than are attached (Meshy 7.1 and
  // T2.5 image to 3D take one), say so before sending, whenever the session's Agent scope allows 3D (the Agent may
  // make a 3D model from any stage). The note says it is about 3D; the pictures stay attached, nothing is blocked
  // and the model is not switched.
  const composerReferenceLimit = modelGeneration.allowed
    ? referenceLimit(modelGeneration.nextTurn.offering, attachedImages.length + (hasCanvasReference ? 1 : 0))
    : null;
  const { consent, awaiting: consentAwaiting, busy: consentBusy, decide: decideConsent } =
    useSessionAgentConsent(activeSessionId, harness, schema.chat.frontend.fallback_poll_milliseconds);
  const { proxyAssetUrl, previewAssetUrl } = useAssetURLRewriter();
  const canvasDelivery = useCanvasArtboard(activeSessionId, Boolean(hasReference));
  const { artboard, pixels: deliveryPixels } = canvasDelivery;
  // #296: with one picture in the composer (and the artboard not the source), the notices say the size an edit of it sends.
  const composerDelivery = useReferenceSize(
    canvasDelivery,
    hasCanvasReference && canvasReference ? [...attachedImages, canvasReference] : attachedImages,
    stageDomain === 'image',
  );
  const showResultDetail = useStudioStore((state) => state.openResultDetail);
  const { groups: threeViews } = useThreeViews();
  const deleteMessage = useChatStore((state) => state.deleteMessage);
  const [historyLoading, setHistoryLoading] = useState(false);
  const controllers = useRef(new Map<string, AbortController>());
  // The cards being decided. The ref is the guard (read synchronously, so a double click cannot
  // submit twice); the state only draws the busy buttons, per card.
  const deciding = useRef(new InFlightGuard());
  const sendPreparation = useRef(new InFlightGuard());
  const [decisionBusy, setDecisionBusy] = useState<ReadonlySet<string>>(() => new Set());
  const [now, setNow] = useState(() => Date.now());
  // The clock the cards' "valid for N hours" reads. It is reset whenever the set of
  // cards changes: a clock left at mount time would make a fresh 24-hour card read
  // "25 hours", and it only ticks while there is a card to count down.
  const pendingCardKey = pending.items.map((item) => item.requestId).join('\n');
  useEffect(() => {
    if (pendingCardKey === '') return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), APPROVAL_CLOCK_MILLISECONDS);
    return () => window.clearInterval(timer);
  }, [pendingCardKey]);
  const maximumImages = schema.runtime.frontend.default_reference_image_max;
  const maximumImageBytes = schema.runtime.input_assets.upload_max_bytes;
  const acceptedImages = imageUploadAccept(
    schema.chat.frontend.image_file_accept,
    schema.runtime.frontend.image_upload,
  );

  const attachFiles = useCallback(async (files: File[]) => {
    if (files.length === 0) return;
    const scope = captureSessionScope();
    if (!scope.sessionId || scope.sessionId !== activeSessionId) return;
    const epoch = draftEpoch.current;
    beginAttachmentRead(attachmentReads, scope);
    setUploading(true);
    try {
      const available = Math.max(0, maximumImages - attachedImages.length - (hasCanvasReference ? 1 : 0));
      if (files.length > available) {
        toast.error(schemaCountMessage(schema, 'ui.chat.input.upload_limit', maximumImages, {
          count: maximumImages,
        }));
      }
      const prepared: string[] = [];
      for (const file of files.slice(0, available)) {
        if (!isCurrentSessionScope(scope) || draftEpoch.current !== epoch) return;
        try {
          const picture = await prepareImageUpload(file, {
            accept: schema.chat.frontend.image_file_accept,
            maximumBytes: maximumImageBytes,
            policy: schema.runtime.frontend.image_upload,
          });
          // #292: the picture is uploaded as an input asset when sent; only the raster types that the
          // transcript can show and the asset upload takes are attached (others used to fail later, unseen).
          if (!safePreviewURL(picture)) throw new ImageUploadError('unsupported');
          prepared.push(picture);
        } catch (error) {
          if (!isCurrentSessionScope(scope) || draftEpoch.current !== epoch) return;
          const code = error instanceof ImageUploadError ? error.code : 'read_failed';
          toast.error(schemaMessage(schema, `ui.form.image_upload.${code}`, {
            filename: file.name,
            size: formatSizeLimit(maximumImageBytes),
            pixels: schema.runtime.frontend.image_upload.maximum_pixels,
          }));
        }
      }
      if (!isCurrentSessionScope(scope) || draftEpoch.current !== epoch) return;
      if (prepared.length > 0) {
        // The same picture twice (pasted again, or a file and its copy) is attached once.
        setAttachedImages((current) => [...new Set([...current, ...prepared])].slice(0, maximumImages));
      }
    } finally {
      if (endAttachmentRead(attachmentReads, scope)) setUploading(false);
    }
  }, [activeSessionId, attachedImages.length, hasCanvasReference, maximumImages, maximumImageBytes, schema, setAttachedImages, setUploading]);

  // Pictures addressed by a pasted rich copy (#281). The read keeps the send locked from this call until
  // every picture is attached; a failure releases it, keeps the words and reports the count in this visit.
  const attachPastedImages = useCallback(async (urls: string[]) => {
    if (urls.length === 0) return;
    const scope = captureSessionScope();
    if (!scope.sessionId || scope.sessionId !== activeSessionId) return;
    const epoch = draftEpoch.current;
    beginAttachmentRead(attachmentReads, scope);
    setUploading(true);
    try {
      const { files, failed } = await readPastedImages(urls);
      if (!isCurrentSessionScope(scope) || draftEpoch.current !== epoch) return;
      if (files.length > 0) await attachFiles(files);
      // Attaching yields too: the draft may have been replaced while the files were prepared.
      if (!isCurrentSessionScope(scope) || draftEpoch.current !== epoch) return;
      if (failed > 0) {
        toast.error(schemaCountMessage(schema, 'ui.chat.input.paste.image_unreadable', failed, { count: failed }));
      }
    } finally {
      if (endAttachmentRead(attachmentReads, scope)) setUploading(false);
    }
  }, [activeSessionId, attachFiles, schema, setUploading]);

  const ui = useMemo(
    () => ({
      sessionFailed: schemaText(schema, 'ui.workspace.agent.session_failed'),
      sendFailed: schemaText(schema, 'ui.workspace.agent.send_failed'),
      loadFailed: schemaText(schema, 'ui.workspace.agent.load_failed'),
      consentFailed: schemaText(schema, 'ui.workspace.agent.consent_failed'),
      notesSaveFailed: schemaText(schema, 'ui.workspace.notes.save_failed'),
      pickFailed: schemaText(schema, 'ui.chat.image.pick_failed'),
    }),
    [schema],
  );

  // 消费"发送到对话"排队的附件：合并进附件列表并清空队列。
  useEffect(() => {
    if (pendingChatAttachments.length === 0) return;
    const queued = consumeChatAttachments();
    if (queued.length === 0) return;
    setAttachedImages((current) =>
      [...current, ...queued].slice(0, maximumImages),
    );
  }, [pendingChatAttachments, consumeChatAttachments, maximumImages, setAttachedImages]);

  // A named session is the task. After a refresh, reopen the one the address names; else the latest.
  useEffect(() => {
    if (activeSessionId || sessionsLoaded) return;
    let cancelled = false;
    const controller = new AbortController();
    resolveInitialSession(chatClient, sessionFromAddress(), controller.signal)
      .then(async ({ session: initial, sessions: saved }) => {
        if (cancelled) return;
        if (initial) {
          setSessions(saved);
          setActiveSession(initial.id);
          return;
        }
        const session = await chatClient.createSession();
        if (cancelled) return;
        setSessions([]);
        addSession(session);
      })
      .catch(() => {
        if (!cancelled) toast.error(ui.sessionFailed);
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [activeSessionId, sessionsLoaded, chatClient, addSession, setSessions, setActiveSession, ui.sessionFailed]);

  // Keep the address on the open session so a refresh returns to it. Before the first
  // session is chosen the address is left alone: it may name the session to reopen.
  useEffect(() => {
    if (activeSessionId) writeSessionToAddress(activeSessionId);
    else if (sessionsLoaded) writeSessionToAddress(null);
  }, [activeSessionId, sessionsLoaded]);

  const messagePageSize = schema.runtime.frontend.chat_message_page_size;
  const olderCursor = olderCursorBySession[activeSessionId ?? ''] ?? '';
  const messages = useMemo(
    () => messagesBySession[activeSessionId ?? ''] ?? [],
    [messagesBySession, activeSessionId],
  );
  const streamBuf = streamingBuffers[activeSessionId ?? ''];
  const isStreaming = Boolean(streamBuf);
  const statusText = streamBuf?.genState?.statusText ?? '';
  const progress = streamBuf?.genState?.progress ?? null;

  // The sent words shown until the server has stored them: one object for the whole turn, so the
  // memoized message is not redrawn with every streamed chunk (#291).
  const streamUserMessage = streamBuf?.userMessage;
  const optimisticUser = useMemo((): ChatMessageType | null => (streamUserMessage ? {
    id: schema.chat.frontend.optimistic_user_message_id,
    role: 'user',
    content: streamUserMessage.content,
    images: streamUserMessage.images,
    createdAt: Date.now(),
  } : null), [schema, streamUserMessage]);
  const displayMessages = useMemo((): ChatMessageType[] => {
    const dbMessages = messages.filter(
      (item) => !(streamBuf && item.isPending),
    );
    if (!streamBuf) return dbMessages;
    const result = [...dbMessages];
    if (optimisticUser) {
      const lastUser = [...dbMessages]
        .reverse()
        .find((item) => item.role === 'user');
      if (!lastUser || lastUser.id === streamBuf.previousUserMessageId) {
        result.push(optimisticUser);
      }
    }
    if (
      streamBuf.textContent ||
      streamBuf.images.length > 0 ||
      streamBuf.isStreaming
    ) {
      result.push({
        id:
          streamBuf.assistantDbId ||
          schema.chat.frontend.optimistic_assistant_message_id,
        role: 'assistant',
        content: streamBuf.textContent,
        images: streamBuf.images.length > 0 ? streamBuf.images : undefined,
        paidSteps: streamBuf.paidSteps.length > 0 ? streamBuf.paidSteps : undefined,
        isStreaming: streamBuf.isStreaming,
        createdAt: Date.now(),
      });
    }
    return result;
  }, [messages, optimisticUser, schema, streamBuf]);

  const processEvent = useCallback(
    (sessionId: string, event: ChatSemanticEvent) => {
      // #285: thinking, tool status and progress share one status slot.
      const buffer = useChatStore.getState().streamingBuffers[sessionId];
      if (buffer) {
        const current = buffer.genState ?? { statusText: '', progress: null };
        const next = nextStreamStatus(schema, current, event);
        if (next !== current) {
          updateStreamGenState(sessionId, next.statusText, next.progress, next.thinking);
        }
      }
      switch (event.type) {
        case 'text':
          appendStreamText(sessionId, event.content);
          break;
        case 'images': {
          const images: ChatImage[] = event.urls.map((url) => ({
            url,
            taskId: event.taskID,
          }));
          addStreamImages(sessionId, images);
          break;
        }
        case 'messageCreated':
          setAssistantDbId(sessionId, event.messageID);
          break;
        case 'title':
          updateSessionTitle(sessionId, event.content);
          break;
        case 'error':
          toast.error(event.content);
          break;
        case 'taskRegistered':
          // P0 契约：agent 会话按任务隔离，任务注册即绑定；
          // P2：注册进 workspace 任务跟踪器，由 use-task-tracker 轮询生命周期。
          registerTask({
            taskId: event.taskID,
            sessionId,
            capability: event.capability,
            prompt: event.prompt ?? '',
            progressMaximum:
              schema.runtime.frontend.percentage_multiplier,
          });
          harness.bindTask(sessionId, event.taskID).catch(() => {});
          { const step = paidStepFromTaskRegistered(event);
            if (step) addStreamPaidStep(sessionId, step);
          }
          // 新提交的付费步骤会增加本会话的累计估算积分，向服务端取最新值。
          void refreshApproval();
          break;
        case 'taskUpdate': {
          const trackedId = event.finalTaskID || event.taskID;
          applyTaskLive(trackedId, {
            ...(event.status !== undefined ? { state: event.status } : {}),
            ...(event.progress !== undefined
              ? { progress: event.progress }
              : {}),
            ...(event.error !== undefined ? { error: event.error } : {}),
          });
          break;
        }
        case 'done': {
          // The server summarises the title in the background after the first turn: read it back.
          const session = useChatStore.getState().sessions.find((item) => item.id === sessionId);
          if (session && isAutoTitlePending(session.title, schema.chat.defaults.title)) {
            void pollAutoTitle({
              currentTitle: session.title,
              attempts: schema.runtime.frontend.session_title_refresh_attempts,
              intervalMilliseconds: schema.runtime.frontend.session_title_refresh_interval_milliseconds,
              fetchTitle: async () => (await chatClient.getSession(sessionId)).title,
              isCurrent: () => useChatStore.getState().sessions.some((item) => item.id === sessionId),
              apply: (title) => updateSessionTitle(sessionId, title),
            });
          }
          break;
        }
      }
    },
    [
      chatClient,
      appendStreamText,
      updateStreamGenState,
      addStreamImages,
      addStreamPaidStep,
      setAssistantDbId,
      updateSessionTitle,
      registerTask,
      applyTaskLive,
      harness,
      refreshApproval,
      schema,
    ],
  );

  const connectEventSource = useCallback(
    (
      sessionId: string,
      opts?: { fromId?: number; onDone?: () => void; onError?: () => void },
    ) =>
      chatClient.subscribe(sessionId, {
        fromID: opts?.fromId,
        onEvent: (event) => processEvent(sessionId, event),
        onDone: opts?.onDone,
        onError: opts?.onError,
      }),
    [chatClient, processEvent],
  );

  // 会话恢复：历史消息 + 断线 SSE 续订（复用共享恢复器）。
  useEffect(() => {
    if (!activeSessionId) return;
    void reconnectTick;
    setHistoryLoading(!(activeSessionId in useChatStore.getState().messagesBySession));
    return recoverChatSession({
      sessionId: activeSessionId,
      client: chatClient,
      pageSize: messagePageSize,
      connect: connectEventSource,
      pollMilliseconds: schema.chat.frontend.fallback_poll_milliseconds,
      onLoading: setHistoryLoading,
      onLoadError: () => toast.error(ui.loadFailed),
    });
  }, [
    activeSessionId,
    reconnectTick,
    chatClient,
    connectEventSource,
    messagePageSize,
    schema.chat.frontend.fallback_poll_milliseconds,
    ui.loadFailed,
  ]);

  // An expired card stays in the chat (with 重新发起) but no longer counts as pending.
  const pendingCount = pending.pendingCount + (consentAwaiting && consent.pending ? 1 : 0);
  const lastDisplayed = displayMessages[displayMessages.length - 1];
  const [floatingRef, floatingHeight] = useElementHeight<HTMLDivElement>();
  const { containerRef: messagesRef, endRef: messagesEndRef, showLatest, jumpToLatest, onScroll: onMessagesScroll } = useChatScrollFollow({
    // The newest message, not the count: earlier pages put in front must not move the reader.
    changeKey: [lastDisplayed?.id ?? '', threeViews.length, statusText, pendingCount, collapsed, queuedTurns.length].join('|'),
    ownMessage: lastDisplayed?.role === 'user',
    resetKey: activeSessionId,
    settleMilliseconds: SCROLL_SETTLE_MILLISECONDS,
  });

  // Earlier messages are read only when asked for; the view stays on the message the reader was looking at.
  const [earlierState, setEarlierState] = useState<'idle' | 'loading' | 'failed'>('idle');
  const keepDistanceFromEnd = useRef<number | null>(null);
  useEffect(() => { setEarlierState('idle'); }, [activeSessionId]);
  const loadEarlier = useCallback(async () => {
    const sessionId = activeSessionId;
    if (!sessionId || !olderCursor || earlierState === 'loading') return;
    setEarlierState('loading');
    try {
      const page = await chatClient.listMessagePage(sessionId, { before: olderCursor, limit: messagePageSize });
      if (useChatStore.getState().activeSessionId !== sessionId) return;
      const container = messagesRef.current;
      keepDistanceFromEnd.current = container ? container.scrollHeight - container.scrollTop : null;
      applyOlderMessagePage(sessionId, page);
      setEarlierState('idle');
    } catch {
      if (useChatStore.getState().activeSessionId === sessionId) setEarlierState('failed');
    }
  }, [activeSessionId, applyOlderMessagePage, chatClient, earlierState, messagePageSize, messagesRef, olderCursor]);
  const firstDisplayedId = displayMessages[0]?.id;
  useLayoutEffect(() => {
    const container = messagesRef.current;
    if (keepDistanceFromEnd.current === null || !container) return;
    container.scrollTop = container.scrollHeight - keepDistanceFromEnd.current;
    keepDistanceFromEnd.current = null;
  }, [firstDisplayedId, messagesRef]);

  const send = useCallback(
    async (
      content: string,
      reuseImages?: string[],
      reuseClaims?: ChatAttachmentClaim[],
      replaceIds?: readonly string[],
      queuedSelection?: ChatAttachmentClaim[],
      queuedMentions?: ChatMentionClaim[],
      queuedInputSource?: string,
      extra?: { choiceAnswer?: ChatTurnOptions['choiceAnswer']; onSent?: () => void },
    ) => {
      const scope = captureSessionScope();
      // #321: a choice-card answer is a plain turn of its own: no Skill, no draft, never queued.
      const choiceAnswer = extra?.choiceAnswer;
      if (scope.sessionId !== activeSessionId) return;
      // Without Agent settings no turn can be answered; the composer shows why (#253).
      if (agentUnavailableReason(schema) !== null) return;
      // A Schema system Skill that binds an Agent tool (three-view) stands in for an empty message.
      const toolSkill = choiceAnswer ? null : skillCatalog.catalog?.skills.find(
        (item) => item.id === sessionSkillId(skillBySession, activeSessionId) && item.tool,
      ) ?? null;
      const written = content.trim() || (reuseImages === undefined ? toolSkill?.defaultMessage ?? '' : '');
      let images = reuseImages ?? [
        ...attachedImages,
        ...(hasCanvasReference && canvasReference ? [canvasReference] : []),
      ];
      if ((!written && images.length === 0) || !activeSessionId || uploading || attachmentsPending(attachmentReads, scope)) return;
      // A turn is running: the message waits its turn below. Replacing the last round cannot wait.
      const turnRunning = Boolean(useChatStore.getState().streamingBuffers[activeSessionId]);
      if (turnRunning && ((replaceIds && replaceIds.length > 0) || choiceAnswer)) return;

      const workspace = useWorkspaceStore.getState();
      // The turn's creative domain follows what is on the stage. The model picked
      // for the other domain travels along so a cross-domain tool uses it.
      const sessionObject = selectionForSession(workspace.currentObject, activeSessionId);
      const domain = stageDomainOf(sessionObject);
      const selectedModel = workspace.modelByDomain[domain];
      if (selectedModel) {
        if (modelsLoading || modelsError) {
          toast.error(schemaText(schema, 'ui.workspace.harness.load_failed'));
          return;
        }
        if (!modelChoices.some((choice) => choice.model_id === selectedModel)) {
          workspace.setModel(domain, null);
          toast.error(schemaText(schema, 'ui.workspace.harness.choose_model'));
          return;
        }
      }
      const otherDomainModels = Object.fromEntries(
        Object.entries(workspace.modelByDomain).filter(
          (entry): entry is [string, string] => entry[0] !== domain && Boolean(entry[1]),
        ),
      );
      const sessionId = activeSessionId;
      // Pictures attached now (not a replayed turn) say what they are for.
      const purposeNote = !reuseImages && attachmentPurpose && images.length > 0
        ? schemaText(schema, `ui.studio.composer.attachment.note_${attachmentPurpose}`)
        : '';
      const notedText = withPurposeNote(written, purposeNote);
      // What the person sees and selects travels with the turn as claims; the
      // server verifies every ID for this owner and session before use.
      let claims: ChatAttachmentClaim[] = reuseClaims ?? images.flatMap((url, index): ChatAttachmentClaim[] => {
        const source = workspace.attachmentSources[url];
        return source ? [{ kind: 'image', imageSlot: index + 1, ...source }] : [];
      });
      const canvasState = useCanvasStore.getState();
      // A handle written in the text takes the turn from the object on the stage (the chip says so).
      const skipped = sessionObject != null
        && (skippedObjectRef.current === objectKey(sessionObject) || overridingHandle(written, chipHandleRef.current) !== '');
      // The Agent's current object is what the dashed artboard shows: whenever the artboard holds anything,
      // it is rasterised at the chosen output pixels like every other delivery (export, send to chat,
      // inpaint, merge) and uploaded as an input asset, even a lone untouched picture.
      let canvasSelection: ChatAttachmentClaim[] | null = null;
      // Writing @画布 asks for the artboard itself: the same rasteriser, whatever the current object is.
      const namesBoard = reuseImages === undefined && mentionsBoard(written, mentionsRef.current);
      if (!queuedSelection && (namesBoard || (!skipped && sessionObject?.kind !== 'model')) && artboard) {
        const source = namesBoard ? 'canvas' : currentObjectSource(canvasState.getNodes(sessionId), artboard);
        if (source === 'canvas') {
          try {
            // #286: save brush edits, rasterise, upload: each failure keeps its step for the toast and the report.
            let boardNodes = useCanvasStore.getState().getNodes(sessionId);
            const { url } = await exportAndUploadSnapshot({
              flush: () => flushPendingEdits(sessionId),
              exportPicture: () => {
                boardNodes = useCanvasStore.getState().getNodes(sessionId);
                return exportArtboardToBlob(boardNodes, schema.canvas, artboard, deliveryPixels);
              },
              upload: (picture) => uploadWorkspaceInputAsset(schema, picture, snapshotFilename(schema)),
              maximumBytes: schema.runtime.input_assets.upload_max_bytes,
            });
            // #272: the claim says how many visible layers the snapshot flattens; the server measures its size.
            canvasSelection = [canvasSnapshotClaim(url, boardNodes, artboard)];
          } catch (error) {
            // The draft stays in the box: nothing below clears it on this path.
            const text = reportSnapshotFailure(schema, 'chat-send', error);
            if (isCurrentSessionScope(scope)) toast.error(text);
            return;
          }
          if (!isCurrentSessionScope(scope)) return;
        }
      }
      // #266: a turn that sends the artboard says so; the server binds that snapshot as the turn's source
      // whatever the Agent names. A queued turn keeps the source it was queued with.
      const inputSource = queuedSelection !== undefined
        ? queuedInputSource
        : canvasSelection ? schema.chat.context.canvas_input_source : undefined;
      const stageSelection = queuedSelection ?? canvasSelection ?? (skipped ? [] : buildChatSelection({
        sessionId,
        currentObject: sessionObject,
        nodes: canvasState.getNodes(sessionId),
        selectedNodeIds: canvasState.selectedNodeIds,
      }));
      // `@` references to pictures that have no handle yet travel as claims; the server verifies them, gives
      // the pictures handles and rewrites the tokens, so the text goes out as written (queued turns too).
      const text = notedText;
      const selection = stageSelection;
      const mentions = queuedMentions ?? (reuseImages !== undefined ? [] : mentionsForTurn(text, mentionsRef.current, pickedMentions(scope)));
      // The Skill's input is a picture: the stage selection, an attachment or an `@` reference. Without one, nothing is sent.
      if (toolSkill?.requiresImage && reuseImages === undefined && queuedSelection === undefined
        && images.length === 0 && mentions.length === 0 && !selection.some((claim) => claim.kind === 'image')) {
        toast.error(schemaText(schema, 'ui.studio.skill.needs_image'));
        return;
      }
      // The same words again while the first send is still on its way (a held or doubled
      // Enter): the box is only cleared once the server took the turn, so this is one message.
      if (turnRunning && !reuseImages && startingRef.current?.sessionId === sessionId && startingRef.current.text === text) return;
      // #292: a picture read from disk or pasted is still a data address; it becomes the person's input asset
      // before the turn is sent or queued, so the message stores the asset's address, never the picture itself.
      // A failure sends nothing: the draft stays in the box, a queued turn goes back into it (see the queue
      // below) and a replayed round is left as it was.
      if (images.some(isInlineImage)) {
        setUploading(true);
        try {
          ({ images, claims } = await uploadInlineImages(images, claims, {
            upload: (picture) => uploadWorkspaceInputAssetResult(schema, picture, inlineImageFilename(schema, picture)),
            maximumBytes: schema.runtime.input_assets.upload_max_bytes,
          }));
        } catch (error) {
          const message = reportInlineImageFailure(schema, error);
          if (isCurrentSessionScope(scope)) toast.error(message);
          return;
        } finally {
          setUploading(false);
        }
        if (!isCurrentSessionScope(scope)) return;
      }
      // The draft's picks are spent with the draft (a queued turn carries its claims already).
      if (queuedMentions === undefined && reuseImages === undefined) pickedMentionsRef.current = { scope, picks: [] };
      if (turnRunning) {
        setQueue((current) => enqueueTurn(current, sessionId, {
          id: `queued-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
          text, images, claims, selection, mentions, ...(inputSource ? { inputSource } : {}),
        }));
        useWorkspaceStore.getState().forgetAttachmentSources(images);
        // The draft is spent with the turn, even when the person has moved to another session meanwhile
        // (the scoped setters below are then no-ops and could not empty the stored draft). Only a send
        // accepted in this visit spends this visit's reads; a visit begun since has its own draft.
        if (isCurrentSessionScope(scope)) draftEpoch.current += 1;
        clearDraft(sessionId);
        if (isCurrentSessionScope(scope)) {
          setInput('');
          setAttachedImages([]);
          setCanvasReference(null);
          setAttachmentPurpose(null);
          setBranchFrom(null);
        }
        return;
      }
      const skillId = choiceAnswer ? null : sessionSkillId(skillBySession, sessionId);
      const selectedSkill = skillCatalog.catalog?.skills.find(item => item.id === skillId);
      if (skillId) {
        if (preflightRef.current?.sessionId === sessionId) return;
        const request = { sessionId, controller: new AbortController() };
        preflightRef.current = request; setPreparingSession(sessionId);
        try {
          const current = await configurationClient.effective(domain, selectedModel, sessionId, request.controller.signal);
          if (!selectedSkill || !current.skills.some(item => item.id === skillId)) {
            toast.error(schemaText(schema, 'ui.workspace.skills.unavailable')); return;
          }
        } catch { if (!request.controller.signal.aborted) toast.error(schemaText(schema, 'ui.workspace.skills.load_failed')); return; }
        finally { if (preflightRef.current === request) { preflightRef.current = null; setPreparingSession(null); } }
        if (request.controller.signal.aborted || !isCurrentSessionScope(scope)) return;
        const currentWorkspace = useWorkspaceStore.getState();
        if (useChatStore.getState().activeSessionId !== sessionId || useChatStore.getState().streamingBuffers[sessionId] ||
          stageDomainOf(selectionForSession(currentWorkspace.currentObject, sessionId)) !== domain ||
          currentWorkspace.modelByDomain[domain] !== selectedModel ||
          sessionSkillId(skillBySessionRef.current, sessionId) !== skillId) return;
      }
      await withSessionSendLock(sendPreparation.current, sessionId, async () => {
        setPreparingSession(sessionId);
        try {
          // Opening parameters does not write. Send flushes only explicit dirty edits before the Agent reads that Offering scope.
          try {
            const contexts = [imageGeneration, modelGeneration]
              .filter((params) => params.allowed)
              .map((params) => params.nextTurn)
              .filter((params) => params.offering?.available);
            await Promise.all(contexts.map((params) => params.flush()));
          } catch {
            if (isCurrentSessionScope(scope)) toast.error(schemaText(schema, 'ui.workspace.harness.save_failed'));
            return;
          }
          if (!isCurrentSessionScope(scope) || useWorkspaceStore.getState().modelByDomain[domain] !== selectedModel ||
              useChatStore.getState().streamingBuffers[sessionId] || startingRef.current?.sessionId === sessionId) return;
          let replaced = false;
          // "重新生成"和"编辑后重发"替换最后一轮：先删旧消息，删不掉就整轮保持原样。
          if (replaceIds && replaceIds.length > 0) {
            const previous = useChatStore.getState().messagesBySession[sessionId] ?? [];
            try {
              for (const id of replaceIds) await chatClient.deleteMessage(sessionId, id);
            } catch {
              const page = await chatClient.listMessagePage(sessionId, { limit: messagePageSize }).catch(() => null);
              if (page) applyNewestMessagePage(sessionId, page);
              else setMessages(sessionId, previous);
              toast.error(schemaText(schema, 'ui.chat.toast.turn_replace_failed'));
              return;
            }
            for (const id of replaceIds) deleteMessage(sessionId, id);
            setEditing(null);
            replaced = true;
          }
          startedTurnsRef.current += 1;
          const starting = { sessionId, text };
          startingRef.current = starting;
          setPreparingSession(null);
          initStreaming(sessionId, {
            content: text,
            images: images.map((url) => ({ url, taskId: '' })),
          });
          const controller = new AbortController();
          controllers.current.set(sessionId, controller);
          let closeEventSource: (() => void) | undefined;
          let removeAbortListener: (() => void) | undefined;
          // Set once the server has the turn: from then on nothing is put back into the box.
          let sent = false;
          const queued = queuedSelection !== undefined;
          try {
            await chatClient.startTurn(
              sessionId,
              text,
              {
                ...(selectedModel ? { model: selectedModel } : {}),
                creativeDomain: domain,
                ...(Object.keys(otherDomainModels).length > 0 ? { domainModels: otherDomainModels } : {}),
                ...(selectedSkill ? { skill: { id: selectedSkill.id, name: selectedSkill.name } } : {}),
                ...(images.length > 0 ? { imageURLs: images } : {}),
                ...(claims.length > 0 ? { attachments: claims } : {}),
                ...(selection.length > 0 ? { selection } : {}),
                ...(mentions.length > 0 ? { mentions } : {}),
                ...(inputSource ? { inputSource } : {}),
                ...(choiceAnswer ? { choiceAnswer } : {}),
              },
              controller.signal,
            );
            sent = true;
            extra?.onSent?.();
            // A one-shot Skill (declared in the Schema) is spent once the server has the turn; a failed send keeps it.
            if (selectedSkill?.oneShot && sessionSkillId(skillBySessionRef.current, sessionId) === selectedSkill.id) {
              const next = { ...skillBySessionRef.current, [sessionId]: null };
              skillBySessionRef.current = next;
              setSkillBySession(next);
            }
            if (startingRef.current === starting) startingRef.current = null;
            useWorkspaceStore.getState().forgetAttachmentSources(images);
            // The draft is spent with the turn, even when the person has moved to another session meanwhile
            // (the scoped setters below are then no-ops and could not empty the stored draft). Only a send
            // accepted in this visit spends this visit's reads; a visit begun since has its own draft.
            // A choice-card answer leaves whatever the person is writing in the box.
            if (!choiceAnswer) {
              if (isCurrentSessionScope(scope)) draftEpoch.current += 1;
              clearDraft(sessionId);
              if (isCurrentSessionScope(scope)) {
                setInput('');
                setAttachedImages([]);
                setCanvasReference(null);
                setAttachmentPurpose(null);
                setBranchFrom(null);
              }
            } else {
              void refreshChoices();
            }
            await new Promise<void>((resolve, reject) => {
              if (controller.signal.aborted) { resolve(); return; }
              closeEventSource = connectEventSource(sessionId, {
                fromId: 0,
                onDone: () => resolve(),
                onError: () =>
                  reject(
                    new Error(schema.chat.presentation.messages.sse_interrupted),
                  ),
              });
              const onAbort = () => {
                closeEventSource?.();
                resolve();
              };
              controller.signal.addEventListener('abort', onAbort, { once: true });
              removeAbortListener = () => controller.signal.removeEventListener('abort', onAbort);
              if (controller.signal.aborted) onAbort();
            });
          } catch (error) {
            // The turn never left: the words were already out of the box (the old round was replaced,
            // or the message had waited in the queue), so they go back, not lost.
            const restore = !sent && (replaced || queued) && isCurrentSessionScope(scope) && !controller.signal.aborted;
            if ((error as Error).name !== 'AbortError') {
              toast.error(
                restore && queued
                  ? schemaText(schema, 'ui.studio.chat.queued_restored')
                  : error instanceof Error && error.message !== ''
                    ? withErrorReference(error.message, error)
                    : ui.sendFailed,
              );
            }
            if (restore) {
              setInput((current) => (current === '' ? written : current));
              useWorkspaceStore.getState().queueChatAttachments(restoreAttachments(images, claims));
            }
          } finally {
            if (startingRef.current === starting) startingRef.current = null;
            closeEventSource?.();
            removeAbortListener?.();
            finishStreaming(sessionId);
            if (controllers.current.get(sessionId) === controller) controllers.current.delete(sessionId);
            chatClient
              .listMessagePage(sessionId, { limit: messagePageSize })
              .then((page) => applyNewestMessagePage(sessionId, page))
              .catch(() => {});
            void refreshApproval();
            void pending.refresh();
            // The Agent may have opened, or answered from a typed reply, a choice card (#321).
            void refreshChoices();
            // The Agent may have updated the settings summary during the turn.
            void refreshNotes();
          }
        } finally {
          setPreparingSession((current) => current === sessionId ? null : current);
        }
      });
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `pending.refresh` is stable per session
    [
      activeSessionId,
      imageGeneration,
      modelGeneration,
      artboard,
      deliveryPixels,
      proxyAssetUrl,
      attachedImages,
      attachmentPurpose,
      setAttachmentPurpose,
      setBranchFrom,
      canvasReference,
      hasCanvasReference,
      chatClient,
      connectEventSource,
      finishStreaming,
      initStreaming,
      modelChoices,
      modelsError,
      modelsLoading,
      schema,
      skillBySession,
      skillCatalog.catalog,
      configurationClient,
      setMessages,
      applyNewestMessagePage,
      messagePageSize,
      deleteMessage,
      setEditing,
      setInput,
      setAttachedImages,
      setCanvasReference,
      setUploading,
      refreshApproval,
      refreshChoices,
      refreshNotes,
      ui.sendFailed,
      uploading,
    ],
  );
  const sendRef = useRef(send);
  sendRef.current = send;
  const skippedObjectRef = useRef(skippedObject);
  skippedObjectRef.current = skippedObject;
  const sessionSelectionRef = useRef(sessionSelection);
  sessionSelectionRef.current = sessionSelection;
  const versionsRef = useRef(versions);
  versionsRef.current = versions;

  // 舞台上的动作（转 3D）请 Agent 做一件事：作为一轮发出，不经过输入框。上一轮还在跑就
  // 排队，和输入框里发的话一样。额外积分的同意不在发送前问：Agent 要提交时，对话里的批准卡
  // 会写明基础积分、额外积分和用途，批准才算同意。
  useEffect(() => {
    if (!turnRequest) return;
    clearTurnRequest();
    if (useChatStore.getState().activeSessionId === null) return;
    void sendRef.current(turnRequest.text, undefined, undefined, undefined, turnRequest.selection);
  }, [turnRequest, clearTurnRequest]);

  // 排队的话：上一轮结束（或回到这个会话时没有在跑的轮次）就发出最早的一条。没能发出就放回
  // 输入框，不让用户的话丢掉。
  useEffect(() => {
    if (!activeSessionId || isStreaming || preparing || uploading || drainingRef.current) return;
    const { turn } = takeQueuedTurn(queue, activeSessionId);
    if (!turn) return;
    const sessionId = activeSessionId;
    drainingRef.current = true;
    setQueue((current) => cancelQueuedTurn(current, sessionId, turn.id));
    const started = startedTurnsRef.current;
    void sendRef.current(turn.text, turn.images, turn.claims, undefined, turn.selection, turn.mentions, turn.inputSource).finally(() => {
      drainingRef.current = false;
      if (startedTurnsRef.current === started) {
        if (useChatStore.getState().activeSessionId === sessionId) {
          setInput((current) => (current === '' ? turn.text : current));
          useWorkspaceStore.getState().queueChatAttachments(restoreAttachments(turn.images, turn.claims));
        }
        toast.error(schemaText(schema, 'ui.studio.chat.queued_restored'));
      }
      setDrainTick((tick) => tick + 1);
    });
  }, [activeSessionId, drainTick, isStreaming, preparing, queue, schema, setInput, uploading]);

  // 「从这里继续」：这个对象成为下一次要求的引用（取消"不带它"），输入框拿到焦点。
  useEffect(() => {
    if (!continueRequest) return;
    clearContinueRequest();
    setSkippedObject(null);
    const from = sessionSelectionRef.current;
    setBranchFrom(from && !from.taskId.startsWith('canvas:')
      ? { key: objectKey(from), label: versionLabel(schema, tileOfObject(versionsRef.current, from), from.kind) }
      : null);
    if (useStudioStore.getState().chatCollapsed) setCollapsed(false);
    window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
  }, [continueRequest, clearContinueRequest, schema, setBranchFrom, setCollapsed, setSkippedObject]);

  // 起步卡片和"重新发起"只把文字放进输入框，让用户确认后自己发送。
  useEffect(() => {
    if (!composerPrefill) return;
    draftEpoch.current += 1;
    setInput(composerPrefill.text);
    clearComposerPrefill();
    if (useStudioStore.getState().chatCollapsed) setCollapsed(false);
    window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
  }, [composerPrefill, clearComposerPrefill, setCollapsed, setInput]);

  // The shared result detail for a chat picture: a sent picture on click, and a result picture only when its click cannot
  // put it on the stage (the card's ⓘ opens the detail on its own); ←/→ pages the message's pictures.
  // Reads the messages at click time (a ref), so the callback every message receives stays the same object (#291).
  const displayMessagesRef = useRef(displayMessages);
  displayMessagesRef.current = displayMessages;
  const openImageDetail = useCallback(
    (messageId: string, localIndex: number) => {
      const source = displayMessagesRef.current.find((item) => item.id === messageId);
      if (!source || !source.images || source.images.length === 0) return;
      const base = schema.chat.context.image_index_base;
      const prefix = schemaText(schema, source.role === 'user' ? 'ui.chat.message.uploaded_image' : 'ui.chat.message.generated_image');
      const group = source.images.map((image, index): ResultTarget => {
        const attachment = source.attachments?.find((item) => item.kind === 'image' && item.imageSlot === index + 1);
        const taskId = image.taskId || attachment?.taskId || '';
        return {
          kind: 'image', url: image.url, title: `${prefix} ${index + base}`, status: 'ready',
          ...(taskId ? { taskId } : {}),
          ...(taskId && activeSessionId ? { sessionId: activeSessionId } : {}),
          ...(attachment?.assetId ? { assetId: attachment.assetId } : {}),
          ...(attachment?.captureId ? { captureId: attachment.captureId } : {}),
        };
      });
      const target = group[Math.min(Math.max(localIndex, 0), group.length - 1)];
      showResultDetail(group.length > 1 ? { ...target, group } : target);
    },
    [activeSessionId, schema, showResultDetail],
  );

  const timeline = useMemo(() => interleaveGroups(displayMessages, threeViews), [displayMessages, threeViews]);
  // #321: each choice card follows the assistant message that asked; an open one not on the page goes last.
  const choicePlacement = useMemo(
    () => choiceCardPlacement(sessionChoices.cards, displayMessages.map((message) => message.id)),
    [sessionChoices.cards, displayMessages],
  );
  // #322: each context compaction shows as a display-only divider after the last message made no later
  // than it; it is not a message, so it is never copied, edited or sent.
  const sessionCompactions = useSessionCompactions(activeSessionId);
  const compactionDividers = useMemo(
    () => compactionPlacement(sessionCompactions ?? [], displayMessages, Boolean(olderCursor)),
    [sessionCompactions, displayMessages, olderCursor],
  );

  const optimisticPrefix = schema.chat.frontend.optimistic_message_id_prefix;
  const turnActionsOpen = Boolean(activeSessionId) && !isStreaming && !preparing && !uploading;
  const regenerate = useMemo(
    () => (turnActionsOpen ? regenerateTarget(displayMessages, optimisticPrefix) : null),
    [displayMessages, optimisticPrefix, turnActionsOpen],
  );
  const editable = useMemo(
    () => (turnActionsOpen ? editTarget(displayMessages, optimisticPrefix) : null),
    [displayMessages, optimisticPrefix, turnActionsOpen],
  );
  // The estimate of the turn being replaced: its paid steps are what a new run costs.
  // A turn with no paid step was plain conversation, which costs no credits, so no figure.
  const regenerateCredits = useMemo(() => {
    const assistant = displayMessages.find((item) => item.id === regenerate?.assistantId);
    const total = (assistant?.paidSteps ?? []).reduce((sum, step) => sum + (step.credits ?? 0), 0);
    return total > 0 ? total : undefined;
  }, [displayMessages, regenerate]);

  // 重新生成：丢弃最后一轮的回复并用同一条用户消息再来一次；重发没有送出时
  // 文字和图片会回到输入框（见 send）。
  const regenerateReply = useCallback(() => {
    if (!regenerate) return;
    const payload = turnPayload(regenerate.user);
    void send(payload.text, payload.images, payload.claims, regenerate.deleteIds);
  }, [regenerate, send]);

  const focusComposer = useCallback(() => {
    window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
  }, []);

  // 编辑后重发：把最后一条用户消息放回输入框（文字与图片及其来源），发送时替换那一轮。
  const editLastMessage = useCallback((message: ChatMessageType) => {
    if (!editable || editable.userId !== message.id) return;
    draftEpoch.current += 1;
    setEditing({ deleteIds: editable.deleteIds });
    setInput(message.content);
    setAttachedImages([]);
    useWorkspaceStore.getState().queueChatAttachments(editAttachments(message));
    focusComposer();
  }, [editable, focusComposer, setAttachedImages, setEditing, setInput]);

  // 更早的用户消息：放回输入框，改一改再发，不替换任何一轮。
  const copyToInput = useCallback((message: ChatMessageType) => {
    draftEpoch.current += 1;
    setEditing(null);
    setInput(message.content);
    setAttachedImages([]);
    useWorkspaceStore.getState().queueChatAttachments(editAttachments(message));
    focusComposer();
  }, [focusComposer, setAttachedImages, setEditing, setInput]);

  const cancelEditing = useCallback(() => {
    draftEpoch.current += 1;
    setEditing(null);
    setInput('');
    pickedMentionsRef.current = { scope: captureSessionScope(), picks: [] };
    setAttachedImages([]);
  }, [setAttachedImages, setEditing, setInput]);

  // 删除：先本地移除（乐观），失败时回填服务端列表并提示，不静默吞掉错误。
  const pickCandidate = useCallback(async (claim: ChatAttachmentClaim) => {
    const ok = await chooseCandidate(claim);
    if (!ok) toast.error(ui.pickFailed);
    return ok;
  }, [chooseCandidate, ui.pickFailed]);
  const removeMessage = useCallback(
    async (messageId: string) => {
      if (!activeSessionId) return;
      const sessionId = activeSessionId;
      const previous = useChatStore.getState().messagesBySession[sessionId] ?? [];
      deleteMessage(sessionId, messageId);
      try {
        await chatClient.deleteMessage(sessionId, messageId);
      } catch {
        setMessages(sessionId, previous);
        toast.error(
          schemaText(schema, 'ui.chat.toast.delete_message_retry'),
        );
      }
    },
    [activeSessionId, chatClient, deleteMessage, schema, setMessages],
  );
  const deleteFromList = useCallback((messageId: string) => { void removeMessage(messageId); }, [removeMessage]);

  const decideTurnConsent = useCallback(
    async (approved: boolean) => {
      if (!activeSessionId || !consent.requestId || consentBusy) return;
      try {
        await decideConsent(consent.requestId, approved);
        // A step approved here may finish after the turn is over: keep reading the version chain for it.
        if (approved) useStudioStore.getState().watchVersions();
      } catch (error) {
        const message = error instanceof AgentHarnessError
          ? schemaText(schema, error.detail) || ui.consentFailed
          : ui.consentFailed;
        toast.error(message);
      }
    },
    [activeSessionId, consent.requestId, consentBusy, decideConsent, schema, ui.consentFailed],
  );

  // A late approval submits a task after its turn has ended. The page registers the task at once
  // (so the count, the progress card and the version strip pick it up without a reload) and falls
  // back to reading the session's task list when the answer carried no task id yet.
  const hydrateLateTasks = useCallback((sessionId: string) => {
    void hydrateSessionTasks({
      tasksRoute: schema.runtime.http.routes.tasks,
      sessionId,
      limit: schema.runtime.frontend.session_menu_page_size,
      taskStates: schema.task_states,
      progressMaximum: schema.runtime.frontend.percentage_multiplier,
      consume: (input, projection) => {
        const store = useWorkspaceStore.getState();
        if (store.trackedTasks.some((task) => task.taskId === input.taskId)) return;
        store.registerTask({ ...input, autoPreview: false });
        useWorkspaceStore.getState().applyTaskDetail(input.taskId, projection, useChatStore.getState().activeSessionId);
      },
    }).catch(() => {});
  }, [schema]);

  const decideDetached = useCallback(
    async (card: ApprovalCardModel, approved: boolean) => {
      if (!activeSessionId || !deciding.current.begin(card.requestId)) return;
      const sessionId = activeSessionId;
      const requestId = card.requestId;
      setDecisionBusy((current) => new Set(current).add(requestId));
      setDecisionError((current) => {
        if (!(requestId in current)) return current;
        const { [requestId]: _cleared, ...rest } = current;
        return rest;
      });
      try {
        const result = await pending.decide(requestId, approved);
        if (approved) {
          useStudioStore.getState().watchVersions();
          if (result.taskId) {
            registerTask({
              taskId: result.taskId,
              sessionId,
              capability: card.capability,
              prompt: card.prompt,
              progressMaximum: schema.runtime.frontend.percentage_multiplier,
            });
          } else {
            for (const delay of LATE_TASK_HYDRATE_MILLISECONDS) window.setTimeout(() => hydrateLateTasks(sessionId), delay);
          }
        }
        // 服务端在对话里插入了"已批准 / 已拒绝"的结果消息：重新读取。
        if (result.messageId) {
          const page = await chatClient.listMessagePage(sessionId, { limit: messagePageSize }).catch(() => null);
          if (page && useChatStore.getState().activeSessionId === sessionId) applyNewestMessagePage(sessionId, page);
        }
        void refreshApproval();
      } catch (error) {
        const detail = error instanceof PendingApprovalError ? schemaText(schema, error.detail) : '';
        const reason = detail || schemaText(schema, 'ui.studio.approval.card.decide_failed');
        setDecisionError((current) => ({
          ...current,
          [requestId]: detail
            ? formatSchemaText(schemaText(schema, 'ui.studio.approval.card.decide_error'), { reason: detail })
            : reason,
        }));
      } finally {
        deciding.current.end(requestId);
        setDecisionBusy((current) => {
          const next = new Set(current);
          next.delete(requestId);
          return next;
        });
      }
    },
    [activeSessionId, applyNewestMessagePage, chatClient, hydrateLateTasks, messagePageSize, pending, refreshApproval, registerTask, schema],
  );

  // #321: the answer goes back as a user message of its own with the card's structured answer.
  // #335: confirmed while the composer's pictures are still being read or uploaded, it is held (the card
  // says so) and goes out once they are done, instead of being dropped by the attachment guard in `send`.
  const [heldChoice, setHeldChoice] = useState<{ card: ChoiceCardModel; selection: ChoiceSelection; text: string; scope: SessionScope } | null>(null);
  // An answer the server never took (the turn did not start, or `send` refused it) says so on its card (#335).
  const [choiceError, setChoiceError] = useState<{ cardId: string; text: string } | null>(null);
  const answerChoice = useCallback(async (card: ChoiceCardModel, selection: ChoiceSelection, text: string) => {
    if (answeringChoice) return;
    setChoiceError(null);
    const scope = captureSessionScope();
    if (uploading || attachmentsPending(attachmentReads, scope)) {
      setHeldChoice({ card, selection, text, scope });
      return;
    }
    setAnsweringChoice(card.id);
    let accepted = false;
    try {
      await sendRef.current(text, [], [], undefined, [], [], undefined, {
        choiceAnswer: selection.useDefault
          ? { choiceId: card.id, useDefault: true }
          : { choiceId: card.id, optionId: selection.optionId },
        onSent: () => { accepted = true; },
      });
      if (!accepted) setChoiceError({ cardId: card.id, text: schemaText(schema, 'ui.chat.choice.failed') });
    } catch {
      setChoiceError({ cardId: card.id, text: schemaText(schema, 'ui.chat.choice.failed') });
    } finally {
      setAnsweringChoice(null);
      void refreshChoices();
    }
  }, [answeringChoice, refreshChoices, schema, uploading]);
  useEffect(() => {
    if (!heldChoice) return;
    const release = heldChoiceRelease({
      sameScope: isCurrentSessionScope(heldChoice.scope),
      cardOpen: sessionChoices.cards.some((card) => card.id === heldChoice.card.id && card.state === 'pending'),
      attachmentsBusy: uploading || attachmentsPending(attachmentReads, heldChoice.scope),
      streaming: isStreaming,
    });
    if (release === 'wait') return;
    setHeldChoice(null);
    if (release === 'send') void answerChoice(heldChoice.card, heldChoice.selection, heldChoice.text);
  }, [activeSessionId, answerChoice, heldChoice, isStreaming, sessionChoices.cards, uploading]);
  const renderChoiceCard = (card: ChoiceCardModel) => (
    <ChoiceCard
      key={card.id}
      card={card}
      busy={answeringChoice === card.id}
      waiting={heldChoice?.card.id === card.id}
      disabled={isStreaming || answeringChoice !== null || heldChoice !== null}
      error={choiceError?.cardId === card.id && card.state === 'pending' ? choiceError.text : null}
      onAnswer={(selection, text) => { void answerChoice(card, selection, text); }}
    />
  );

  const rerequest = useCallback((prompt: string) => {
    prefillComposer(formatSchemaText(schemaText(schema, 'ui.studio.approval.card.rerequest_prefill'), { prompt }));
  }, [prefillComposer, schema]);

  // What a picture on an approval card is called: the view it came from, else its version number.
  const labelOfInput = useCallback((input: PendingApprovalInput): string => {
    for (const group of threeViews) {
      const slot = group.slots.find((item) => (input.assetId && item.outputAssetId === input.assetId) || (input.taskId && item.taskId === input.taskId));
      if (slot) return slot.label;
    }
    if (!input.taskId) return '';
    const key = versions.byTask.get(input.taskId);
    const tile = key ? versions.tiles.find((item) => item.key === key) : undefined;
    return tile ? versionLabel(schema, tile, tile.domain === 'model' ? 'model' : 'image') : '';
  }, [schema, threeViews, versions.byTask, versions.tiles]);
  const turnCard = consentAwaiting ? cardFromConsent(consent.pending, consent.requestId) : null;
  const approvalCards = [
    ...(turnCard ? [turnCard] : []),
    ...pending.items.map((item) => cardFromPending(item, labelOfInput)),
  ];

  // Tasks of this session that the page saw running. When one of them ends outside a turn (a late
  // approval), the result message, the version strip and the session credits catch up by themselves.
  const trackedTasks = useWorkspaceStore((state) => state.trackedTasks);
  const watchedTasksRef = useRef(new Set<string>());
  const sessionTasks = useMemo(
    () => trackedTasks.filter((task) => task.sessionId === activeSessionId && !task.notFound),
    [trackedTasks, activeSessionId],
  );
  for (const task of sessionTasks) if (!task.terminal) watchedTasksRef.current.add(task.taskId);
  const settledWatchedKey = sessionTasks
    .filter((task) => task.terminal && watchedTasksRef.current.has(task.taskId))
    .map((task) => task.taskId)
    .sort()
    .join(',');
  const settledKey = sessionTasks.map((task) => `${task.taskId}:${task.terminal ? 1 : 0}`).sort().join(',');
  const viewsKey = threeViews.flatMap((group) => group.slots.map((slot) => `${slot.key}:${slot.status}`)).join(',');
  const isStreamingRef = useRef(isStreaming);
  isStreamingRef.current = isStreaming;
  useEffect(() => {
    if (!activeSessionId || settledWatchedKey === '') return;
    const sessionId = activeSessionId;
    const reload = () => {
      if (isStreamingRef.current) return;
      chatClient.listMessagePage(sessionId, { limit: messagePageSize }).then((page) => {
        if (useChatStore.getState().activeSessionId === sessionId && !isStreamingRef.current) applyNewestMessagePage(sessionId, page);
      }).catch(() => {});
    };
    // The result message is written when the server's sweep sees the task end, a moment later.
    reload();
    const timers = LATE_RESULT_RELOAD_MILLISECONDS.map((delay) => window.setTimeout(reload, delay));
    return () => timers.forEach((timer) => window.clearTimeout(timer));
  }, [activeSessionId, applyNewestMessagePage, chatClient, messagePageSize, settledWatchedKey]);
  const refreshApprovalRef = useRef(refreshApproval);
  refreshApprovalRef.current = refreshApproval;
  // Credits are server-summed: ask again whenever a task or a view starts or ends.
  useEffect(() => {
    if (!activeSessionId) return;
    void refreshApprovalRef.current();
  }, [activeSessionId, settledKey, viewsKey]);

  const sessionMessages = displayMessages;
  // Recomputed with every streamed chunk; the previous map is kept while its content is the same, so the
  // memoized messages that receive it are not redrawn (#291).
  const handleReferencesRef = useRef<ReturnType<typeof handleReferences> | null>(null);
  const sessionHandleReferences = useMemo(() => {
    const next = handleReferences(sessionMessages);
    const previous = handleReferencesRef.current;
    return previous && sameData(previous, next) ? previous : next;
  }, [sessionMessages]);
  handleReferencesRef.current = sessionHandleReferences;
  const boardNodes = useCanvasStore((state) => (activeSessionId ? state.nodesBySession[activeSessionId] : undefined) ?? NO_NODES);
  const chip = useMemo((): ComposerChip | null => {
    if (!sessionSelection) return null;
    const tile = tileOfObject(versions, sessionSelection);
    // A picture drawn on the board that is no version has no chip; a merged version (手绘) is one.
    if (sessionSelection.taskId.startsWith('canvas:') && !tile) return null;
    // The artboard holds something: the turn carries the whole artboard.
    if (sessionSelection.kind === 'image' && artboard && currentObjectSource(boardNodes, artboard) === 'canvas') {
      return {
        kind: 'image',
        thumbnail: previewAssetUrl(sessionSelection.url),
        label: schemaText(schema, 'ui.studio.composer.chip.canvas'),
        handle: '',
      };
    }
    return {
      kind: sessionSelection.kind,
      // #290: chips are small; they show the preview of a task picture.
      thumbnail: sessionSelection.kind === 'image'
        ? previewAssetUrl(sessionSelection.url)
        : tile?.thumbnailUrl ? previewAssetUrl(tile.thumbnailUrl) : '',
      label: versionLabel(schema, tile, sessionSelection.kind),
      handle: sessionSelection.handle ?? handleOfObject(sessionMessages, sessionSelection),
    };
  }, [artboard, boardNodes, previewAssetUrl, schema, sessionMessages, sessionSelection, versions]);
  const chipSkipped = objectSkipped;
  // The branch chip belongs to the version it was made for: another version on the stage drops it.
  const branchChip = useMemo((): ComposerBranch | null => {
    if (!branchFrom || !sessionSelection || objectKey(sessionSelection) !== branchFrom.key) return null;
    return {
      label: branchFrom.label,
      onClear: () => {
        // The new version follows the object the turn carries; leaving the branch leaves the object too.
        setBranchFrom(null);
        setSkippedObject(branchFrom.key);
      },
    };
  }, [branchFrom, sessionSelection, setBranchFrom, setSkippedObject]);
  chipHandleRef.current = chip?.handle ?? '';
  // The mask being painted shows above the box so it is never forgotten while chatting.
  const maskTool = useCanvasStore((state) => state.activeTool === 'mask');
  const maskDraft = useMaskStore((state) => state.draft);
  const maskStrokes = maskStrokeCount(maskDraft, activeSessionId);
  const maskChip = useMemo((): ComposerMask | null => {
    if (!maskTool && maskStrokes === 0) return null;
    // The mask belongs to the canvas (the dashed artboard), not to a picture: no picture handle is named.
    return {
      strokes: maskStrokes,
      handle: '',
      onClear: () => {
        if (maskStrokes > 0) { useMaskStore.getState().ask({ kind: 'exit' }); return; }
        useMaskStore.getState().clearDraft();
        useCanvasStore.getState().setActiveTool('select');
      },
    };
  }, [maskStrokes, maskTool]);
  // Every picture an `@` can name: the session's own (with their handles), the ones waiting in the box,
  // generated versions and the layers on the canvas of this session.
  const canvasNodes = useCanvasStore((state) => (activeSessionId ? state.nodesBySession[activeSessionId] : undefined));
  const pendingSources = useWorkspaceStore((state) => state.attachmentSources);
  const mentionList = useMemo((): MentionCandidate[] => {
    const base = schema.chat.context.image_index_base;
    const attachedNow = [...attachedImages, ...(hasCanvasReference && canvasReference ? [canvasReference] : [])];
    const uploadName = (index: number) => formatSchemaText(schemaText(schema, 'ui.studio.composer.attachment.title'), { index: index + base });
    const versionImages = versions.tiles.flatMap((tile) => {
      if (tile.domain !== 'image' || tile.status !== 'ready' || tile.groupLabel !== '') return [];
      const label = versionLabel(schema, tile, 'image');
      const assets = tile.assets.filter((asset) => asset.kind === 'image' && asset.url);
      return assets.map((asset, index) => ({
        name: assets.length > 1 ? `${label}-${index + 1}` : label,
        taskId: tile.taskId,
        assetId: asset.id,
        url: asset.url,
      }));
    });
    const layers = [...(canvasNodes ?? [])]
      .filter((node) => node.type === 'image' && node.visible !== false && node.imageUrl)
      .sort((left, right) => left.zIndex - right.zIndex)
      .map((node, index) => ({
        name: formatSchemaText(schemaText(schema, 'ui.studio.composer.mention.token_layer'), { index: index + base }),
        url: node.imageUrl,
        ...(node.taskId && !node.taskId.startsWith('canvas:') && !node.taskId.startsWith('upload:') ? { taskId: node.taskId } : {}),
        ...(node.captureId ? { captureId: node.captureId } : {}),
      }));
    const listed = buildMentionCandidates({
      history: mentionCandidates(sessionMessages),
      uploads: attachedNow.map((url, index) => ({ url, name: uploadName(index), claim: pendingSources[url] })),
      versions: versionImages,
      layers,
    });
    // The first entry is the whole canvas, whenever the session has a dashed frame to rasterise.
    return withBoardMention(listed, artboard ? boardMentionCandidate(schemaText(schema, 'ui.studio.composer.mention.token_board')) : null);
  }, [artboard, attachedImages, canvasNodes, canvasReference, hasCanvasReference, pendingSources, schema, sessionMessages, versions.tiles]);
  mentionsRef.current = mentionList;
  const mentions = useMemo((): ComposerMention[] => mentionList.map((candidate) => {
    let thumbnail = '';
    if (candidate.kind === 'image') {
      thumbnail = previewAssetUrl(candidate.url
        || (candidate.taskId && candidate.assetId ? taskAssetRoute(schema, candidate.taskId, candidate.assetId) : ''));
    } else {
      const tile = versions.tiles.find((item) => item.taskIds.includes(candidate.taskId ?? ''));
      thumbnail = tile?.thumbnailUrl ? previewAssetUrl(tile.thumbnailUrl) : '';
    }
    return { ...candidate, thumbnail };
  }), [mentionList, previewAssetUrl, schema, versions.tiles]);

  const stopTurn = () => {
    // 真停止：请求服务端取消轮次（含在飞生成任务），再断开本地 SSE；失败时仍退回仅断开连接。
    if (activeSessionId) {
      void chatClient.cancelTurn(activeSessionId).catch(() => undefined);
      controllers.current.get(activeSessionId)?.abort();
    }
  };

  const title = schemaText(schema, 'ui.studio.chat.title');
  const collapseLabel = schemaText(schema, 'ui.studio.chat.collapse');
  const expandLabel = schemaText(schema, 'ui.studio.chat.expand');

  return (
    <aside
      aria-label={title}
      data-testid="studio-chat-column"
      data-collapsed={collapsed}
      style={{ width: collapsed ? CHAT_RAIL_WIDTH : chatWidth }}
      className="relative flex h-full min-h-0 shrink-0 flex-col border-r border-border bg-sidebar"
    >
      {collapsed ? (
        <div className="flex flex-1 flex-col items-center gap-3 py-3" data-testid="chat-rail">
          <HintTooltip name={expandLabel}>
            <Action type="button" variant="ghost" size="icon" onClick={() => setCollapsed(false)} aria-label={expandLabel} className="text-muted-foreground">
              <ChevronsRight className="size-4" />
            </Action>
          </HintTooltip>
          <span className="relative flex size-9 items-center justify-center rounded-lg bg-primary/15 text-primary">
            <Bot aria-hidden className="size-4" />
            {isStreaming ? <span aria-hidden className="absolute -right-0.5 -top-0.5 size-2 animate-pulse rounded-full bg-primary" /> : null}
            {pendingCount > 0 ? (
              <span
                role="status"
                aria-label={schemaCountMessage(schema, 'ui.studio.chat.pending_badge', pendingCount, { count: pendingCount })}
                className="absolute -bottom-1 -right-1 flex min-w-4 items-center justify-center rounded-full bg-warning px-1 text-[10px] font-semibold leading-4 text-primary-foreground"
              >
                {pendingCount}
              </span>
            ) : null}
          </span>
        </div>
      ) : (
        <>
          <header className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
            <Bot aria-hidden className="size-4 text-primary" />
            <h2 className="flex-1 text-sm font-semibold">{title}</h2>
            <HintTooltip name={collapseLabel}>
              <Action type="button" variant="ghost" size="icon-sm" onClick={() => setCollapsed(true)} aria-label={collapseLabel} className="text-muted-foreground">
                <ChevronsLeft className="size-4" />
              </Action>
            </HintTooltip>
          </header>

          {activeSessionId ? (
            <SessionNotesPanel
              key={activeSessionId}
              text={notesText}
              saved={sessionNotes.notes}
              maximum={schema.chat.context.maximum_session_notes_characters}
              busy={sessionNotes.busy}
              onSave={async (value) => {
                const ok = await sessionNotes.save(value);
                if (!ok) toast.error(ui.notesSaveFailed);
                return ok;
              }}
            />
          ) : null}

          <div className="relative flex min-h-0 flex-1 flex-col">
          <div
            ref={messagesRef}
            onScroll={onMessagesScroll}
            onCopy={(event) => {
              // Selected chat pictures go with the words (#281): their draggable frames keep them out of a plain copy.
              const selection = window.getSelection();
              if (!selection || selection.isCollapsed || selection.rangeCount === 0) return;
              const range = selection.getRangeAt(0);
              const pictures = Array.from(event.currentTarget.querySelectorAll<HTMLImageElement>('img[data-chat-picture]'))
                .filter((image) => range.intersectsNode(image))
                // The card shows a preview (#290); the original's address (data-chat-picture) is what is copied.
                .map((image) => image.dataset.chatPicture || image.currentSrc || image.src);
              // Whole Agent replies go as their Markdown source (#333); a partial selection keeps the rendered words.
              const replies = selectedTranscriptText(selection, event.currentTarget, markdownSourceOf);
              const payload = transcriptClipboard(replies ?? selection.toString(), pictures, replies !== null);
              if (!payload) return;
              event.preventDefault();
              event.clipboardData.setData('text/plain', payload.plain);
              event.clipboardData.setData('text/html', payload.html);
            }}
            className="min-h-0 flex-1 overflow-y-auto px-2 pt-3"
            style={{ paddingBottom: `calc(0.75rem + ${floatingHeight}px)` }}
            data-testid="chat-messages"
          >
            {historyLoading && displayMessages.length === 0 ? (
              <ChatHistorySkeleton label={schemaText(schema, 'ui.chat.loading')} />
            ) : displayMessages.length === 0 && threeViews.length === 0 && !isStreaming && approvalCards.length === 0 && queuedTurns.length === 0 ? (
              <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
                <div className="rounded-xl bg-primary/15 p-3 text-primary"><ImagePlus className="size-6" /></div>
                <p className="max-w-xs text-sm leading-6 text-muted-foreground">{schemaText(schema, 'ui.workspace.agent.empty')}</p>
              </div>
            ) : (
              <>
                {olderCursor ? (
                  <div className="flex justify-center pb-2">
                    <button
                      type="button"
                      className="rounded-full px-3 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-60"
                      disabled={earlierState === 'loading'}
                      onClick={() => void loadEarlier()}
                      data-testid="chat-load-earlier"
                    >
                      {schemaText(schema, earlierState === 'loading'
                        ? 'ui.chat.history.loading_earlier'
                        : earlierState === 'failed' ? 'ui.chat.history.load_earlier_failed' : 'ui.chat.history.load_earlier')}
                    </button>
                  </div>
                ) : null}
                {compactionDividers.leading.map((compaction) => <ContextCompactedDivider key={compaction.id} compaction={compaction} />)}
                {timeline.map((item) => item.kind === 'group' ? (
                  <ThreeViewCard key={`three-view-${item.group.runId}`} group={item.group} />
                ) : (
                  <Fragment key={item.message.id}>
                  <ChatMessage
                    message={item.message}
                    sessionId={activeSessionId}
                    onImageClick={openImageDetail}
                    handleReferences={sessionHandleReferences}
                    pick={sessionPick.pick}
                    pickBusy={sessionPick.busy}
                    onPick={pickCandidate}
                    onCopyToInput={item.message.isPending || editable?.userId === item.message.id ? undefined : copyToInput}
                    onRegenerate={regenerate?.assistantId === item.message.id ? regenerateReply : undefined}
                    regenerateCredits={regenerate?.assistantId === item.message.id ? regenerateCredits : undefined}
                    onEdit={editable?.userId === item.message.id ? editLastMessage : undefined}
                    onDelete={item.message.isPending ? undefined : deleteFromList}
                  />
                  {(choicePlacement.byMessage.get(item.message.id)?.length ?? 0) > 0 ? (
                    <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-choice-cards">
                      {choicePlacement.byMessage.get(item.message.id)!.map(renderChoiceCard)}
                    </div>
                  ) : null}
                  {compactionDividers.byMessage.get(item.message.id)?.map((compaction) => (
                    <ContextCompactedDivider key={compaction.id} compaction={compaction} />
                  ))}
                  </Fragment>
                ))}
                {isStreaming && statusText ? (
                  <ChatStatusBubble text={statusText} progress={progress} thinking={streamBuf?.genState?.thinking === true} />
                ) : null}
              </>
            )}
            <TaskStatusCards part="failed" />
            {choicePlacement.loose.length > 0 ? (
              <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-choice-cards">
                {choicePlacement.loose.map(renderChoiceCard)}
              </div>
            ) : null}
            {approvalCards.length > 0 ? (
              <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-approval-cards">
                {approvalCards.map((card) => (
                  <ApprovalCard
                    key={card.requestId}
                    model={card}
                    busy={card.source === 'turn' ? consentBusy : decisionBusy.has(card.requestId)}
                    now={now}
                    error={decisionError[card.requestId] ?? null}
                    onDecide={(approved) => {
                      if (card.source === 'turn') void decideTurnConsent(approved);
                      else void decideDetached(card, approved);
                    }}
                    onRerequest={() => rerequest(card.prompt)}
                  />
                ))}
              </div>
            ) : null}
            <QueuedTurns
              turns={queuedTurns}
              onCancel={(id) => { if (activeSessionId) setQueue((current) => cancelQueuedTurn(current, activeSessionId, id)); }}
            />
            <div ref={messagesEndRef} />
          </div>
          {/* #244：进行中的任务悬浮在消息区底部、输入区上方，不撑开消息滚动；消息区按其实测高度留出底部空间。 */}
          <div className="pointer-events-none absolute inset-x-2 bottom-2 flex max-h-[45%] flex-col items-stretch gap-2">
            {showLatest ? (
              <Action
                type="button" variant="secondary" size="sm" onClick={jumpToLatest}
                className="pointer-events-auto self-center shadow-md" data-testid="chat-scroll-latest"
              >
                <ArrowDown aria-hidden className="size-3.5" />
                {schemaText(schema, 'ui.chat.scroll.latest')}
              </Action>
            ) : null}
            <div ref={floatingRef} className="pointer-events-auto flex min-h-0 flex-col" data-testid="chat-task-float">
              <TaskStatusCards part="running" />
            </div>
          </div>
          </div>

          <Composer
            value={input}
            onChange={setInput}
            onSend={() => { void send(input, undefined, undefined, editing?.deleteIds); }}
            onStop={stopTurn}
            streaming={isStreaming}
            locked={uploading || preparing}
            canSend={(input.trim() !== '' || attachedImages.length > 0 || hasCanvasReference || Boolean(currentSkill?.tool)) && Boolean(activeSessionId)}
            chip={chip}
            chipSkipped={chipSkipped}
            onToggleChip={() => {
              if (sessionSelection) setSkippedObject(chipSkipped ? null : objectKey(sessionSelection));
            }}
            mask={maskChip}
            branch={branchChip}
            attachmentPurpose={attachmentPurpose}
            onAttachmentPurpose={setAttachmentPurpose}
            images={attachedImages}
            onRemoveImage={(index) => setAttachedImages((current) => current.filter((_, imageIndex) => imageIndex !== index))}
            pinnedImage={canvasReference || null}
            onUnpin={() => setCanvasReference(null)}
            acceptedImages={acceptedImages}
            onFiles={(files) => { void attachFiles(files); }}
            onPasteImages={(urls) => { void attachPastedImages(urls); }}
            editing={editing !== null}
            onCancelEdit={cancelEditing}
            skill={{
              sessionId: activeSessionId,
              catalog: skillCatalog.catalog,
              effective: effective.data,
              selectedId: selectedSkillId,
              unavailable: skillSelection.unavailable,
              loading: skillCatalog.loading || effective.loading,
              failed: skillCatalog.failed || effective.failed,
              disabled: isStreaming || preparing,
              onSelect: (id) => {
                if (!activeSessionId) return;
                const next = { ...skillBySessionRef.current, [activeSessionId]: id };
                skillBySessionRef.current = next;
                setSkillBySession(next);
                // A Skill's recommended size becomes the size default when the model offers it.
                const chosen = skillCatalog.catalog?.skills.find((item) => item.id === id);
                const size = chosen ? skillSizeDefault(generation.nextTurn.model.sizeParams, chosen.recommendedSize) : null;
                if (size) generation.nextTurn.setValue(size.name, size.value);
              },
              onDeleted: (id) => {
                // 指向被删 Skill 的会话选择改为“不使用”，不保留失效 ID（#238）。
                const next = Object.fromEntries(Object.entries(skillBySessionRef.current)
                  .map(([session, selected]) => [session, selected === id ? null : selected]));
                skillBySessionRef.current = next;
                setSkillBySession(next);
                setSkillRefreshVersion((version) => version + 1);
              },
              onCreated: (id) => {
                if (activeSessionId) {
                  const next = { ...skillBySessionRef.current, [activeSessionId]: id };
                  skillBySessionRef.current = next;
                  setSkillBySession(next);
                }
                setSkillRefreshVersion((version) => version + 1);
              },
            }}
            params={{
              params: generation,
              domains: [imageGeneration, modelGeneration],
              hasSession: Boolean(activeSessionId),
              skill: currentSkill ? { name: currentSkill.name, recommendedSize: currentSkill.recommendedSize } : null,
              effective,
              canvas: activeSessionId ? { sessionId: activeSessionId, delivery: composerDelivery } : null,
            }}
            sendCredits={generation.nextTurn.estimate.total}
            referenceLimit={composerReferenceLimit}
            mentions={mentions}
            onMentionPicked={(candidate) => {
              const scope = captureSessionScope();
              pickedMentionsRef.current = { scope, picks: [...pickedMentions(scope).filter((item) => item.handle !== candidate.handle), candidate] };
            }}
          />
        </>
      )}
      {!collapsed ? (
        <ResizeHandle width={chatWidth} label={schemaText(schema, 'ui.studio.chat.resize')} onResize={setChatWidth} />
      ) : null}
    </aside>
  );
}

function objectKey(object: { taskId: string; assetId?: string; url: string }): string {
  return `${object.taskId}:${object.assetId ?? object.url}`;
}
