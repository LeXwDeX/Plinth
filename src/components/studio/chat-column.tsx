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
import useSWR from 'swr';

import {
  ChatMessage,
  ChatStatusBubble,
} from '@/components/chat/chat-message';
import { markdownSourceOf } from '@/components/chat/markdown-text';
import { useProductSchema } from '@/components/product-schema-provider';
import { ApprovalCard } from '@/components/studio/approval-card';
import { PlanCard, PlanReloadNotice } from '@/components/studio/plan-card';
import { AssetListCardLoader } from '@/components/studio/asset-list-card-loader';
import { assetListPlacement } from '@/components/studio/asset-list-placement';
import { ChoiceCard } from '@/components/studio/choice-card';
import { QueuedTurns } from '@/components/studio/queued-turns';
import { SessionStepsBar } from '@/components/studio/session-steps-bar';
import { TaskStatusCards } from '@/components/studio/task-status-cards';
import { Composer, type ComposerBoardMode, type ComposerBranch, type ComposerChip, type ComposerMask, type ComposerMention } from '@/components/studio/composer';
import { SessionNotesPanel, sessionNotesText } from '@/components/workspace/session-notes-panel';
import { applyChoiceAction } from '@/components/workspace/harness-choice-action';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAgentEffectiveConfiguration } from '@/hooks/use-agent-effective-configuration';
import { useAgentSkillCatalog } from '@/hooks/use-agent-skill-catalog';
import { useAuth } from '@/hooks/use-auth';
import { useSessionSkillSelection } from '@/hooks/use-session-skill-selection';
import { useGenerationParams } from '@/hooks/use-generation-params';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { stageDomainOf } from '@/hooks/use-stage-domain';
import { useSessionAgentConsent } from '@/hooks/use-session-agent-consent';
import { defaultApprovalMode } from '@/lib/chat/approval-modes';
import { useSessionApproval } from '@/hooks/use-session-approval';
import { useSessionNotes } from '@/hooks/use-session-notes';
import { useSessionPendingApprovals } from '@/hooks/use-session-pending-approvals';
import { useSessionChoices } from '@/hooks/use-session-choices';
import { useSessionHarness } from '@/hooks/use-session-harness';
import { useSessionAssetLists } from '@/hooks/use-session-asset-lists';
import { useSessionPick } from '@/hooks/use-session-pick';
import { useSessionPreferences } from '@/hooks/use-session-preferences';
import { useSessionState } from '@/hooks/use-session-state';
import { useThreeViews } from '@/hooks/use-three-views';
import { asksTurnBackground, useTurnBackground } from '@/hooks/use-turn-background';
import { interleaveGroups } from '@/lib/workspace/three-view';
import { ThreeViewCard } from '@/components/studio/three-view-card';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { useStageViewer } from '@/hooks/use-stage-viewer';
import { useObjectActions } from '@/hooks/use-object-actions';
import { objectOfTile } from '@/lib/workspace/version-object';
import { compareFromGroup } from '@/lib/workspace/compare';
import { candidateSlots, candidateTile } from '@/lib/workspace/candidate-selection';
import { useRestoreSessionModels } from '@/hooks/use-restore-session-models';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { sessionSkillId } from '@/lib/agent-configuration-resource';
import { createAgentConfigurationClient, resolveSessionSkill } from '@/lib/agent-configuration';
import { AgentHarnessError, createAgentHarnessClient } from '@/lib/agent-params';
import { isAutoTitlePending, pollAutoTitle } from '@/lib/chat/session-pages';
import { nextStreamStatus } from '@/lib/chat/stream-status';
import { useCanvasArtboard } from '@/hooks/use-canvas-artboard';
import { useReferenceSize } from '@/hooks/use-reference-size';
import { canvasSnapshotClaim } from '@/lib/canvas/current-object';
import { boardInputSource, boardMode } from '@/lib/chat/board-mode';
import { exportArtboardToBlob } from '@/lib/canvas/export-canvas';
import { flushPendingEdits, settleBoardForTurn } from '@/lib/canvas/pending-edits';
import { inpaintGeometry, paintSubmittedMask } from '@/lib/canvas/inpaint-export';
import { exportAndUploadSnapshot, reportSnapshotFailure, snapshotFilename } from '@/lib/canvas/send-snapshot';
import { uploadWorkspaceInputAsset, uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import { inlineImageFilename, isInlineImage, reportInlineImageFailure, uploadInlineImages } from '@/lib/chat/inline-images';
import { buildChatSelection, type ChatAttachmentClaim, type ChatMentionClaim } from '@/lib/chat/attachments';
import { formatSizeLimit } from '@/lib/size-text';
import { cardFromConsent, cardFromPending, type ApprovalCardModel } from '@/lib/chat/approval-card';
import { handleReferences } from '@/lib/chat/composer-reference';
import { boardMentionCandidate, buildMentionCandidates, mentionsBoard, mentionsForTurn, overridingHandle, mentionCandidates, withBoardMention, type MentionCandidate } from '@/lib/chat/mentions';
import { useComposerDraft } from '@/hooks/use-composer-draft';
import { clearDraft } from '@/lib/chat/composer-draft';
import { useChatScrollFollow } from '@/hooks/use-chat-scroll-follow';
import { useConnectivityStore } from '@/store/connectivity-store';
import { createChatBrowserClient, paidStepFromTaskRegistered, type ChatSemanticEvent, type ChatTurnOptions, type MultiviewTurnRequest } from '@/lib/chat/browser-client';
import { choiceCardPlacement, choiceSelectionAction, heldChoiceRelease, latestReplies, type ChoiceCardModel, type ChoiceSelection } from '@/lib/chat/choice-cards';
import { originalWordsBefore, runChoiceAnswer } from '@/lib/chat/choice-actions';
import { assetListClient } from '@/lib/asset-lists/client';
import { readPastedImages } from '@/lib/chat/composer-input';
import { withSessionSendLock } from '@/lib/chat/send-preparation';
import { InFlightGuard } from '@/lib/chat/in-flight-guard';
import { flushExplicitModel3D, flushExplicitModelImage } from '@/lib/chat/explicit-model';
import { budgetFollowsTasks, followSettledBudget, reloadSessionBudget } from '@/lib/chat/session-budget';
import { budgetAwaitsDecision, budgetIsShown } from '@/lib/chat/budget';
import { PendingApprovalError, type PendingApprovalInput } from '@/lib/chat/pending-approvals';
import { continuePlanOnce } from '@/lib/chat/plan-continue';
import {
  cancelQueuedTurn, enqueueTurn, holdDrainForRead, holdsToRelease, nextDrainStep, pruneQueuedSessions, queueMayDrain, queuedFor,
  readCompleted, takeQueuedTurn, type DrainGate, type QueueBySession,
} from '@/lib/chat/send-queue';
import { chatErrorToastId, requestTurnStop, sendStageRequest, stageRequestStep } from '@/lib/chat/turn-signals';
import { createCoalescer } from '@/lib/chat/coalesce';
import { restoreRemovedMessage } from '@/lib/chat/message-pages';
import { turnActiveRefusal, turnFailureText } from '@/lib/chat/turn-error-text';
import { createUncertainSendStore, newSendKey, sendSignature, sendUnderKey } from '@/lib/chat/send-key';
import { idempotencyRefused } from '@/lib/chat/send-error-codes';
import { activeLocale } from '@/lib/i18n/locale';
import { groupedCount } from '@/lib/workspace/model-stats';
import { releaseAllHolds, useSendQueueStore } from '@/store/send-queue-store';
import { ChatHistorySkeleton } from '@/components/chat/chat-history-skeleton';
import { lastReadStarted, recoverChatSession } from '@/lib/chat/session-recovery';
import { resolveInitialSession, sessionFromAddress, writeSessionToAddress } from '@/lib/chat/session-url';
import { editAttachments, editTarget, regenerateTarget, restoreAttachments, turnPayload } from '@/lib/chat/turn-actions';
import { followTurnStream, type TurnFollowEnd } from '@/lib/chat/turn-follow';
import { messagesBesideStream, readUntilSettled, refreshTasksAfterDone } from '@/lib/chat/turn-settle';
import { refreshSessionTasks } from '@/lib/workspace/task-read';
import { replaceFailureMessage } from '@/lib/chat/turn-replace-error';
import { hasSentWords, sessionTurnRunning } from '@/lib/chat/turn-running';
import { selectedTranscriptText, transcriptClipboard } from '@/lib/chat/transcript-copy';
import { compactionPlacement } from '@/lib/chat/context-compaction';
import { ContextCompactedDivider } from '@/components/studio/context-compacted-divider';
import { useSessionCompactions } from '@/hooks/use-session-context';
import { imageUploadAccept, ImageUploadError, prepareImageUpload } from '@/lib/image-upload';
import { agentUnavailableReason, formatSchemaText, schemaCountMessage, schemaMessage, schemaText } from '@/lib/product-schema';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import { offeringForTurn, skillSizeDefault } from '@/lib/workspace/generation-params';
import { styleLockCapability } from '@/lib/workspace/style-lock-capability';
import { useStyleLock } from '@/hooks/use-style-lock';
import { hydrateSessionTasks } from '@/lib/workspace/task-hydration';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import { handleOfObject } from '@/lib/workspace/object-handle';
import { captureSessionScope, isCurrentSessionScope, sameSessionScope, selectionForSession, type SessionScope } from '@/lib/workspace/session-scope';
import { versionLabel } from '@/lib/workspace/version-label';
import type { ResultTarget } from '@/lib/workspace/result-target';
import { latestToolCallCount } from '@/lib/chat/tool-call-count';
import { cn } from '@/lib/utils';
import { focusWhenUnblocked } from '@/components/ui/focus-handoff';
import {
  useChatStore,
  type ChatImage,
  type ChatMessage as ChatMessageType,
} from '@/store/chat-store';
import { useCanvasStore } from '@/store/canvas-store';
import { maskStrokeCount, sameMaskDraft, useMaskStore } from '@/store/mask-store';
import { CHAT_RAIL_WIDTH, CHAT_WIDTH_MIN, chatWidthMax, clampChatWidth, rightPanelWidth, useStudioStore } from '@/store/studio-store';
import { useViewportValue } from '@/hooks/use-viewport-width';
import { turnModels } from '@/lib/workspace/turn-models';
import { panelWidth } from '@/lib/workspace/panel-tabs';
import { useWorkspaceStore } from '@/store/workspace-store';
import { isRunning, statusBubbleShown } from '@/lib/workspace/task-progress';
import { useSessionContextStore } from '@/store/session-context-store';
import { useProjectStore } from '@/store/project-store';
import { branchConversation, branchCopiedCurrentState, branchUnavailableReason } from '@/lib/chat/session-branch';
import { MultiviewUploadPanel } from '@/components/studio/multiview-upload-panel';
import { buildMultiviewRequest, chosenViewLabels, multiviewEntryOffered, multiviewLockNote, multiviewOffering } from '@/lib/workspace/multiview-upload';
import { referenceLimit } from '@/lib/workspace/reference-limit';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { sameData } from '@/lib/core/keep-unchanged';

const RESIZE_KEY_STEP = 16;
/** Shift with an arrow moves the divider this far: the range reaches half the window now (#695). */
const RESIZE_KEY_STEP_LARGE = 64;
const SCROLL_SETTLE_MILLISECONDS = 400;
/** A stored turn that cannot be read back must not keep the live buffer and the stop button forever. */
const STREAM_SETTLE_FALLBACK_MILLISECONDS = 5000;
const APPROVAL_CLOCK_MILLISECONDS = 30_000;
// A late approval whose task id is not in the answer yet: look the task up again after these delays.
const LATE_TASK_HYDRATE_MILLISECONDS = [1_500, 4_000, 8_000];
// The server writes a late task's result message when its sweep (every 2 s) sees the task end.
const LATE_RESULT_RELOAD_MILLISECONDS = [2_500, 5_000];
// #597: the server sends a turn's `done` just before it marks the turn ended; a read in between still calls the
// reply pending, so it is read again at these delays until it is not.
const SETTLE_REREAD_MILLISECONDS = [400, 1_200, 3_000, 6_000];

/** Text selection on or off for the whole page; Safari reads only the -webkit- property. */
function setPageSelectable(selectable: boolean) {
  const value = selectable ? '' : 'none';
  document.body.style.userSelect = value;
  document.body.style.setProperty('-webkit-user-select', value);
}

/**
 * The divider on the chat column's right edge. A drag shows its width through `onPreview` once per frame and
 * stores it with `onCommit` only when it ends (#695); arrows step 16px (64px with Shift), Home and End go to
 * the narrowest and widest. `max` is the widest the window allows now.
 */
export function ResizeHandle({ width, max, label, onPreview, onCommit }: {
  width: number;
  max: number;
  label: string;
  onPreview: (width: number | null) => void;
  onCommit: (width: number) => void;
}) {
  const drag = useRef<{ startX: number; startWidth: number; last: number | null; frame: number } | null>(null);
  const endDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    const current = drag.current;
    drag.current = null;
    setPageSelectable(true);
    if (current) {
      if (current.frame) cancelAnimationFrame(current.frame);
      if (current.last !== null) onCommit(current.last);
      onPreview(null);
    }
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  };
  // Leaving the page mid-drag must not leave text selection switched off, nor a width shown that was never stored.
  useEffect(() => {
    const state = drag;
    return () => {
      setPageSelectable(true);
      if (state.current?.frame) cancelAnimationFrame(state.current.frame);
      if (state.current) onPreview(null);
      state.current = null;
    };
  }, [onPreview]);
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuemin={CHAT_WIDTH_MIN}
      aria-valuemax={max}
      aria-valuenow={width}
      tabIndex={0}
      data-testid="chat-resize-handle"
      onPointerDown={(event) => {
        // A drag must not start a text selection: the browser would highlight the stage and its title.
        event.preventDefault();
        event.currentTarget.setPointerCapture(event.pointerId);
        drag.current = { startX: event.clientX, startWidth: width, last: null, frame: 0 };
        setPageSelectable(false);
      }}
      onPointerMove={(event) => {
        const current = drag.current;
        if (!current) return;
        current.last = clampChatWidth(current.startWidth + event.clientX - current.startX, max);
        if (current.frame) return;
        current.frame = requestAnimationFrame(() => {
          current.frame = 0;
          if (drag.current === current && current.last !== null) onPreview(current.last);
        });
      }}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onLostPointerCapture={endDrag}
      onKeyDown={(event) => {
        const step = event.shiftKey ? RESIZE_KEY_STEP_LARGE : RESIZE_KEY_STEP;
        const next = event.key === 'ArrowLeft' ? width - step
          : event.key === 'ArrowRight' ? width + step
            : event.key === 'Home' ? CHAT_WIDTH_MIN
              : event.key === 'End' ? max
                : null;
        if (next === null) return;
        event.preventDefault();
        onCommit(clampChatWidth(next, max));
      }}
      className="absolute inset-y-0 -right-1 z-sticky w-2 cursor-col-resize select-none outline-none after:absolute after:inset-y-0 after:left-1/2 after:w-px after:bg-transparent hover:after:bg-primary/60 focus-visible:after:bg-primary"
    />
  );
}

/** 对话列：会话设定、消息与批准卡片、输入区。折叠成窄条时事件流照常接收。 */

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

/**
 * #241: the generated 2D pictures of one message as claims for a weak preference signal. Only pictures the server
 * gave a Task are sent; the server re-verifies every one for the owner and the session.
 */
function generatedPictureClaims(message: ChatMessageType | undefined): ChatAttachmentClaim[] {
  if (!message) return [];
  return (message.attachments ?? []).flatMap((attachment): ChatAttachmentClaim[] => {
    if (attachment.kind !== 'image' || !attachment.taskId) return [];
    return [{
      kind: 'image',
      ...(attachment.taskId ? { taskId: attachment.taskId } : {}),
      ...(attachment.assetId ? { assetId: attachment.assetId } : {}),
      ...(attachment.url ? { url: attachment.url } : {}),
      ...(attachment.captureId ? { captureId: attachment.captureId } : {}),
    }];
  });
}

export function ChatColumn() {
  const { schema } = useProductSchema();
  const chatClient = useMemo(() => createChatBrowserClient(schema), [schema]);
  const harness = useMemo(
    () =>
      createAgentHarnessClient({
        routes: {
          params: schema.runtime.http.routes.agent_params,
          consent: schema.runtime.http.routes.agent_consent,
        },
      }),
    [schema],
  );
  const configurationClient = useMemo(() => createAgentConfigurationClient(schema), [schema]);
  const assetListsClient = useMemo(() => assetListClient(schema), [schema]);
  const chatWidth = useStudioStore((state) => state.chatWidth);
  const collapsed = useStudioStore((state) => state.chatCollapsed);
  const setChatWidth = useStudioStore((state) => state.setChatWidth);
  const setCollapsed = useStudioStore((state) => state.setChatCollapsed);
  // #695/#696: the stored width is shown up to what the window allows now, never written back, so it returns
  // when the window is wide again; a drag in progress is shown before it is stored.
  const panelCollapsed = useStudioStore((state) => state.panelCollapsed);
  // The cap, not the width, is what the column reads: dragging the window renders the column only when it changes.
  const rightWidth = rightPanelWidth(panelWidth(schema), panelCollapsed);
  const chatWidthCap = useViewportValue((viewportWidth) => chatWidthMax(viewportWidth, rightWidth));
  const [dragWidth, setDragWidth] = useState<number | null>(null);
  const shownChatWidth = clampChatWidth(dragWidth ?? chatWidth, chatWidthCap);
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
  const [skillRefreshVersion, setSkillRefreshVersion] = useState(0);
  const preflightRef = useRef<{ sessionId: string; controller: AbortController } | null>(null);
  const [preparingSession, setPreparingSession] = useState<string | null>(null);
  const { modelChoices, isLoading: modelsLoading, error: modelsError } =
    useWorkspaceOfferings(schema, stageDomain);
  // Several uploaded pictures may be views of one object when a 3D Offering takes them (found by its Schema parameter).
  const { offeringsByCapability: model3dOfferings } = useWorkspaceOfferings(schema, 'model-3d');
  const picked3dModel = useWorkspaceStore((state) => state.modelByDomain['model-3d']);
  const multiviewOfferings = useMemo(() => Object.values(model3dOfferings ?? {}).flat(), [model3dOfferings]);
  const multiviewTarget = useMemo(
    () => multiviewOffering(multiviewOfferings, picked3dModel ?? undefined),
    [multiviewOfferings, picked3dModel],
  );
  // #373: the panel makes its target even when the 3D model the person picked takes no views; the request names the
  // target and the server leaves the session's pick as it is, so the panel says which model it uses and why.
  const multiviewNote = useMemo(
    () => (multiviewTarget ? multiviewLockNote(multiviewOfferings, multiviewTarget, picked3dModel) : null),
    [multiviewOfferings, multiviewTarget, picked3dModel],
  );
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
    settleStreaming,
    applyOlderMessagePage,
    updateSessionTitle,
    initStreaming,
    setAssistantDbId,
    appendStreamText,
    addStreamImages,
    addStreamPaidStep,
    updateStreamGenState,
    finishStreaming,
    rewindStreaming,
  } = useChatStore();

  const approval = useSessionApproval(
    chatClient,
    activeSessionId,
    defaultApprovalMode(schema),
  );
  const refreshApproval = approval.refresh;
  const sessionNotes = useSessionNotes(chatClient, activeSessionId);
  const refreshNotes = sessionNotes.refresh;
  const notesText = useMemo(() => sessionNotesText(schema), [schema]);
  const sessionPick = useSessionPick(chatClient, activeSessionId);
  const chooseCandidate = sessionPick.choose;
  const sessionPreferences = useSessionPreferences(chatClient, activeSessionId);
  const recordPreferenceSignal = sessionPreferences.signal;
  const refreshSessionPreferences = sessionPreferences.refresh;
  const rejectedAssets = useMemo(() => {
    const rejected = new Set<string>();
    for (const preference of sessionPreferences.preferences) {
      if (preference.kind !== 'rejected' || !preference.active) continue;
      if (preference.assetId) rejected.add(preference.assetId);
      if (preference.handle) rejected.add(preference.handle);
    }
    return rejected;
  }, [sessionPreferences.preferences]);
  const pending = useSessionPendingApprovals(activeSessionId);
  const sessionChoices = useSessionChoices(activeSessionId);
  const refreshChoices = sessionChoices.refresh;
  // #327: the production lists proposed in this session, shown as cards in the conversation.
  const sessionAssetLists = useSessionAssetLists(activeSessionId ?? null);
  const [answeringChoice, setAnsweringChoice] = useState<string | null>(null);
  const versions = useSessionVersions();
  const { slotOf } = useStageViewer();
  const { openOnStage } = useObjectActions();
  // #242: a plan candidate group compares its finished tasks in the existing
  // 2/4-pane view; selecting one opens that model or picture on the stage.
  const compareCandidates = useCallback((taskIds: readonly string[]) => {
    const slots = candidateSlots(versions.tiles, taskIds, (tile) => slotOf(tile));
    if (slots.length >= 2) useStudioStore.getState().startCompare(compareFromGroup(slots));
  }, [slotOf, versions.tiles]);
  const selectCandidate = useCallback((taskId: string) => {
    const tile = candidateTile(versions.tiles, taskId);
    const object = tile && activeSessionId ? objectOfTile(tile, activeSessionId) : null;
    if (object) openOnStage(object);
  }, [activeSessionId, openOnStage, versions.tiles]);

  const effective = useAgentEffectiveConfiguration(schema, activeSessionId, stageDomain, selectedGenerationModel, skillRefreshVersion);
  const skillCatalog = useAgentSkillCatalog(schema, effective.data?.revision, skillRefreshVersion);
  const { user } = useAuth();
  // #374: a direction-card option may save the function selection before its answer is sent. This is the 功能 panel's own
  // session Harness state (one SWR key, so the panel and the summary above the input follow the save) and its CAS save.
  const sessionHarness = useSessionHarness(activeSessionId, user?.id ?? '');
  const sessionHarnessRef = useRef(sessionHarness);
  sessionHarnessRef.current = sessionHarness;
  const { data: supportingModels, error: supportingModelsError } = useSWR(
    user?.id && activeSessionId && modelChoices.length
      ? ['skill-supporting-models', schema.revision, user.id, activeSessionId, stageDomain, skillRefreshVersion,
        effective.data?.revision, modelChoices.map((model) => [model.model_id, model.model_name])] : null,
    async () => {
      const results = await Promise.all(modelChoices.map(async (model) => ({
        name: model.model_name,
        configuration: await configurationClient.effective(stageDomain, model.model_id, activeSessionId),
      })));
      const models: Record<string, string[]> = {};
      for (const result of results) {
        for (const skill of result.configuration.skills) {
          const names = models[skill.id] ??= [];
          if (result.name && !names.includes(result.name)) names.push(result.name);
        }
      }
      return models;
    },
    { revalidateOnFocus: false, shouldRetryOnError: false },
  );
  const { selection: skillBySession, selectionRef: skillBySessionRef, select: selectSkill } = useSessionSkillSelection(
    user?.id ?? null, activeSessionId,
    !skillCatalog.loading && !skillCatalog.failed ? skillCatalog.catalog?.skills ?? null : null,
  );
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
  // 芯片上的 ✕ 取消选中（#557）：这个对象不再是默认目标，消息不带它，也不带画板快照。
  // 舞台上再选中对象（包括再点一次同一个）就恢复选中。
  const [deselectedObject, setDeselectedObject] = useSessionState<string | null>(null);
  useEffect(() => {
    setDeselectedObject(null);
  }, [currentObject, setDeselectedObject]);
  // 「从这里继续」之后、发出之前：下一个版本接在这个版本后面（芯片可取消）。
  const [branchFrom, setBranchFrom] = useSessionState<{ key: string; label: string } | null>(null);
  // #644 「以画板为主」: this message only, off by default and off again once it has left the box. A selection or
  // mask on the board, `@画布` or 「发送到对话」 from the board turns it on (boardMode below).
  const [boardModeChosen, setBoardModeChosen] = useSessionState(false);
  const boardModeChosenRef = useRef(false);
  boardModeChosenRef.current = boardModeChosen;
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
    droppedNote: useCallback((count: number) => schemaCountMessage(schema, 'ui.studio.composer.draft.local_dropped', count, { count }), [schema]),
  });
  // Back from offline: the event stream of the open session is joined again.
  const reconnectTick = useConnectivityStore((state) => state.reconnectTick);
  // #134: bumped when the server starts a turn on its own (an approved plan runs), so the
  // session recovery reads the newest messages again and attaches to that turn's stream.
  const [serverTurnTick, setServerTurnTick] = useState(0);
  const [uploading, setUploading] = useSessionState(false);
  // Synchronous send guard for pictures that are still being read or prepared; state alone is one render late.
  const attachmentReads = useRef<PendingAttachmentReads>({ scope: captureSessionScope(), count: 0 });
  // A draft replaced by send, edit, clear or a prefill in the same visit: a read started against the old draft is spent.
  const draftEpoch = useRef(0);
  // Messages sent while a turn runs wait here ("排队中") and go out when it ends. Kept per
  // session, so switching away and back does not lose them.
  // #665: the queue is kept in this browser (a reload does not lose a queued message) and leaves with its session.
  const queue: QueueBySession = useSendQueueStore((state) => state.queue);
  const setQueue = useSendQueueStore((state) => state.update);
  const queuedTurns = queuedFor(queue, activeSessionId);
  const [drainTick, setDrainTick] = useState(0);
  // #665: the sessions whose drain waits for a read started after another tab's claim or send (see DrainGate, numbered
  // by lastReadStarted), and the read a waiting drain last asked for. The holds this tab took are kept per session in
  // the send queue store.
  const [drainGate, setDrainGate] = useState<DrainGate>({});
  const forcedRead = useRef<number | null>(null);
  // #154: the session's saved style lock and this turn's exemption. The exemption never outlives its turn.
  const styleLock = useStyleLock(chatClient, activeSessionId);
  const [styleLockSkipped, setStyleLockSkipped] = useState(false);
  useEffect(() => { setStyleLockSkipped(false); }, [activeSessionId]);
  // #665: the session whose queued turn this page is claiming or sending, or null.
  const drainingRef = useRef<string | null>(null);
  const startedTurnsRef = useRef(0);
  // #665: per session, the last send whose answer never came, until a send of another request or an answered one
  // replaces it; kept in the tab's sessionStorage so a reload keeps it.
  const [uncertainSends] = useState(() => createUncertainSendStore(() => (typeof window === 'undefined' ? undefined : window.sessionStorage)));
  // The turn whose start the server has not answered yet: the box still holds its words.
  const startingRef = useRef<{ sessionId: string; text: string } | null>(null);
  const [decisionError, setDecisionError] = useState<Record<string, string>>({});
  // #103: the plan card cannot be shown or trusted after a failed session re-read.
  const [planReloadFailure, setPlanReloadFailure] = useState<{ sessionId: string; message: string } | null>(null);
  const [planReloadingSession, setPlanReloadingSession] = useState<string | null>(null);
  const planReloadError = planReloadFailure?.sessionId === activeSessionId ? planReloadFailure.message : null;
  const planReloading = planReloadingSession === activeSessionId;
  // The next turn's settings and estimate. A turn that carries a picture (the
  // object on the stage, an upload or a pinned canvas image) runs the Offering
  // that takes references, so that is the one priced.
  const objectDeselected = sessionSelection != null && deselectedObject === objectKey(sessionSelection);
  const hasReference = attachedImages.length > 0 || hasCanvasReference || (sessionSelection?.kind === 'image' && !objectDeselected);
  const imageGeneration = useGenerationParams('image', activeSessionId, hasReference);
  const modelGeneration = useGenerationParams('model-3d', activeSessionId, hasReference);
  const generation = stageDomain === 'image' ? imageGeneration : modelGeneration;
  // #329 follow-up: when the 3D model the Agent would use takes fewer pictures than are attached (Meshy 7.1 and
  // T2.5 image to 3D take one), say so before sending, whenever the session's Agent scope allows 3D (the Agent may
  // make a 3D model from any stage). The note says it is about 3D; the pictures stay attached, nothing is blocked
  // and the model is not switched.
  // #86: while the multiview way is offered for these pictures, the note says both ways: a plain send cuts,
  // confirming the views makes the model from all of them with the multiview model.
  // #721: the offer is the 用作多视角 entry beside the attachments, not a question above the box. The views panel opens
  // only after the person presses it, for the pictures attached then; other pictures close it again.
  const multiviewOffered = multiviewEntryOffered(multiviewTarget, attachedImages.length, Boolean(editing));
  const [multiviewOpenFor, setMultiviewOpenFor] = useState<string | null>(null);
  const multiviewSignature = attachedImages.join('\n');
  const multiviewOpen = multiviewOffered && multiviewOpenFor === multiviewSignature;
  const composerReferenceLimit = modelGeneration.allowed
    ? referenceLimit(
      modelGeneration.nextTurn.offering,
      attachedImages.length + (hasCanvasReference ? 1 : 0),
      multiviewOffered && !hasCanvasReference ? multiviewTarget?.offering.model_name ?? '' : '',
    )
    : null;
  // #154: the label shows while the session has a saved lock; its notice follows this turn's exemption.
  const styleLockActive = styleLock.lock.enabled && (styleLock.lock.text.trim() !== '' || styleLock.lock.references.length > 0);
  // Style-lock references make the turn a reference turn, so this is the Offering the server prepares.
  const styleLockOffering = offeringForTurn(imageGeneration.offerings.filter((item) => item.available), true);
  const styleLockNoticeKind = styleLockActive && !styleLockSkipped
    ? styleLockCapability(styleLockOffering, styleLock.lock.references.length + attachedImages.length + (hasCanvasReference ? 1 : 0))
    : null;
  const styleLockNotice = styleLockNoticeKind === null ? null : {
    kind: styleLockNoticeKind === 'model_no_reference' ? 'unavailable' as const : 'trimmed' as const,
    text: schemaText(schema, styleLockNoticeKind === 'model_no_reference'
      ? 'ui.unavailable.reason.style_lock.model_no_reference'
      : 'ui.studio.style_lock.references_trimmed'),
  };
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
  // #410: the plan whose continuation turn the server is starting; one at a time.
  const [continueBusy, setContinueBusy] = useState<string | null>(null);
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
            pixels: groupedCount(schema.runtime.frontend.image_upload.maximum_pixels, activeLocale(schema)),
          }));
        }
      }
      if (!isCurrentSessionScope(scope) || draftEpoch.current !== epoch) return;
      if (prepared.length > 0) {
        // The same picture twice (pasted again, or a file and its copy) is attached once.
        const next = [...new Set([...attachedImages, ...prepared])].slice(0, maximumImages);
        setAttachedImages(next);
      }
    } finally {
      if (endAttachmentRead(attachmentReads, scope)) setUploading(false);
    }
  }, [activeSessionId, attachedImages, hasCanvasReference, maximumImages, maximumImageBytes, schema, setAttachedImages, setUploading]);

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
      rejectFailed: schemaText(schema, 'ui.chat.image.reject_failed'),
    }),
    [schema],
  );

  // 消费"发送到对话"排队的附件：合并进附件列表并清空队列。
  useEffect(() => {
    if (pendingChatAttachments.length === 0) return;
    const queued = consumeChatAttachments();
    if (queued.length === 0) return;
    const next = [...attachedImages, ...queued].slice(0, maximumImages);
    setAttachedImages(next);
  }, [pendingChatAttachments, attachedImages, consumeChatAttachments, maximumImages, setAttachedImages]);

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
        const session = await chatClient.createSession('', useProjectStore.getState().activeProjectId ?? '');
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
  // #403: a stream joined after a reload has no sent words of its own (the stored user message is already in the
  // list), so an empty one is not shown as a blank message.
  const optimisticUser = useMemo((): ChatMessageType | null => (streamUserMessage && hasSentWords(streamUserMessage) ? {
    id: schema.chat.frontend.optimistic_user_message_id,
    role: 'user',
    content: streamUserMessage.content,
    images: streamUserMessage.images,
    createdAt: Date.now(),
  } : null), [schema, streamUserMessage]);
  const displayMessages = useMemo((): ChatMessageType[] => {
    // #597: only this turn's own pending reply gives way to the live buffer; an earlier reply still marked
    // pending (read just before its turn ended) stays in the list with its pictures.
    const dbMessages = messagesBesideStream(messages, streamBuf);
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

  const coalesceReads = useRef(createCoalescer());
  const replyStalled = useMemo(() => ({
    text: schemaText(schema, 'ui.chat.toast.reply_stalled'),
    retry: schemaText(schema, 'ui.chat.toast.reply_stalled.retry'),
  }), [schema]);
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
      // #275: a status event follows each round's tool calls being saved on the assistant message, so
      // the count in the saved message is read again then; it only ever rises during a reply.
      // #665: a replayed stream repeats every status; the reads are coalesced per session.
      if (event.type === 'status') {
        coalesceReads.current(`tool-calls:${sessionId}`, () => chatClient
          .listMessagePage(sessionId, { limit: 4 })
          .then((page) => {
            const count = latestToolCallCount(page.messages);
            if (count !== undefined) useChatStore.getState().observeToolCallCount(sessionId, count);
          }));
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
          // #665: the same error replayed on a reattach updates its toast instead of stacking another one.
          toast.error(event.content, { id: chatErrorToastId(sessionId, buffer?.assistantDbId ?? '', event.content) });
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
          { const step = paidStepFromTaskRegistered(event);
            if (step) addStreamPaidStep(sessionId, step);
          }
          // 新提交的付费步骤会增加本会话的累计估算积分，向服务端取最新值（#665：重放时合并为一次读取）。
          coalesceReads.current(`approval:${sessionId}`, async () => { await refreshApproval(); });
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
    void serverTurnTick;
    setHistoryLoading(!(activeSessionId in useChatStore.getState().messagesBySession));
    const stalledToast = `reply-stalled:${activeSessionId}`;
    const stop = recoverChatSession({
      sessionId: activeSessionId,
      client: chatClient,
      pageSize: messagePageSize,
      connect: connectEventSource,
      pollMilliseconds: schema.chat.frontend.fallback_poll_milliseconds,
      onLoading: setHistoryLoading,
      onLoadError: () => toast.error(ui.loadFailed),
      onRead: (started) => setDrainGate((gate) => readCompleted(gate, activeSessionId, started)),
      // #665: a reply that stays pending is no longer read on its own; say so and offer to read it again.
      onGaveUp: () => toast.message(replyStalled.text, {
        id: stalledToast,
        duration: Infinity,
        action: { label: replyStalled.retry, onClick: () => setServerTurnTick((tick) => tick + 1) },
      }),
    });
    return () => {
      stop();
      toast.dismiss(stalledToast);
    };
  }, [
    activeSessionId,
    reconnectTick,
    serverTurnTick,
    chatClient,
    connectEventSource,
    messagePageSize,
    schema.chat.frontend.fallback_poll_milliseconds,
    ui.loadFailed,
    replyStalled,
  ]);

  // #665: the queues of sessions this account no longer has (deleted in another tab or device, or another account's)
  // leave the browser once the session list is known.
  useEffect(() => {
    if (!sessionsLoaded) return;
    const known = new Set(useChatStore.getState().sessions.map((session) => session.id));
    const controller = new AbortController();
    void pruneQueuedSessions(
      Object.keys(useSendQueueStore.getState().queue),
      known,
      (sessionId, signal) => chatClient.getSession(sessionId, signal),
      (sessionId) => useSendQueueStore.getState().forgetSession(sessionId),
      controller.signal,
    );
    return () => controller.abort();
  }, [sessionsLoaded, chatClient]);

  // #665: another tab claimed a queued turn of this session, or the turn it sent ended: this tab's messages cannot
  // show that yet, so its drain waits for a read started from now on (a read already in flight does not count).
  const foreignChanges = useSendQueueStore((state) => (activeSessionId ? state.foreignChanges[activeSessionId] ?? 0 : 0));
  useEffect(() => {
    if (!activeSessionId || foreignChanges === 0) return;
    const next = lastReadStarted() + 1;
    setDrainGate((gate) => holdDrainForRead(gate, activeSessionId, next));
  }, [activeSessionId, foreignChanges]);

  // #665: the session's turn as this page sees it; a stalled-reply notice goes away once the reply is settled.
  const activeTurnRunning = sessionTurnRunning(activeSessionId ? messagesBySession[activeSessionId] : undefined, isStreaming);
  useEffect(() => {
    if (activeSessionId && !activeTurnRunning) toast.dismiss(`reply-stalled:${activeSessionId}`);
  }, [activeSessionId, activeTurnRunning]);

  // An expired card stays in the chat (with 重新发起) but no longer counts as pending.
  const pendingCount = pending.pendingCount + (consentAwaiting && consent.pending ? 1 : 0);
  // #403/#410: a step of an approved plan that has no task can still get one while the session's turn runs (the
  // running projection of the messages, or a stream this browser follows). An open approval card is deliberately
  // not counted: the server projects every step's standing and only counts a turn as running, so a plan whose turn
  // ended reads "not run" instead of waiting for ever.
  const planStepsMayStillRun = sessionTurnRunning(messagesBySession[activeSessionId ?? ''], isStreaming);
  const lastDisplayed = displayMessages[displayMessages.length - 1];
  // #546: the running tasks are drawn under the newest assistant reply (after its text, above its credit line).
  const runningTaskCount = useWorkspaceStore((state) => state.trackedTasks.filter((task) => task.sessionId === activeSessionId && isRunning(task)).length);
  // One element for every render, so the memoized messages keep their props (#291).
  const runningTaskBlock = useMemo(() => <TaskStatusCards part="running" />, []);
  const progressMessageId = runningTaskCount > 0 ? [...displayMessages].reverse().find((message) => message.role === 'assistant')?.id ?? null : null;
  const {
    containerRef: messagesRef, endRef: messagesEndRef, showLatest, jumpToLatest, onScroll: onMessagesScroll,
  } = useChatScrollFollow({
    // The newest message, not the count: earlier pages put in front must not move the reader.
    // #177: streamed text and pictures grow the last message without changing its id; a reader at the
    // end must keep seeing it, otherwise the whole drift is corrected at once when the turn settles.
    changeKey: [
      lastDisplayed?.id ?? '', threeViews.length, pendingCount, collapsed, queuedTurns.length,
      streamBuf?.textContent.length ?? 0, streamBuf?.images.length ?? 0,
      // #546: the task block sits in the list now, and a task starting or ending changes its height.
      runningTaskCount,
    ].join('|'),
    // #177: the status line appears and goes away without animation; only real content is followed smoothly.
    pinKey: statusText,
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
      // `onSent` runs once the turn left the box: queued, or taken by the server. A refused or failed send never calls it.
      extra?: { choiceAnswer?: ChatTurnOptions['choiceAnswer']; onSent?: () => void; queued?: boolean; multiview?: MultiviewTurnRequest; multiviewViews?: string[]; domainModels?: Record<string, string>; styleLockSkipped?: boolean; selectionMask?: { maskAssetId: string }; idempotencyKey?: string; queuedId?: string; canvasTurn?: boolean },
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
      // #678: a model confirmed for this one turn (转 3D without a pick) rides along as that domain's model; no lock.
      // With a 3D model on the stage (只用正面转 3D) it is the stage domain's model and becomes the turn's model;
      // a queued turn keeps it and is read the same way against the stage it goes out under. It never becomes
      // `selectedModel`: the checks below compare the person's own pick.
      const { model: turnModel, domainModels: otherDomainModels } = turnModels(domain, workspace.modelByDomain, extra?.domainModels);
      const oneTurnModels = extra?.domainModels;
      const sessionId = activeSessionId;
      // What the person sees and selects travels with the turn as claims; the
      // server verifies every ID for this owner and session before use.
      let claims: ChatAttachmentClaim[] = reuseClaims ?? images.flatMap((url, index): ChatAttachmentClaim[] => {
        const source = workspace.attachmentSources[url];
        return source ? [{ kind: 'image', imageSlot: index + 1, ...source }] : [];
      });
      const canvasState = useCanvasStore.getState();
      // #557: the selected object is the default target. A deselected object, or a handle written in the text
      // (which wins over the selection; the chip says so), leaves the object and the artboard out of the turn.
      const skipped = sessionObject != null
        && (deselectedObjectRef.current === objectKey(sessionObject) || overridingHandle(written, chipHandleRef.current) !== '');
      // #644: the board is the turn's source only on a canvas turn (以画板为主). Then the dashed artboard is
      // rasterised at the chosen output pixels like every other delivery (export, inpaint, merge) and uploaded as
      // an input asset. Every other turn works on the conversation (the current object's own picture, the
      // attached pictures and the outputs) and the board takes no part: #610/#631 were the board leaking in.
      let canvasSelection: ChatAttachmentClaim[] | null = null;
      // Writing @画布 asks for the artboard itself: the same rasteriser, whatever the current object is.
      const namesBoard = reuseImages === undefined && mentionsBoard(written, mentionsRef.current);
      // #244: a painted mask belongs to the artboard snapshot and forces that snapshot to be the turn's source.
      const maskDraftNow = reuseImages === undefined && queuedSelection === undefined ? useMaskStore.getState().draft : null;
      const maskStrokesNow = maskDraftNow ? maskStrokeCount(maskDraftNow, sessionId) : 0;
      const withMask = maskStrokesNow > 0 && artboard !== null;
      // The toggle belongs to the box: a stage action, a card answer or a replayed round never picks it up.
      const fromBox = reuseImages === undefined && queuedSelection === undefined && !choiceAnswer;
      // #709: the card option 以画板为主继续 (continue_on_canvas) is the person's explicit click: that one answer goes out
      // as a canvas turn. No other path asks for one.
      const canvasTurnAsked = extra?.canvasTurn === true;
      const boardTurn = boardMode({
        chosen: (fromBox && boardModeChosenRef.current) || canvasTurnAsked, artboard: artboard !== null, maskSteps: maskStrokesNow, namesBoard,
      }).on;
      if (!queuedSelection && boardTurn && artboard) {
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
      // A canvas turn the card asked for never goes out as a plain turn (no board frame, or a queued selection).
      if (canvasTurnAsked && canvasSelection === null) return;
      // #244: the mask is painted on the same artboard at the same delivery pixels, so a stroke lands on the
      // same pixel of the snapshot and of the mask. Upload it as an input asset; the server measures and freezes it.
      let selectionMask = extra?.selectionMask;
      if (withMask && !selectionMask && artboard) {
        const geometry = inpaintGeometry(artboard, deliveryPixels);
        const maskCanvas = document.createElement('canvas');
        maskCanvas.width = geometry.size.width;
        maskCanvas.height = geometry.size.height;
        const maskContext = maskCanvas.getContext('2d');
        if (!maskContext || !maskDraftNow) return;
        paintSubmittedMask(maskContext, geometry, maskDraftNow.strokes, '#000000');
        let maskAssetId = '';
        try {
          await exportAndUploadSnapshot({
            exportPicture: () => new Promise<Blob>((resolve, reject) => {
              maskCanvas.toBlob((picture) => picture ? resolve(picture) : reject(new Error('mask toBlob returned null')), 'image/png');
            }),
            upload: async (picture) => {
              const uploaded = await uploadWorkspaceInputAssetResult(schema, picture,
                `${schema.canvas.frontend.export_filename_fallback}-mask.png`);
              maskAssetId = uploaded.id;
              return uploaded.url;
            },
            maximumBytes: schema.runtime.input_assets.upload_max_bytes,
          });
        } catch (error) {
          // Nothing is sent; the mask draft stays on the stage, like a failed snapshot.
          const text = reportSnapshotFailure(schema, 'chat-send', error);
          if (isCurrentSessionScope(scope)) toast.error(text);
          return;
        }
        if (!isCurrentSessionScope(scope) || maskAssetId === '') return;
        selectionMask = { maskAssetId };
      }
      // #266: a turn that sends the artboard says so; the server binds that snapshot as the turn's source
      // whatever the Agent names. A queued turn keeps the source it was queued with. #644: only a canvas turn
      // sends a snapshot, so every other turn goes out without an input source and the server keeps the board out.
      const inputSource = queuedSelection !== undefined
        ? queuedInputSource
        : boardInputSource(canvasSelection !== null, schema.chat.context.canvas_input_source);
      const stageSelection = queuedSelection ?? canvasSelection ?? (skipped ? [] : buildChatSelection({
        sessionId,
        currentObject: sessionObject,
        nodes: canvasState.getNodes(sessionId),
        selectedNodeIds: canvasState.selectedNodeIds,
        resolveUrl: proxyAssetUrl,
      }));
      // `@` references to pictures that have no handle yet travel as claims; the server verifies them, gives
      // the pictures handles and rewrites the tokens, so the text goes out as written (queued turns too).
      // #697: attached pictures carry no purpose, so the text goes out as written (no 「附图用途」 line).
      const text = written;
      const selection = stageSelection;
      const mentions = queuedMentions ?? (reuseImages !== undefined ? [] : mentionsForTurn(text, mentionsRef.current, pickedMentions(scope)));
      // #593: a picture-input Skill (three-view, four-view) is sent with or without a picture; without one the
      // Agent draws a source first, as the Skill says. No send is refused for a missing picture.
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
      // Pictures the person labelled as views of one object: their asset IDs exist only after the upload above.
      let multiview = extra?.multiview;
      if (extra?.multiviewViews) {
        const routes = schema.runtime.http.routes;
        multiview = (multiviewTarget
          ? buildMultiviewRequest(routes, multiviewTarget.offering.model_id, images, extra.multiviewViews)
          : null) ?? undefined;
        if (!multiview) {
          toast.error(schemaText(schema, 'ui.studio.multiview_upload.invalid'));
          return;
        }
      }
      // The draft's picks are spent with the draft (a queued turn carries its claims already).
      if (queuedMentions === undefined && reuseImages === undefined) pickedMentionsRef.current = { scope, picks: [] };
      if (turnRunning) {
        setQueue((current) => enqueueTurn(current, sessionId, {
          id: `queued-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
          idempotencyKey: newSendKey(),
          text, images, claims, selection, mentions,
          ...(inputSource ? { inputSource } : {}),
          ...(selectionMask ? { selectionMask } : {}),
          ...(multiview ? { multiview } : {}),
          ...(oneTurnModels ? { domainModels: oneTurnModels } : {}),
          ...(styleLockSkipped ? { styleLockSkipped: true } : {}),
        }));
        useWorkspaceStore.getState().forgetAttachmentSources(images);
        // #244: the queued turn froze the uploaded mask; the stage draft is spent with it.
        if (withMask && maskDraftNow && sameMaskDraft(useMaskStore.getState().draft, maskDraftNow)) {
          useMaskStore.getState().clearDraft();
        }
        // The queued turn carries the exemption; the box starts the next message without it.
        setStyleLockSkipped(false);
        // The draft is spent with the turn, even when the person has moved to another session meanwhile
        // (the scoped setters below are then no-ops and could not empty the stored draft). Only a send
        // accepted in this visit spends this visit's reads; a visit begun since has its own draft.
        if (isCurrentSessionScope(scope)) draftEpoch.current += 1;
        clearDraft(sessionId);
        if (isCurrentSessionScope(scope)) {
          setInput('');
          setAttachedImages([]);
          setCanvasReference(null);
          setBranchFrom(null);
          // #644: 以画板为主 is this message's; the next one starts off again.
          setBoardModeChosen(false);
        }
        // Queued is accepted: the box and the draft are already spent.
        extra?.onSent?.();
        return;
      }
      const skillId = choiceAnswer ? null : sessionSkillId(skillBySession, sessionId);
      const selectedSkill = skillCatalog.catalog?.skills.find(item => item.id === skillId);
      if (skillId) {
        if (preflightRef.current?.sessionId === sessionId) return;
        const request = { sessionId, controller: new AbortController() };
        preflightRef.current = request; setPreparingSession(sessionId);
        try {
          const current = await configurationClient.effective(domain, turnModel, sessionId, request.controller.signal);
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
      // The lock is released only after the turn's own cleanup: a queued message that came due meanwhile waits for
      // the release (the drain below re-checks on it) instead of being refused by the lock and put back.
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
          // #709: a turn that is not a canvas turn waits, at most canvas.frontend.send_save_wait_milliseconds, for the
          // board's pending saves (a picture dragged in just before Enter, brush bitmaps still uploading), so the Agent's
          // fallback reading of the board sees what is on screen. It only keeps the server's board fresh: this turn still
          // carries no snapshot, no input source and no board selection (#644), and a slow or failed save sends anyway,
          // without a dialog (#700). A canvas turn already flushed the brush on its snapshot path above.
          await settleBoardForTurn({
            sessionId, inputSource, canvasInputSource: schema.chat.context.canvas_input_source,
            waitMilliseconds: schema.canvas.frontend.send_save_wait_milliseconds,
          });
          if (!isCurrentSessionScope(scope) || useWorkspaceStore.getState().modelByDomain[domain] !== selectedModel ||
              useChatStore.getState().streamingBuffers[sessionId] || startingRef.current?.sessionId === sessionId) return;
          // #103/#239: the session's explicit 2D and 3D locks are written before the
          // turn that may use them. Each pick's write is already in flight (or
          // was made before this session existed); wait for it. A failed write
          // was rolled back and toasted, and this turn must not start under a
          // lock the server lacks.
          if (schema.runtime.http.routes.chat_session_model3d) {
            try {
              await flushExplicitModel3D(sessionId, (id, model) => chatClient.setExplicitModel3D(id, model));
            } catch {
              if (isCurrentSessionScope(scope)) toast.error(schemaText(schema, 'ui.studio.picker.lock_failed'));
              return;
            }
            if (!isCurrentSessionScope(scope) || useWorkspaceStore.getState().modelByDomain[domain] !== selectedModel ||
                useChatStore.getState().streamingBuffers[sessionId] || startingRef.current?.sessionId === sessionId) return;
          }
          if (schema.runtime.http.routes.chat_session_model2d) {
            try {
              await flushExplicitModelImage(sessionId, (id, model) => chatClient.setExplicitModelImage(id, model));
            } catch {
              if (isCurrentSessionScope(scope)) toast.error(schemaText(schema, 'ui.studio.picker.lock_failed'));
              return;
            }
            if (!isCurrentSessionScope(scope) || useWorkspaceStore.getState().modelByDomain[domain] !== selectedModel ||
                useChatStore.getState().streamingBuffers[sessionId] || startingRef.current?.sessionId === sessionId) return;
          }
          let replaced = false;
          // "重新生成"和"编辑后重发"替换最后一轮：先删旧消息，删不掉就整轮保持原样。
          if (replaceIds && replaceIds.length > 0) {
            const previous = useChatStore.getState().messagesBySession[sessionId] ?? [];
            try {
              // #645: each deletion names the turn's messages still to delete; the server deletes only while they
              // are its last turn, so a page that missed a newer turn gets a 409 and nothing is deleted.
              for (const [index, id] of replaceIds.entries()) {
                await chatClient.deleteMessage(sessionId, id, replaceIds.slice(index));
              }
            } catch (error) {
              const page = await chatClient.listMessagePage(sessionId, { limit: messagePageSize }).catch(() => null);
              if (page) applyNewestMessagePage(sessionId, page);
              else setMessages(sessionId, previous);
              // #403: a 409 is the server saying why (the turn still runs); anything else keeps the general text.
              toast.error(replaceFailureMessage(error, schemaText(schema, 'ui.chat.toast.turn_replace_failed')));
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
          const turn = initStreaming(sessionId, {
            content: text,
            images: images.map((url) => ({ url, taskId: '' })),
          });
          const controller = new AbortController();
          controllers.current.set(sessionId, controller);
          let closeEventSource: (() => void) | undefined;
          // How following the turn's stream ended; unset when the turn never reached a stream.
          let followed: TurnFollowEnd | undefined;
          // Set once the server has the turn: from then on nothing is put back into the box.
          let sent = false;
          // Only a message taken from the queue was typed by the person and already left the box. A stage action
          // (转 3D) or a choice-card answer also carries its own selection, but was never in the box or the queue.
          const queued = extra?.queued === true;
          try {
            const turnOptions: ChatTurnOptions = {
              ...(turnModel ? { model: turnModel } : {}),
              creativeDomain: domain,
              ...(Object.keys(otherDomainModels).length > 0 ? { domainModels: otherDomainModels } : {}),
              ...(selectedSkill ? { skill: { id: selectedSkill.id, name: selectedSkill.name } } : {}),
              ...(images.length > 0 ? { imageURLs: images } : {}),
              ...(claims.length > 0 ? { attachments: claims } : {}),
              ...(selection.length > 0 ? { selection } : {}),
              ...(mentions.length > 0 ? { mentions } : {}),
              ...(inputSource ? { inputSource } : {}),
              ...(selectionMask ? { selectionMask } : {}),
              ...(choiceAnswer ? { choiceAnswer } : {}),
              ...(multiview ? { multiview } : {}),
              ...(extra?.styleLockSkipped ? { styleLockSkipped: true } : {}),
            };
            // #665: the same request as a send whose answer never came goes out under that send's key, so the server
            // answers the turn it may already have started; any other send gets a key of its own. The locale is part
            // of the request (the server's fingerprint has it), and so is a queued message's id: two queued messages
            // with the same words are two requests. A key the server refuses is dropped and the send goes once more
            // under a fresh one (sendUnderKey).
            const locale = activeLocale(schema);
            const queuedId = extra?.queuedId;
            await sendUnderKey(uncertainSends, {
              sessionId,
              signature: sendSignature(sessionId, text, turnOptions, { locale, ...(queuedId ? { queuedId } : {}) }),
              ...(queuedId ? { typedSignature: sendSignature(sessionId, text, turnOptions, { locale }) } : {}),
              ...(extra?.idempotencyKey ? { preferred: extra.idempotencyKey } : {}),
            }, (key) => chatClient.startTurn(sessionId, text, { ...turnOptions, idempotencyKey: key }, controller.signal), idempotencyRefused);
            sent = true;
            extra?.onSent?.();
            // A one-shot Skill (declared in the Schema) is spent once the server has the turn; a failed send keeps it.
            if (selectedSkill?.oneShot && sessionSkillId(skillBySessionRef.current, sessionId) === selectedSkill.id) {
              selectSkill(sessionId, null);
            }
            // The exemption belongs to the turn that just went out.
            if (extra?.styleLockSkipped) setStyleLockSkipped(false);
            if (startingRef.current === starting) startingRef.current = null;
            useWorkspaceStore.getState().forgetAttachmentSources(images);
            // The draft is spent with the turn, even when the person has moved to another session meanwhile
            // (the scoped setters below are then no-ops and could not empty the stored draft). Only a send
            // accepted in this visit spends this visit's reads; a visit begun since has its own draft.
            // A choice-card answer leaves whatever the person is writing in the box.
            if (!choiceAnswer) {
              // #244: only a turn the server accepted spends the painted mask.
              if (withMask && maskDraftNow && sameMaskDraft(useMaskStore.getState().draft, maskDraftNow)) {
                useMaskStore.getState().clearDraft();
              }
              if (isCurrentSessionScope(scope)) draftEpoch.current += 1;
              clearDraft(sessionId);
              if (isCurrentSessionScope(scope)) {
                setInput('');
                setAttachedImages([]);
                setCanvasReference(null);
                setBranchFrom(null);
                // #644: 以画板为主 is this message's; the next one starts off again.
                setBoardModeChosen(false);
              }
            } else {
              void refreshChoices();
            }
            // #403: a stream that breaks is not the end of the turn. The server is asked whether it still runs;
            // if it does the stream is attached again from the turn's first event, up to the Schema's bound.
            followed = await followTurnStream({
              connect: (callbacks) => {
                closeEventSource = connectEventSource(sessionId, { fromId: 0, ...callbacks });
                return closeEventSource;
              },
              stillRunning: async (signal) => {
                const page = await chatClient.listMessagePage(sessionId, { limit: messagePageSize }, signal);
                return page.messages.some((message) => message.isPending);
              },
              rewind: () => rewindStreaming(sessionId),
              attempts: schema.chat.frontend.stream_reconnect_attempts,
              signal: controller.signal,
            });
            if (followed === 'interrupted') throw new Error(schema.chat.presentation.messages.sse_interrupted);
          } catch (error) {
            // The turn never left: the words were already out of the box (the old round was replaced,
            // or the message had waited in the queue), so they go back, not lost.
            const restore = !sent && (replaced || queued) && isCurrentSessionScope(scope) && !controller.signal.aborted;
            if ((error as Error).name !== 'AbortError') {
              if (restore && queued && turnActiveRefusal(error)) {
                // #665: another tab's turn of the session runs (it sent first): nothing failed, the words wait in the box.
                toast.info(schemaText(schema, 'ui.studio.chat.queued_other_tab'));
              } else if (idempotencyRefused(error)) {
                // #665: the key was refused twice; the message was not sent and the key is gone.
                toast.error(schemaText(schema, 'ui.studio.chat.send_key_refused'));
              } else {
                toast.error(
                  restore && queued
                    ? schemaText(schema, 'ui.studio.chat.queued_restored')
                    : turnFailureText(error, [schema.chat.presentation.messages.sse_interrupted], ui.sendFailed),
                );
              }
            }
            if (restore) {
              setInput((current) => (current === '' ? written : current));
              useWorkspaceStore.getState().queueChatAttachments(restoreAttachments(images, claims));
            }
          } finally {
            if (startingRef.current === starting) startingRef.current = null;
            closeEventSource?.();
            if (controllers.current.get(sessionId) === controller) controllers.current.delete(sessionId);
            // #177: read the stored turn first and swap it in place of the buffer in one update. Clearing
            // the buffer first dropped the whole turn for a frame, shrinking the list and jumping the view.
            let settled = false;
            const settleFallback = window.setTimeout(() => {
              if (!settled) finishStreaming(sessionId, turn);
            }, STREAM_SETTLE_FALLBACK_MILLISECONDS);
            chatClient
              .listMessagePage(sessionId, { limit: messagePageSize })
              .then((page) => {
                settled = true;
                window.clearTimeout(settleFallback);
                // #665: a read that comes back after a later turn started is that earlier turn's: it is dropped.
                if (!settleStreaming(sessionId, page, turn)) return;
                const shown = () => useChatStore.getState().activeSessionId === sessionId && !useChatStore.getState().streamingBuffers[sessionId];
                // #605: the server ended this turn's tasks with it; read them now and with the rereads below rather
                // than leave them running (and the branch disabled) until the next task poll.
                if (followed === 'done') {
                  const taskRead = {
                    route: schema.runtime.http.routes.task_item,
                    taskStates: schema.task_states,
                    progressMaximum: schema.runtime.frontend.percentage_multiplier,
                    timeoutMilliseconds: schema.runtime.frontend.task_read_timeout_milliseconds,
                    isCurrent: shown,
                  };
                  void refreshTasksAfterDone({
                    refresh: () => refreshSessionTasks(sessionId, taskRead),
                    isCurrent: shown,
                    delays: SETTLE_REREAD_MILLISECONDS,
                  });
                }
                // #403: the stream is over without saying the turn ended, yet the server still runs it (the stream
                // was given up or stopped, or another turn holds the session). The session recovery reconnects it
                // and keeps reading until it ends.
                if (!page.messages.some((message) => message.isPending)) return;
                if (followed !== 'done') {
                  setServerTurnTick((tick) => tick + 1);
                  return;
                }
                // #597: the stream said the turn ended before the server marked it so. Read again until the reply
                // is no longer pending; one still pending after that is a turn that really runs: recovery follows it.
                void readUntilSettled({
                  read: () => chatClient.listMessagePage(sessionId, { limit: messagePageSize }),
                  apply: (next) => applyNewestMessagePage(sessionId, next),
                  isCurrent: shown,
                  delays: SETTLE_REREAD_MILLISECONDS,
                }).then((outcome) => {
                  if (outcome === 'running' && shown()) setServerTurnTick((tick) => tick + 1);
                });
              })
              .catch(() => {
                settled = true;
                window.clearTimeout(settleFallback);
                finishStreaming(sessionId, turn);
              });
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
      }).finally(() => setDrainTick((tick) => tick + 1));
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
      setBranchFrom,
      setBoardModeChosen,
      canvasReference,
      hasCanvasReference,
      chatClient,
      connectEventSource,
      finishStreaming,
      settleStreaming,
      rewindStreaming,
      initStreaming,
      modelChoices,
      modelsError,
      modelsLoading,
      schema,
      skillBySession,
      selectSkill,
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
      uncertainSends,
      uploading,
    ],
  );
  const sendRef = useRef(send);
  sendRef.current = send;
  const deselectedObjectRef = useRef(deselectedObject);
  deselectedObjectRef.current = deselectedObject;
  const sessionSelectionRef = useRef(sessionSelection);
  sessionSelectionRef.current = sessionSelection;
  const versionsRef = useRef(versions);
  versionsRef.current = versions;

  // 舞台上的动作（转 3D）请 Agent 做一件事：作为一轮发出，不经过输入框。上一轮还在跑就
  // 排队，和输入框里发的话一样。额外积分的同意不在发送前问：Agent 要提交时，对话里的批准卡
  // 会写明基础积分、额外积分和用途，批准才算同意。
  useEffect(() => {
    if (!turnRequest) return;
    // #665: a send refuses while attachments upload; the request is kept (and said to wait) instead of being dropped,
    // and goes out when the upload ends (this effect runs again then).
    const step = stageRequestStep({
      hasSession: useChatStore.getState().activeSessionId !== null,
      uploadsPending: uploading || attachmentsPending(attachmentReads, captureSessionScope()),
    });
    if (step === 'wait') {
      toast.message(schemaText(schema, 'ui.chat.toast.stage_request_waiting'), { id: `stage-request-waiting:${turnRequest.seq}` });
      return;
    }
    // #691: the request stays pending (the three-view card's turn buttons stay off) until this send is over, a turn
    // that ran or a send that returned without one; a dropped request is no longer pending.
    const studio = useStudioStore.getState();
    void sendStageRequest(step, {
      clear: clearTurnRequest,
      begin: studio.beginStageTurn,
      end: studio.endStageTurn,
      send: () => sendRef.current(turnRequest.text, undefined, undefined, undefined, turnRequest.selection, undefined, undefined,
        turnRequest.multiview || turnRequest.models
          ? { ...(turnRequest.multiview ? { multiview: turnRequest.multiview } : {}), ...(turnRequest.models ? { domainModels: turnRequest.models } : {}) }
          : undefined),
    });
  }, [turnRequest, clearTurnRequest, uploading, schema]);

  // 排队的话：上一轮结束（或回到这个会话时没有在跑的轮次）就发出最早的一条。没能发出就放回
  // 输入框，不让用户的话丢掉。
  // #665: after a reload the queue waits for the session's messages to be read, and for a reply the server still runs
  // to end, so it never goes out beside a turn this page did not start. A turn is claimed from the stored queue before
  // it is sent: another tab that already sent (or cancelled) it makes the claim fail, and it is not sent twice.
  const drainMessages = activeSessionId ? messagesBySession[activeSessionId] : undefined;
  useEffect(() => {
    if (!activeSessionId || drainingRef.current) return;
    if (!queueMayDrain({ busy: preparing || uploading, historyLoading, messages: drainMessages, streaming: isStreaming })) return;
    // The turn that just ended may still hold the send lock while it cleans up; its release bumps drainTick.
    if (sendPreparation.current.has(activeSessionId)) return;
    const { turn } = takeQueuedTurn(queue, activeSessionId);
    if (!turn) return;
    // #665: another tab holds the session's send while the turn it sent runs: wait for its release (a storage event)
    // or for the hold to lapse (a tab that closed without releasing it). After another tab's claim or send, or a claim
    // of this tab that failed, read the session again first; a read that failed or was cut short is asked for again
    // after the Schema's poll interval.
    const step = nextDrainStep({
      hold: useSendQueueStore.getState().foreignHold(activeSessionId),
      now: Date.now(),
      required: drainGate[activeSessionId],
      forcedRead: forcedRead.current,
      pollMilliseconds: schema.chat.frontend.fallback_poll_milliseconds,
    });
    if (step.kind === 'wait') {
      const timer = setTimeout(() => {
        forcedRead.current = null;
        setDrainTick((tick) => tick + 1);
      }, step.delay);
      return () => clearTimeout(timer);
    }
    if (step.kind === 'read') {
      forcedRead.current = step.required;
      setServerTurnTick((tick) => tick + 1);
      return;
    }
    const sessionId = activeSessionId;
    drainingRef.current = sessionId;
    void (async () => {
      if (!(await useSendQueueStore.getState().claim(sessionId, turn.id))) {
        drainingRef.current = null;
        const next = lastReadStarted() + 1;
        setDrainGate((gate) => holdDrainForRead(gate, sessionId, next));
        // The hold this tab may still keep from its last turn goes now (the release effect runs on drainTick), so
        // another tab's queued messages for the session are not held back by an idle heartbeat and turn lock.
        setDrainTick((tick) => tick + 1);
        return;
      }
      const started = startedTurnsRef.current;
      await sendRef.current(turn.text, turn.images, turn.claims, undefined, turn.selection, turn.mentions, turn.inputSource, { queued: true, idempotencyKey: turn.idempotencyKey ?? turn.id, queuedId: turn.id, ...(turn.multiview ? { multiview: turn.multiview } : {}), ...(turn.domainModels ? { domainModels: turn.domainModels } : {}), ...(turn.styleLockSkipped ? { styleLockSkipped: true } : {}), ...(turn.selectionMask ? { selectionMask: turn.selectionMask } : {}) }).finally(() => {
        drainingRef.current = null;
        if (startedTurnsRef.current === started) {
          if (useChatStore.getState().activeSessionId === sessionId) {
            setInput((current) => (current === '' ? turn.text : current));
            useWorkspaceStore.getState().queueChatAttachments(restoreAttachments(turn.images, turn.claims));
            setStyleLockSkipped(turn.styleLockSkipped === true);
          }
          toast.error(schemaText(schema, 'ui.studio.chat.queued_restored'));
        }
        setDrainTick((tick) => tick + 1);
      });
    })();
  }, [activeSessionId, drainGate, drainTick, historyLoading, isStreaming, preparing, queue, schema, drainMessages, setInput, uploading]);

  // #665: each hold this tab took with a claim lives while the turn it sent runs (the store keeps it alive), and is
  // released once that turn is over or the person left the session, so another tab drains next. A page that goes away
  // releases them all.
  useEffect(() => {
    const store = useSendQueueStore.getState();
    for (const sessionId of holdsToRelease(store.heldSessions(), { sending: drainingRef.current, activeSessionId, activeTurnRunning })) {
      store.releaseHold(sessionId);
    }
  }, [activeSessionId, activeTurnRunning, drainTick]);
  useEffect(() => releaseAllHolds, []);

  // 「从这里继续」：这个对象成为下一次要求的引用（恢复选中），输入框拿到焦点。
  useEffect(() => {
    if (!continueRequest) return;
    clearContinueRequest();
    setDeselectedObject(null);
    const from = sessionSelectionRef.current;
    setBranchFrom(from && !from.taskId.startsWith('canvas:')
      ? { key: objectKey(from), label: versionLabel(schema, tileOfObject(versionsRef.current, from), from.kind) }
      : null);
    if (useStudioStore.getState().chatCollapsed) setCollapsed(false);
    window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
  }, [continueRequest, clearContinueRequest, schema, setBranchFrom, setCollapsed, setDeselectedObject]);

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
  // #645: an open card of a reply before the latest user message is not drawn (the person typed instead).
  const choicePlacement = useMemo(
    () => choiceCardPlacement(sessionChoices.cards, displayMessages.map((message) => message.id),
      latestReplies(displayMessages, schema.chat.frontend.optimistic_assistant_message_id), answeringChoice),
    [sessionChoices.cards, displayMessages, schema, answeringChoice],
  );
  // #327: a production list follows the message that proposed it; every other list goes to the end.
  const assetListCards = useMemo(
    () => assetListPlacement(sessionAssetLists.lists, displayMessages.map((message) => message.id)),
    [sessionAssetLists.lists, displayMessages],
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
    // #241: regenerating the same step is a weak, unconfirmed signal about the direction the reply produced.
    const produced = displayMessages.find((message) => message.id === regenerate.assistantId);
    void recordPreferenceSignal('regenerate', generatedPictureClaims(produced));
    const payload = turnPayload(regenerate.user);
    // #65: a turn sent with a structured multiview request sends it again, so the same pictures and views are bound.
    void send(payload.text, payload.images, payload.claims, regenerate.deleteIds, undefined, undefined, undefined,
      payload.multiview ? { multiview: payload.multiview } : undefined);
  }, [displayMessages, recordPreferenceSignal, regenerate, send]);

  const focusComposer = useCallback(() => {
    window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
  }, []);

  // 编辑后重发：把最后一条用户消息放回输入框（文字与图片及其来源），发送时替换那一轮。
  const editLastMessage = useCallback((message: ChatMessageType) => {
    if (!editable || editable.userId !== message.id) return;
    // #241: editing the prompt and sending again means the previous direction was not confirmed. It is a weak
    // signal, so the Agent asks before producing a batch in a new direction.
    const replaced = displayMessages.filter((item) => editable.deleteIds.includes(item.id));
    void recordPreferenceSignal('prompt_edit', replaced.flatMap(generatedPictureClaims));
    draftEpoch.current += 1;
    setEditing({ deleteIds: editable.deleteIds });
    setInput(message.content);
    setAttachedImages([]);
    useWorkspaceStore.getState().queueChatAttachments(editAttachments(message));
    focusComposer();
  }, [displayMessages, editable, focusComposer, recordPreferenceSignal, setAttachedImages, setEditing, setInput]);

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
    if (!ok) {
      toast.error(ui.pickFailed);
      return ok;
    }
    // #241: the pick is also a selected preference on the server. Refresh the
    // shared list so the sidebar and the rejected candidate cards agree with it.
    void refreshSessionPreferences();
    return ok;
  }, [chooseCandidate, refreshSessionPreferences, ui.pickFailed]);
  // #241: "不要这个" is an explicit server fact; a failure is shown and the dialog stays open.
  const rejectCandidate = useCallback(async (claim: ChatAttachmentClaim, reasonPreset: string, reasonText: string) => {
    const ok = await sessionPreferences.reject(claim, reasonPreset, reasonText);
    if (!ok) toast.error(ui.rejectFailed);
    return ok;
  }, [sessionPreferences.reject, ui.rejectFailed]);
  const removeMessage = useCallback(
    async (messageId: string) => {
      if (!activeSessionId) return;
      const sessionId = activeSessionId;
      const previous = useChatStore.getState().messagesBySession[sessionId] ?? [];
      const removed = previous.find((message) => message.id === messageId);
      deleteMessage(sessionId, messageId);
      try {
        await chatClient.deleteMessage(sessionId, messageId);
      } catch {
        // #665: only the removed message goes back; pages read since the deletion started are kept.
        if (removed) {
          setMessages(sessionId, restoreRemovedMessage(useChatStore.getState().messagesBySession[sessionId] ?? [], previous, removed));
        }
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
        // #651: a task already terminal on the server is history, not a failure this page just saw happen.
        store.registerTask({ ...input, autoPreview: false, restored: projection.terminal });
        useWorkspaceStore.getState().applyTaskDetail(input.taskId, projection, useChatStore.getState().activeSessionId);
      },
    }).catch(() => {});
  }, [schema]);

  const decideDetached = useCallback(
    async (card: ApprovalCardModel, approved: boolean) => {
      if (!activeSessionId) return;
      const sessionId = activeSessionId;
      const requestId = card.requestId;
      await deciding.current.run(requestId, async () => {
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
          setDecisionBusy((current) => {
            const next = new Set(current);
            next.delete(requestId);
            return next;
          });
        }
      });
    },
    [activeSessionId, applyNewestMessagePage, chatClient, hydrateLateTasks, messagePageSize, pending, refreshApproval, registerTask, schema],
  );

  // #103: re-read the session after a plan decision. One retry is made because a
  // stale budget is worse than a short wait; when both reads fail the plan area
  // shows the reload notice instead of keeping the old figures.
  const refreshSessionBudget = useCallback(
    async (sessionId: string): Promise<boolean> => {
      setPlanReloadingSession(sessionId);
      try {
        const loaded = await reloadSessionBudget(chatClient, sessionId);
        if (useChatStore.getState().activeSessionId === sessionId) {
          useChatStore.getState().updateSessionBudget(sessionId, loaded.budget);
          if (loaded.brief) useChatStore.getState().updateSessionBrief(sessionId, loaded.brief);
        }
        setPlanReloadFailure(null);
        return true;
      } catch {
        setPlanReloadFailure({ sessionId, message: schemaText(schema, 'ui.studio.plan.reload_failed') });
        return false;
      } finally {
        setPlanReloadingSession((current) => (current === sessionId ? null : current));
      }
    },
    [chatClient, schema],
  );

  // #410: 「继续」runs the session's own plan again. The plan is already approved, so this
  // starts no new decision and no new spend beyond the approved budget; the server refuses
  // when the plan is no longer the session's active one or its turn still runs, and the
  // Schema's text says which. A started turn is followed like any reply.
  const continuePlan = useCallback(
    async (budgetId: string) => {
      if (!activeSessionId) return;
      const sessionId = activeSessionId;
      await continuePlanOnce(deciding.current, schema, sessionId, budgetId, {
        started: () => {
          setContinueBusy(budgetId);
          setDecisionError((current) => {
            if (!(budgetId in current)) return current;
            const { [budgetId]: _cleared, ...rest } = current;
            return rest;
          });
        },
        succeeded: () => setServerTurnTick((tick) => tick + 1),
        failed: (error) => {
          const detail = error instanceof PendingApprovalError ? schemaText(schema, error.detail) : '';
          setDecisionError((current) => ({
            ...current,
            [budgetId]: detail || schemaText(schema, 'ui.studio.plan.continue_failed'),
          }));
        },
        settled: () => setContinueBusy(null),
      });
    },
    [activeSessionId, schema],
  );

  // #103: a plan is decided on the session budget route, not on a step grant. After the
  // decision the session is read again so the budget card and the topbar badge show the
  // server's new spent/limit figures without waiting for another turn.
  const decidePlan = useCallback(
    async (budgetId: string, approved: boolean) => {
      if (!activeSessionId) return;
      const sessionId = activeSessionId;
      await deciding.current.run(budgetId, async () => {
        setDecisionBusy((current) => new Set(current).add(budgetId));
        setDecisionError((current) => {
          if (!(budgetId in current)) return current;
          const { [budgetId]: _cleared, ...rest } = current;
          return rest;
        });
        try {
          const result = await pending.decide(budgetId, approved, 'plan');
          if (result.messageId) {
            const page = await chatClient.listMessagePage(sessionId, { limit: messagePageSize }).catch(() => null);
            if (page && useChatStore.getState().activeSessionId === sessionId) applyNewestMessagePage(sessionId, page);
          }
          const reloaded = await refreshSessionBudget(sessionId);
          if (!reloaded) toast.error(schemaText(schema, 'ui.studio.plan.reload_failed'));
          void refreshApproval();
          // #134: an approved plan runs in a turn the server started; follow its stream like any reply.
          if (result.turnMessageId && useChatStore.getState().activeSessionId === sessionId) {
            setServerTurnTick((tick) => tick + 1);
          }
        } catch (error) {
          const detail = error instanceof PendingApprovalError ? schemaText(schema, error.detail) : '';
          setDecisionError((current) => ({
            ...current,
            [budgetId]: detail || schemaText(schema, 'ui.studio.plan.decide_failed'),
          }));
        } finally {
          setDecisionBusy((current) => {
            const next = new Set(current);
            next.delete(budgetId);
            return next;
          });
        }
      });
    },
    [activeSessionId, applyNewestMessagePage, chatClient, messagePageSize, pending, refreshApproval, refreshSessionBudget, schema],
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
      // #374: an option with an action saves the function selection first and the answer goes out only once it is
      // saved. A selection that cannot be saved (a revision conflict included) says so on the card, sends nothing and
      // leaves the card answerable. The Harness is read at the moment of the save, not as of this callback's render.
      const choiceAnswer = selection.useDefault
        ? { choiceId: card.id, useDefault: true as const }
        : { choiceId: card.id, optionId: selection.optionId };
      const outcome = await runChoiceAnswer(choiceSelectionAction(card, selection), {
        apply: (action) => applyChoiceAction(action, {
          schema,
          harness: { selection: sessionHarnessRef.current.view?.selection ?? null, save: sessionHarnessRef.current.save },
          // #709: continue_on_canvas reads the board frame and the person's own words the card answers.
          canvas: {
            hasBoard: artboard !== null,
            originalWords: scope.sessionId
              ? originalWordsBefore(useChatStore.getState().messagesBySession[scope.sessionId] ?? [], card.assistantMessageId)
              : '',
            openBoardMode: () => {
              setBoardModeChosen(true);
              window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
            },
          },
        }),
        stillHere: () => isCurrentSessionScope(scope),
        send: async (applied) => {
          if (applied?.canvasTurnText) {
            // #709: the person's previous words again, as the canvas turn that answers the card (one send, the snapshot
            // path of a 以画板为主 turn; no queued selection, so the board is rasterised now).
            await sendRef.current(applied.canvasTurnText, [], [], undefined, undefined, [], undefined, {
              choiceAnswer, canvasTurn: true, onSent: () => { accepted = true; },
            });
            return;
          }
          await sendRef.current(text, [], [], undefined, [], [], undefined, {
            choiceAnswer,
            onSent: () => { accepted = true; },
          });
        },
      });
      if (outcome.status === 'action_failed') {
        setChoiceError({ cardId: card.id, text: schemaText(schema, outcome.failureKey) });
      } else if (outcome.status === 'sent') {
        if (!accepted) setChoiceError({ cardId: card.id, text: schemaText(schema, 'ui.chat.choice.failed') });
      }
    } catch {
      setChoiceError({ cardId: card.id, text: schemaText(schema, 'ui.chat.choice.failed') });
    } finally {
      setAnsweringChoice(null);
      void refreshChoices();
    }
  }, [answeringChoice, artboard, refreshChoices, schema, setBoardModeChosen, uploading]);
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
  const planCards = pending.items.filter((item) => item.kind === 'plan' && item.plan !== null);
  const reloadPlans = useCallback(() => {
    if (!activeSessionId) return;
    void pending.refresh();
    void refreshSessionBudget(activeSessionId);
  }, [activeSessionId, pending, refreshSessionBudget]);
  const approvalCards = [
    ...(turnCard ? [turnCard] : []),
    ...pending.items.filter((item) => item.kind === 'step').map((item) => cardFromPending(item, labelOfInput)),
  ];
  const sessionBudget = useChatStore((state) => state.sessions.find((session) => session.id === activeSessionId)?.budget ?? null);
  // #330 §6: the session's own title names the asset in the step dialog.
  const sessionTitle = useChatStore((state) => state.sessions.find((session) => session.id === activeSessionId)?.title ?? '');
  // #120: whether the plan is shown or still waits for a decision is the Schema role of its state.
  const budgetShown = budgetIsShown(schema, sessionBudget);
  const budgetAwaiting = budgetAwaitsDecision(schema, sessionBudget);

  // Tasks of this session that the page saw running. When one of them ends outside a turn (a late
  // approval), the result message, the version strip and the session credits catch up by themselves.
  const trackedTasks = useWorkspaceStore((state) => state.trackedTasks);
  const watchedTasksRef = useRef(new Set<string>());
  const sessionTasks = useMemo(
    () => trackedTasks.filter((task) => task.sessionId === activeSessionId && !task.notFound),
    [trackedTasks, activeSessionId],
  );
  const compacting = useSessionContextStore((state) => Boolean(activeSessionId && state.compacting[activeSessionId]));
  const [branchBusy, setBranchBusy] = useState(false);
  const branchBusyRef = useRef(false);
  // 3D is no longer a reason to disable branching (#186): the branch copies
  // the current stage, versions and captures. Only live work and decisions
  // still block, because the copy must be a stable current state.
  const branchReasonKey = branchUnavailableReason({
    busy: branchBusy, compacting, approval: pendingCount > 0 || budgetAwaiting,
    running: isStreaming || preparing || uploading || sessionTasks.some((task) => !task.terminal),
  });
  const branchFromMessage = useCallback(async (message: ChatMessageType) => {
    const sessionId = activeSessionId;
    if (!sessionId || branchBusyRef.current || branchReasonKey) return;
    branchBusyRef.current = true;
    setBranchBusy(true);
    try {
      await branchConversation(chatClient.branchSession, sessionId, message.id, {
        flush: () => flushPendingEdits(sessionId), isCurrent: () => useChatStore.getState().activeSessionId === sessionId,
        clear: clearTrackedTasks, select: addSession, remember: useChatStore.getState().rememberSession,
        done: (copied) => toast.success(schemaText(schema, 'ui.studio.message.branch_done'), branchCopiedCurrentState(copied) ? { description: schemaText(schema, 'ui.chat.branch_canvas_current') } : undefined),
        failed: (error) => toast.error(turnFailureText(error, [], schemaText(schema, 'ui.studio.message.branch_failed'))),
      });
    } finally {
      branchBusyRef.current = false;
      setBranchBusy(false);
    }
  }, [activeSessionId, addSession, branchReasonKey, chatClient, clearTrackedTasks, schema]);
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
  // #561: a decided plan's figures and state move on the server after its Tasks end (the settlement charges the
  // step a moment later, and the plan turns used up or completed). Read the budget again at the same moments as the
  // late result, so the plan card does not keep the state it had when it was approved. No polling: only an ended
  // Task starts it.
  const budgetFollows = budgetFollowsTasks(schema, sessionBudget);
  useEffect(() => {
    if (!activeSessionId || settledWatchedKey === '' || !budgetFollows) return;
    const sessionId = activeSessionId;
    return followSettledBudget(chatClient, sessionId, {
      delays: [0, ...LATE_RESULT_RELOAD_MILLISECONDS],
      apply: ({ budget }) => {
        if (useChatStore.getState().activeSessionId === sessionId) useChatStore.getState().updateSessionBudget(sessionId, budget);
      },
    });
  }, [activeSessionId, budgetFollows, chatClient, settledWatchedKey]);
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
  const chip = useMemo((): ComposerChip | null => {
    if (!sessionSelection) return null;
    const tile = tileOfObject(versions, sessionSelection);
    // A picture drawn on the board that is no version has no chip; a merged version (手绘) is one.
    if (sessionSelection.taskId.startsWith('canvas:') && !tile) return null;
    // #644: the chip names the picture the turn works on; a canvas turn shows the board instead (composerChip).
    return {
      kind: sessionSelection.kind,
      // #290: chips are small; they show the preview of a task picture.
      thumbnail: sessionSelection.kind === 'image'
        ? previewAssetUrl(sessionSelection.url)
        : tile?.thumbnailUrl ? previewAssetUrl(tile.thumbnailUrl) : '',
      label: versionLabel(schema, tile, sessionSelection.kind),
      handle: sessionSelection.handle ?? handleOfObject(sessionMessages, sessionSelection),
    };
  }, [previewAssetUrl, schema, sessionMessages, sessionSelection, versions]);
  // The branch chip belongs to the version it was made for: another version on the stage drops it.
  const branchChip = useMemo((): ComposerBranch | null => {
    if (!branchFrom || !sessionSelection || objectKey(sessionSelection) !== branchFrom.key) return null;
    return {
      label: branchFrom.label,
      onClear: () => {
        // The new version follows the object the turn carries; leaving the branch leaves the object too.
        setBranchFrom(null);
        setDeselectedObject(branchFrom.key);
      },
    };
  }, [branchFrom, sessionSelection, setBranchFrom, setDeselectedObject]);
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
    // #560: a hidden layer can still be named but lists in the library. Visible layers keep their numbers; hidden
    // ones are numbered after them, so hiding a layer does not renumber the layers still shown.
    const pictureLayers = [...(canvasNodes ?? [])]
      .filter((node) => node.type === 'image' && node.imageUrl)
      .sort((left, right) => left.zIndex - right.zIndex);
    const layers = [...pictureLayers.filter((node) => node.visible !== false), ...pictureLayers.filter((node) => node.visible === false)]
      .map((node, index) => ({
        name: formatSchemaText(schemaText(schema, 'ui.studio.composer.mention.token_layer'), { index: index + base }),
        url: node.imageUrl,
        ...(node.taskId && !node.taskId.startsWith('canvas:') && !node.taskId.startsWith('upload:') ? { taskId: node.taskId } : {}),
        ...(node.captureId ? { captureId: node.captureId } : {}),
        ...(node.visible === false ? { hidden: true } : {}),
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
  // #644: what the next message is sent as. The same rule the send reads, so the toggle never shows one thing
  // while the message goes out as another; a canvas turn shows the board as the chip.
  const boardTurnNow = boardMode({
    chosen: boardModeChosen, artboard: artboard !== null, maskSteps: maskStrokes, namesBoard: mentionsBoard(input, mentionList),
  });
  const composerChip = useMemo((): ComposerChip | null => (boardTurnNow.on && chip?.kind === 'image'
    ? { kind: 'image', thumbnail: chip.thumbnail, label: schemaText(schema, 'ui.studio.composer.chip.canvas'), handle: '' }
    : chip), [boardTurnNow.on, chip, schema]);
  const composerBoardMode = useMemo((): ComposerBoardMode | null => (artboard === null ? null : {
    on: boardTurnNow.on,
    forced: boardTurnNow.forced,
    onToggle: () => setBoardModeChosen((current) => !current),
  }), [artboard, boardTurnNow.forced, boardTurnNow.on, setBoardModeChosen]);
  // The composer's context for the next turn's settings and the canvas size notice (use-canvas-artboard): whether
  // a picture rides along, and whether the board is the turn's source (#644: only a canvas turn).
  const boardTurnOn = boardTurnNow.on;
  useEffect(() => {
    if (!activeSessionId) return;
    const context = { sessionId: activeSessionId, hasReference: Boolean(hasReference), objectDeselected, boardTurn: boardTurnOn };
    useStudioStore.getState().setGenerationReferenceContext(context);
    return () => {
      if (useStudioStore.getState().generationReferenceContext === context) {
        useStudioStore.getState().setGenerationReferenceContext(null);
      }
    };
  }, [activeSessionId, boardTurnOn, hasReference, objectDeselected]);
  // 「发送到对话」 from the board: this message goes out as a canvas turn (#644). #677: the press always shows what it
  // did: the toggle lights up, the composer takes focus and a note says so, or says it was already on.
  const boardModeRequest = useWorkspaceStore((state) => state.boardModeRequest);
  const boardTurnOnRef = useRef(boardTurnOn);
  boardTurnOnRef.current = boardTurnOn;
  useEffect(() => {
    if (!boardModeRequest || boardModeRequest !== activeSessionId) return;
    useWorkspaceStore.getState().clearBoardModeRequest();
    if (boardTurnOnRef.current) toast.info(schemaText(schema, 'ui.studio.stage.action.send_chat_already_on'));
    else {
      setBoardModeChosen(true);
      toast.success(schemaText(schema, 'ui.studio.stage.action.send_chat_done'));
    }
    if (useStudioStore.getState().chatCollapsed) setCollapsed(false);
    window.requestAnimationFrame(() => focusWhenUnblocked(() => document.getElementById('art-ai-chat-composer')));
  }, [activeSessionId, boardModeRequest, schema, setBoardModeChosen, setCollapsed]);
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
    // #665: a cancel the server did not take is said, and the turn is still followed (it still runs).
    if (activeSessionId) {
      const sessionId = activeSessionId;
      void requestTurnStop(chatClient, sessionId, {
        onStopped: () => controllers.current.get(sessionId)?.abort(),
        onRefused: () => toast.error(schemaText(schema, 'ui.chat.toast.stop_failed')),
      });
    }
  };
  // #137: while a Task of the streaming turn runs, ask the server whether the turn may stop waiting
  // with its Task running on; only then does the stop offer that choice. The SSE stays open: the
  // server ends the turn with its note and the done event, and the Task stays in the task tray.
  const backgroundPollMilliseconds = schema.chat.frontend.background_wait_poll_milliseconds;
  const asksBackground = asksTurnBackground({
    sessionId: activeSessionId,
    streaming: isStreaming,
    taskRunning: sessionTasks.some((task) => !task.terminal),
    route: schema.runtime.http.legacy_routes.chat_turn_background,
    member: schema.chat.api.fields.turn_background_available,
    intervalMilliseconds: backgroundPollMilliseconds,
  });
  const backgroundAvailable = useTurnBackground(chatClient, activeSessionId, asksBackground, backgroundPollMilliseconds);
  const stopInBackground = () => {
    if (!activeSessionId) return;
    void chatClient.continueTurnInBackground(activeSessionId).catch((error: unknown) => {
      toast.error(turnFailureText(error, [], schema.chat.presentation.messages.internal_error));
    });
  };

  const title = schemaText(schema, 'ui.studio.chat.title');
  const collapseLabel = schemaText(schema, 'ui.studio.chat.collapse');
  const expandLabel = schemaText(schema, 'ui.studio.chat.expand');

  return (
    <aside
      aria-label={title}
      data-testid="studio-chat-column"
      data-collapsed={collapsed}
      style={{ width: collapsed ? CHAT_RAIL_WIDTH : shownChatWidth }}
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
            <SessionStepsBar sessionID={activeSessionId} name={sessionTitle} />
          ) : null}

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
            style={{ paddingBottom: '0.75rem' }}
            data-testid="chat-messages"
          >
            {historyLoading && displayMessages.length === 0 ? (
              <ChatHistorySkeleton label={schemaText(schema, 'ui.chat.loading')} />
            ) : displayMessages.length === 0 && threeViews.length === 0 && !isStreaming && approvalCards.length === 0 && planCards.length === 0 && !budgetShown && queuedTurns.length === 0 ? (
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
                    taskProgress={item.message.id === progressMessageId ? runningTaskBlock : undefined}
                    statusBubbleShown={statusBubbleShown(isStreaming, statusText, runningTaskCount)}
                    sessionId={activeSessionId}
                    onImageClick={openImageDetail}
                    handleReferences={sessionHandleReferences}
                    pick={sessionPick.pick}
                    pickBusy={sessionPick.busy}
                    onPick={pickCandidate}
                    rejectedAssets={rejectedAssets}
                    onReject={rejectCandidate}
                    onCopyToInput={item.message.isPending || editable?.userId === item.message.id ? undefined : copyToInput}
                    onRegenerate={regenerate?.assistantId === item.message.id ? regenerateReply : undefined}
                    regenerateCredits={regenerate?.assistantId === item.message.id ? regenerateCredits : undefined}
                    onEdit={editable?.userId === item.message.id ? editLastMessage : undefined}
                    onDelete={item.message.isPending ? undefined : deleteFromList}
                    onBranch={schema.runtime.http.routes.chat_session_branch && (item.message.role === 'user' || item.message.role === 'assistant') ? branchFromMessage : undefined}
                    branchDisabledReason={item.message.isPending ? schemaText(schema, 'ui.unavailable.reason.branch.running') : branchReasonKey ? schemaText(schema, branchReasonKey) : undefined}
                  />
                  {(choicePlacement.byMessage.get(item.message.id)?.length ?? 0) > 0 ? (
                    <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-choice-cards">
                      {choicePlacement.byMessage.get(item.message.id)!.map(renderChoiceCard)}
                    </div>
                  ) : null}
                  {(assetListCards.byMessage.get(item.message.id)?.length ?? 0) > 0 ? (
                    <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-asset-list-cards">
                      {assetListCards.byMessage.get(item.message.id)!.map((list) => (
                        <AssetListCardLoader
                          key={list.id}
                          summary={list}
                          schema={schema}
                          client={assetListsClient}
                          variant="chat"
                          onDecided={sessionAssetLists.refresh}
                          onEdited={sessionAssetLists.refresh}
                          onStale={sessionAssetLists.refresh}
                        />
                      ))}
                    </div>
                  ) : null}
                  {compactionDividers.byMessage.get(item.message.id)?.map((compaction) => (
                    <ContextCompactedDivider key={compaction.id} compaction={compaction} />
                  ))}
                  </Fragment>
                ))}
                {/* #546: one expression of progress at a time; the turn's task block replaces the "generating" bubble. */}
                {!progressMessageId && runningTaskCount > 0 ? <TaskStatusCards part="running" /> : null}
                {statusBubbleShown(isStreaming, statusText, runningTaskCount) ? (
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
            {assetListCards.loose.length > 0 ? (
              <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-asset-list-loose">
                {assetListCards.loose.map((list) => (
                  <AssetListCardLoader
                    key={list.id}
                    summary={list}
                    schema={schema}
                    client={assetListsClient}
                    variant="chat"
                    onDecided={sessionAssetLists.refresh}
                    onEdited={sessionAssetLists.refresh}
                    onStale={sessionAssetLists.refresh}
                  />
                ))}
              </div>
            ) : null}
            {(planCards.length > 0 || budgetShown || planReloadError) ? (
              <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-plan-cards">
                {planCards.map((item) => item.plan ? (
                  <PlanCard
                    key={item.requestId}
                    plan={item.plan}
                    budgets={item.budgets}
                    stepTotals={item.stepTotals}
                    contingencyTotals={item.contingencyTotals}
                    tasks={sessionTasks}
                    busy={decisionBusy.has(item.requestId)}
                    error={decisionError[item.requestId] ?? null}
                    onDecide={(approved) => { void decidePlan(item.requestId, approved); }}
                    onCompareCandidates={compareCandidates}
                    onSelectCandidate={selectCandidate}
                  />
                ) : null)}
                {planCards.length === 0 && (planReloadError || budgetAwaiting) ? (
                  <PlanReloadNotice
                    message={planReloadError ?? schemaText(schema, 'ui.studio.plan.reload_failed')}
                    busy={planReloading}
                    onReload={reloadPlans}
                  />
                ) : planCards.length === 0 && budgetShown && sessionBudget ? (
                  <PlanCard
                    plan={sessionBudget.plan}
                    tasks={sessionTasks}
                    progress={sessionBudget}
                    stepsMayStillRun={planStepsMayStillRun}
                    error={decisionError[sessionBudget.id] ?? null}
                    onContinue={() => { void continuePlan(sessionBudget.id); }}
                    continueBusy={continueBusy === sessionBudget.id}
                    onCompareCandidates={compareCandidates}
                    onSelectCandidate={selectCandidate}
                  />
                ) : null}
              </div>
            ) : null}
            {approvalCards.length > 0 ? (
              <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-approval-cards">
                {approvalCards.map((card) => (
                  <ApprovalCard
                    key={card.requestId}
                    model={card}
                    busy={card.source === 'turn' ? consentBusy : decisionBusy.has(card.requestId)}
                    now={now + (pending.clockOffset ?? 0)}
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
          {/* 「回到最新」按钮悬浮在消息区底部；#546 起进行中的任务改在对话内容里显示，不再悬浮。 */}
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
          </div>
          </div>

          {multiviewTarget && multiviewOpen ? (
            <MultiviewUploadPanel
              images={attachedImages} target={multiviewTarget} lockNote={multiviewNote} disabled={uploading || preparing || isStreaming}
              onDismiss={() => setMultiviewOpenFor(null)}
              onConfirm={(choices) => {
                const picked = attachedImages.flatMap((url, index) => (choices[index] ? [url] : []));
                const views = choices.filter((view) => view !== '');
                const labels = chosenViewLabels(choices, multiviewTarget.views).join(schemaText(schema, 'ui.studio.three_view.view_separator'));
                void send(formatSchemaText(schemaText(schema, 'ui.studio.multiview_upload.turn'), { views: labels }), picked, undefined, undefined, undefined, undefined, undefined, { multiviewViews: views });
              }}
            />
          ) : null}
          <Composer
            value={input}
            onChange={setInput}
            onSend={(onAccepted) => {
              // Only the editor that sent the turn closes it: a send accepted after the person switched
              // sessions must not collapse the new session's editor.
              const session = activeSessionId;
              void send(input, undefined, undefined, editing?.deleteIds, undefined, undefined, undefined, {
                ...(styleLockSkipped ? { styleLockSkipped: true } : {}),
                onSent: () => { if (useChatStore.getState().activeSessionId === session) onAccepted(); },
              });
            }}
            onStop={stopTurn}
            onStopInBackground={backgroundAvailable ? stopInBackground : null}
            streaming={isStreaming}
            locked={uploading || preparing}
            canSend={(input.trim() !== '' || attachedImages.length > 0 || hasCanvasReference || Boolean(currentSkill?.tool)) && Boolean(activeSessionId)}
            chip={objectDeselected ? null : composerChip}
            boardMode={composerBoardMode}
            multiview={multiviewOffered && multiviewTarget ? {
              model: multiviewTarget.offering.model_name,
              open: multiviewOpen,
              onToggle: () => setMultiviewOpenFor(multiviewOpen ? null : multiviewSignature),
            } : null}
            onDeselectChip={() => {
              if (sessionSelection) setDeselectedObject(objectKey(sessionSelection));
            }}
            mask={maskChip}
            branch={branchChip}
            images={attachedImages}
            onRemoveImage={(index) => {
              setAttachedImages((current) => current.filter((_, imageIndex) => imageIndex !== index));
            }}
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
              supportingModels: supportingModelsError ? undefined : supportingModels,
              unavailable: skillSelection.unavailable,
              loading: skillCatalog.loading || effective.loading,
              failed: skillCatalog.failed || effective.failed,
              disabled: isStreaming || preparing,
              onSelect: (id) => {
                if (!activeSessionId) return;
                selectSkill(activeSessionId, id);
                // A Skill's recommended size becomes the size default when the model offers it.
                const chosen = skillCatalog.catalog?.skills.find((item) => item.id === id);
                const size = chosen ? skillSizeDefault(generation.nextTurn.model.sizeParams, chosen.recommendedSize) : null;
                if (size) generation.nextTurn.setValue(size.name, size.value);
              },
              onDeleted: (id) => {
                // 指向被删 Skill 的会话选择改为“不使用”，不保留失效 ID（#238）。
                Object.entries(skillBySessionRef.current).forEach(([session, selected]) => {
                  if (selected === id) selectSkill(session, null);
                });
                setSkillRefreshVersion((version) => version + 1);
              },
              onCreated: (id) => {
                if (activeSessionId) {
                  selectSkill(activeSessionId, id);
                }
                setSkillRefreshVersion((version) => version + 1);
              },
              onUpdated: () => {
                // 编辑保持当前选择（ID 未变），只刷新目录与有效配置。
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
            styleLock={styleLockActive ? {
              active: true,
              skipped: styleLockSkipped,
              notice: styleLockNotice,
              onToggleSkip: () => setStyleLockSkipped((current) => !current),
              onOpen: () => {
                // The panel lives in the 功能 tab now; the right panel switches the tab and opens the section.
                useStudioStore.getState().requestStyleLock();
              },
            } : null}
            mentions={mentions}
            onMentionPicked={(candidate) => {
              const scope = captureSessionScope();
              pickedMentionsRef.current = { scope, picks: [...pickedMentions(scope).filter((item) => item.handle !== candidate.handle), candidate] };
            }}
          />
        </>
      )}
      {!collapsed ? (
        <ResizeHandle
          width={shownChatWidth}
          max={chatWidthCap}
          label={schemaText(schema, 'ui.studio.chat.resize')}
          onPreview={setDragWidth}
          onCommit={setChatWidth}
        />
      ) : null}
    </aside>
  );
}

function objectKey(object: { taskId: string; assetId?: string; url: string }): string {
  return `${object.taskId}:${object.assetId ?? object.url}`;
}
