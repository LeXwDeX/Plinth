'use client';

import { Suspense, lazy, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';
import { AlertTriangle, Box, ChevronLeft, ChevronRight, Copy, Download, FileText, ImageIcon, Loader2, Maximize2, RotateCw, Share2, Eye } from 'lucide-react';
import { toast } from 'sonner';

import { ResultAttachments } from './result-attachments';
import { TaskRecoveryPanel } from './task-recovery-panel';
import { TaskRefetchPanel } from './task-refetch-panel';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { ModelViewer3D } from '@/components/viewer/model-viewer-3d';
import { ModelArchiveLink, ModelFormatsPanel } from '@/components/workspace/model-formats';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { useActiveAgentScope } from '@/hooks/use-session-agent-scope';
import { useAuth } from '@/hooks/use-auth';
import { writeSessionAgentParams } from '@/hooks/use-session-agent-params';
import { useTaskDetail } from '@/hooks/use-task-detail';
import { useModelFormatCoverage } from '@/hooks/use-model-format-coverage';
import { useSessionVersions } from '@/hooks/use-session-versions';
import { objectOfTile } from '@/lib/workspace/version-object';
import { modelFormatsRoute } from '@/lib/workspace/model-formats';
import { buildDetailRows, conversionAllowed, detailActions, detailBlockedReason, detailPrompt, detailSource, detailStatus, type DetailStatus } from '@/lib/workspace/task-detail';
import { groupPosition, objectOfTarget, targetOfTaskAssets, targetOfTile, type ResultTarget } from '@/lib/workspace/result-target';
import { formatSchemaText, schemaCountMessage } from '@/lib/product-schema';
import { findTaskState, isDownloadableAsset, resolveErrorText, viewableModelAsset } from '@/lib/workspace/task-projection';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { fetchCapabilityOfferings } from '@/lib/agent-offerings';
import { canCreateGaussianSplat, gaussianSplatAvailableCount, gaussianSplatTool } from '@/lib/workspace/gaussian-splat-action';

type PreviewKind = 'image' | 'video' | 'model' | 'splat' | 'none';

const GaussianSplatViewer = lazy(() => import('@/components/viewer/gaussian-splat-viewer').then((module) => ({ default: module.GaussianSplatViewer })));

export function previewKindOf(target: Pick<ResultTarget, 'kind' | 'mediaType' | 'url'>): PreviewKind {
  if (!target.url) return 'none';
  if (target.kind === 'splat') return 'splat';
  if (target.kind === 'model') return 'model';
  const media = target.mediaType ?? '';
  if (media.startsWith('video/')) return 'video';
  if (media.startsWith('image/') || (media === '' && target.kind === 'image')) return 'image';
  return 'none';
}

/**
 * 任务详情：所有入口（版本条、全部产出、对话结果卡、任务中心、我的资源）共用的一个面板。
 * 上方预览（2D 图 / 3D 模型，可全屏），中间是由 Schema 驱动的信息，下方按任务类型和
 * Agent 模式显示的动作。没有 taskId 的是上传的资源：只有预览、文件信息和下载。
 */
export function ResultDetail({ target }: { target: ResultTarget }) {
  const { user } = useAuth();
  const { schema, text } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { openOnStage, toThreeD, toGaussianSplat, captureToTwoD } = useObjectActions();
  const scope = useActiveAgentScope();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const tracked = useWorkspaceStore((state) => state.trackedTasks.find((task) => task.taskId === target.taskId));
  const closeResultDetail = useStudioStore((state) => state.closeResultDetail);
  const { task, offering, error } = useTaskDetail(target.taskId, target.capability || tracked?.capability);
  // An entry without a session (the account page) reads the task's own session for its source (#189).
  const versions = useSessionVersions(target.sessionId || task?.sessionId || null);
  const source = useMemo(() => detailSource(schema, target.taskId ?? '', versions), [schema, target.taskId, versions.tiles, versions.byTask, versions.fileTaskParents]);
  const [measured, setMeasured] = useState<{ width: number; height: number } | null>(null);
  const previewRef = useRef<HTMLDivElement>(null);

  const status: DetailStatus = (task ? detailStatus(schema, task.state) : null) ?? target.status ?? (target.taskId ? 'pending' : 'ready');
  const prompt = detailPrompt({ task, fallbackPrompt: target.prompt || tracked?.prompt });
  const rows = useMemo(
    () => buildDetailRows(schema, { task, offering, source, fallbackPrompt: prompt, measured, capability: target.capability || tracked?.capability }),
    [schema, task, offering, source, prompt, measured, target.capability, tracked?.capability],
  );
  const hasTask = Boolean(target.taskId);
  // Fill task-only entries after loading; never replace the file the person selected.
  const availableAssets = target.assetId ? task?.assets.filter((asset) => asset.id === target.assetId) : task?.assets;
  const resolvedTarget = !target.url && availableAssets?.length ? targetOfTaskAssets(target, availableAssets) : target;
  const displayTarget = resolvedTarget.url ? resolvedTarget : target;
  const file = hasTask ? proxyAssetUrl(displayTarget.url, target.taskId) : safePreviewURL(displayTarget.url);
  const kind = previewKindOf({ ...displayTarget, url: file ? displayTarget.url : '' });
  const actionContext = {
    kind: displayTarget.kind, status, hasTask,
    inActiveSession: Boolean(target.sessionId) && target.sessionId === activeSessionId,
    hasPrompt: prompt !== '', hasFile: Boolean(file), scope,
  };
  const actions = detailActions(actionContext);
  const formatsRoute = target.taskId ? modelFormatsRoute(schema, target.taskId) : null;
  const showFormats = Boolean(target.taskId) && displayTarget.kind === 'model' && status === 'ready' && formatsRoute !== null;
  // A file the format list already offers (the keycap's OBJ bundle) is not listed again as an attachment (#189).
  const { data: coverage } = useModelFormatCoverage(showFormats ? formatsRoute : null);
  const listedFormats = new Set(coverage?.formats.map((row) => row.assetId).filter(Boolean) ?? []);
  // Without a viewable model (e.g. a lampshade's separate STL parts), every model
  // file is its own download instead of only the first one (#189).
  const unviewableModels = status === 'ready' && task && !viewableModelAsset(task.assets)
    ? task.assets.filter(asset => asset.kind === 'model' && isDownloadableAsset(asset)).map(asset => ({ ...asset, kind: 'other' as const }))
    : [];
  const attachments = status === 'ready' ? [...(task?.assets.filter(asset => asset.kind === 'other' && !listedFormats.has(asset.id)) ?? []), ...unviewableModels] : [];
  const singleDownload = !showFormats && !((displayTarget.kind === 'other' || unviewableModels.length > 0) && attachments.length > 0) && (actions.download || (!hasTask && Boolean(file)));
  const fileLabel = displayTarget.fileName || displayTarget.mediaType?.split('/').pop()?.toUpperCase() || displayTarget.title;
  // A result of another session keeps its stage actions, dimmed, with the reason on the button.
  const blockedReason = detailBlockedReason(actionContext);
  const blockedText = blockedReason ? text(blockedReason) : '';
  const conversion = blockedReason ? conversionAllowed(displayTarget.kind, scope) : null;
  const position = groupPosition(target);
  const stepGroup = useStudioStore((state) => state.stepResultDetail);
  const object = objectOfTarget(displayTarget);
  const splatTool = gaussianSplatTool(schema);
  const { data: splatOfferings, isLoading: splatOfferingsLoading, error: splatOfferingsError } = useSWR(
    splatTool ? ['gaussian-splat-offerings', schema.revision, splatTool.capability] : null,
    () => fetchCapabilityOfferings(schema, splatTool!.capability),
    { revalidateOnFocus: false },
  );
  const splatAvailableCount = gaussianSplatAvailableCount(splatOfferings);
  const splatCanRun = canCreateGaussianSplat(splatTool, splatOfferings);
  const stateLabel = task ? findTaskState(schema.task_states, task.state)?.label : undefined;
  const failureText = status === 'failed'
    ? resolveErrorText(schema.content.messages, task?.failure?.detail || tracked?.failure?.detail || tracked?.liveError, text('ui.workspace.tasks.error_title'))
    : '';
  const loadFailed = hasTask && !task && Boolean(error);

  // The dialog keeps one size while paging. The declared previous/next keys work
  // wherever focus sits in it, except while typing and while a control already
  // handled the key. Listen in the capture phase: a modal primitive on document
  // (bubble) stops the event before it reaches a document-level listener.
  const canPage = (position?.count ?? 0) > 1;
  const previousKey = schema.chat.frontend.lightbox_previous_key;
  const nextKey = schema.chat.frontend.lightbox_next_key;
  useEffect(() => {
    if (!canPage) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
      if (event.key !== previousKey && event.key !== nextKey) return;
      const element = event.target instanceof HTMLElement ? event.target : null;
      if (element?.closest('input, textarea, select, [contenteditable="true"]')) return;
      event.preventDefault();
      stepGroup(event.key === previousKey ? -1 : 1);
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [canPage, previousKey, nextKey, stepGroup]);

  const openTask = (taskId: string, sessionId: string, capability: string) => {
    useStudioStore.getState().openResultDetail({ taskId, sessionId, capability, kind: 'model', url: '', title: text('ui.result.detail.title'), status: 'pending' });
  };
  const recoveryLabels = schema.task_history.asset_recovery;

  const fullscreen = () => { previewRef.current?.requestFullscreen?.().catch(() => undefined); };
  const copyPrompt = async () => {
    try {
      await navigator.clipboard.writeText(prompt);
      toast.success(text('ui.result.detail.copied'));
    } catch {
      toast.error(text('ui.result.detail.copy_failed'));
    }
  };
  // The conversion and open actions put the object on the stage, so the dialog gets out of the way.
  const run = (action: () => boolean) => { if (action()) closeResultDetail(); };
  const regenerate = async () => {
    // Puts the stored parameters into the session's params store (the params panel's own path) and the
    // prompt into the composer. Nothing is submitted, so the person still confirms the cost.
    const declared = new Set((offering?.parameters ?? []).filter((param) => !param.fixed && param.presentation?.surface !== 'hidden' && param.presentation?.surface !== 'readonly').map((param) => param.name));
    const values = Object.fromEntries(Object.entries(task?.parameters ?? {}).filter(([name]) => declared.has(name)));
    if (target.sessionId && offering?.id && Object.keys(values).length > 0) {
      try {
        await writeSessionAgentParams(schema, target.sessionId, values, offering.id, user?.id);
      } catch {
        toast.error(text('ui.workspace.harness.save_failed'));
      }
    }
    useStudioStore.getState().prefillComposer(prompt);
    closeResultDetail();
  };

  // The pager must not move when the index changes. The UI font stack has no
  // tabular digits (tabular-nums is a no-op there), so "1"/"7" are narrower than
  // the other digits and re-center the whole row. Reserve the width of the
  // widest digit ("8" in this stack) for every index digit of the group and
  // center the real label over that slot.
  const positionLabel = position
    ? schemaCountMessage(schema, 'ui.result.detail.position', position.count, { index: position.index + 1, count: position.count })
    : '';
  const positionDigits = position ? String(position.count).length : 0;
  const positionSlotLabel = position
    ? formatSchemaText(text('ui.result.detail.position'), { index: '8'.repeat(positionDigits), count: '8'.repeat(positionDigits) })
    : '';

  return (
    <div className="flex min-h-0 min-w-0 flex-col gap-3" data-testid="result-detail" data-status={status} data-has-task={hasTask}>
      {position ? (
        <div className="flex shrink-0 items-center justify-center gap-2" role="group" aria-label={text('ui.result.detail.group_label')} data-testid="result-detail-group">
          <HintTooltip name={text('ui.result.detail.prev')}>
            <Action type="button" variant="ghost" size="icon-xs" disabled={position.index === 0} onClick={() => stepGroup(-1)} aria-label={text('ui.result.detail.prev')} data-testid="result-detail-prev"><ChevronLeft /></Action>
          </HintTooltip>
          <span className="grid text-xs tabular-nums text-muted-foreground" data-testid="result-detail-position-slot">
            <span aria-hidden="true" className="invisible col-start-1 row-start-1 whitespace-nowrap">{positionSlotLabel}</span>
            <span className="col-start-1 row-start-1 whitespace-nowrap text-center" data-testid="result-detail-position">{positionLabel}</span>
          </span>
          <HintTooltip name={text('ui.result.detail.next')}>
            <Action type="button" variant="ghost" size="icon-xs" disabled={position.index === position.count - 1} onClick={() => stepGroup(1)} aria-label={text('ui.result.detail.next')} data-testid="result-detail-next"><ChevronRight /></Action>
          </HintTooltip>
        </div>
      ) : null}
      <div
        ref={previewRef}
        className={`relative flex shrink-0 ${displayTarget.kind === 'other' && kind === 'none' ? 'h-24' : 'h-[min(42dvh,24rem)]'} items-center justify-center overflow-hidden rounded-lg ${displayTarget.kind === 'other' ? 'bg-muted' : 'bg-black'} [&:fullscreen]:rounded-none [&:fullscreen_img]:max-h-full [&:fullscreen_video]:max-h-full`}
        data-testid="result-detail-preview"
        data-kind={kind}
      >
        {kind === 'image' ? (
          <img
            src={file}
            alt={displayTarget.title}
            className="max-h-full max-w-full object-contain"
            onLoad={(event) => setMeasured({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
          />
        ) : kind === 'video' ? (
          <video src={file} controls className="max-h-full max-w-full" />
        ) : kind === 'model' ? (
          <ModelViewer3D key={displayTarget.url} src={file} alt={displayTarget.title} taskId={target.taskId} autoRotate className="h-full w-full max-w-none" />
        ) : kind === 'splat' ? (
          <Suspense fallback={<p className="flex h-full w-full items-center justify-center bg-[#1c1d1f] p-4 text-sm text-white" role="status">{text('ui.viewer.splat.loading')}</p>}>
            <GaussianSplatViewer
              key={displayTarget.url}
              src={file}
              alt={displayTarget.title}
              loadingText={text('ui.viewer.splat.loading')}
              failedText={text('ui.viewer.splat.load_failed')}
              retryText={text('ui.viewer.retry')}
              resetText={text('ui.viewer.reset_view')}
              className="aspect-auto h-full min-h-0 w-full"
            />
          </Suspense>
        ) : (
          <p className={`flex items-center gap-2 ${displayTarget.kind === 'other' ? 'p-3' : 'p-6'} text-sm text-muted-foreground`} role="status">
            {status === 'failed' ? <AlertTriangle className="size-4" aria-hidden /> : status === 'ready' ? (displayTarget.kind === 'model' ? <Box className="size-4" aria-hidden /> : displayTarget.kind === 'other' ? <FileText className="size-4" aria-hidden /> : <ImageIcon className="size-4" aria-hidden />) : <Loader2 className="size-4 animate-spin" aria-hidden />}
            {status === 'ready' && displayTarget.kind === 'other' ? schema.task_history.assets.attachments.no_preview : text(status === 'ready' ? 'ui.account.preview_unavailable' : status === 'failed' ? 'ui.result.detail.no_file_failed' : 'ui.result.detail.no_file_running')}
          </p>
        )}
        {kind !== 'none' ? (
          <HintTooltip name={text('ui.account.fullscreen')} className="absolute right-2 top-2">
            <Action type="button" variant="media" size="icon-xs" onClick={fullscreen} aria-label={text('ui.account.fullscreen')} data-testid="result-detail-fullscreen">
              <Maximize2 />
            </Action>
          </HintTooltip>
        ) : null}
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain pr-1 [scrollbar-gutter:stable]" data-testid="result-detail-body">
        {failureText ? (
          <p className="flex items-start gap-1.5 text-xs text-destructive" role="alert" data-testid="result-detail-failure">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden />{failureText}
          </p>
        ) : null}
        {status === 'pending' || status === 'running' ? (
          <p className="flex items-center gap-1.5 text-xs text-muted-foreground" role="status" data-testid="result-detail-live">
            <Loader2 className="size-3.5 animate-spin" aria-hidden />{stateLabel ?? text(status === 'pending' ? 'ui.studio.strip.pending' : 'ui.studio.strip.running')}
          </p>
        ) : null}
        {loadFailed ? <p className="text-xs text-muted-foreground" role="status">{text('ui.result.detail.load_failed')}</p> : null}
        {displayTarget.kind === 'splat' ? <p className="text-xs text-muted-foreground" role="note" data-testid="result-detail-splat-scope">{text('ui.result.detail.splat_scope')}</p> : null}

        {hasTask ? (
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs" data-testid="result-detail-rows">
            {rows.map((row) => (
              <div key={row.key} className="contents" data-testid="result-detail-row" data-key={row.key}>
                <dt className="text-muted-foreground">{row.label}</dt>
                <dd className="min-w-0 break-words">
                  {row.parentKey && target.sessionId === activeSessionId ? (
                    <HintTooltip name={text('ui.result.detail.jump_source')}>
                      <button type="button" className="text-primary underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-ring" data-testid="result-detail-source-jump"
                        onClick={() => {
                          const parent = versions.tiles.find((tile) => tile.key === row.parentKey);
                          const parentObject = parent && target.sessionId ? objectOfTile(parent, target.sessionId) : null;
                          if (parentObject) run(() => openOnStage(parentObject));
                          else if (parent && target.sessionId) useStudioStore.getState().openResultDetail(targetOfTile(parent, target.sessionId, source.label, {}, '', schema));
                        }}>{row.value}</button>
                    </HintTooltip>
                  ) : row.value}
                </dd>
              </div>
            ))}
            {prompt ? (
              <div className="contents" data-testid="result-detail-row" data-key="prompt">
                <dt className="text-muted-foreground">{text('ui.result.detail.field.prompt')}</dt>
                <dd className="max-h-24 min-w-0 overflow-y-auto whitespace-pre-wrap break-words">{prompt}</dd>
              </div>
            ) : null}
          </dl>
        ) : target.detail ? (
          <p className="text-xs text-muted-foreground" data-testid="result-detail-asset-info">{target.detail}</p>
        ) : null}

        {task?.recoverySource && recoveryLabels ? (
          <div className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2 text-xs" data-testid="task-recovery-source-link">
            <span className="text-muted-foreground">{recoveryLabels.source_label}</span>
            <Action type="button" variant="outline" size="sm" onClick={() => openTask(task.recoverySource?.sourceTaskId ?? '', task.sessionId || target.sessionId || '', '')} data-testid="task-recovery-open-source">
              {recoveryLabels.open_source_task}
            </Action>
          </div>
        ) : null}

        {task && target.taskId && status === 'failed' && (task.sessionId || target.sessionId) ? (
          <TaskRecoveryPanel taskId={task.id} sessionId={task.sessionId || target.sessionId || ''} capability={task.capability || target.capability || ''} prompt={prompt} onOpenTask={openTask} />
        ) : null}
        {task && target.taskId && status === 'failed' ? (
          <TaskRefetchPanel taskId={task.id} sessionId={task.sessionId || target.sessionId || ''} capability={task.capability || target.capability || ''} prompt={prompt} onOpenTask={openTask} />
        ) : null}

        {/* A disabled button on its own says nothing on touch screens or to a keyboard: the reason is written out. */}
        {blockedReason ? (
          <p id="result-detail-blocked-reason" role="note" className="text-right text-xs text-muted-foreground" data-testid="result-detail-blocked-reason">{blockedText}</p>
        ) : null}
        <div className="flex flex-wrap items-center justify-end gap-2" role="group" aria-label={text('ui.result.detail.actions')} data-testid="result-detail-actions">
          {actions.copyPrompt ? (
            <Action type="button" variant="outline" size="sm" onClick={() => void copyPrompt()} data-testid="result-detail-copy-prompt"><Copy />{text('ui.result.detail.copy_prompt')}</Action>
          ) : null}
          {actions.regenerate ? (
            <Action type="button" variant="outline" size="sm" onClick={() => void regenerate()} data-testid="result-detail-regenerate"><RotateCw />{text('ui.result.detail.regenerate')}</Action>
          ) : null}
          {actions.sendTo3D && object ? (
            <Action type="button" variant="outline" size="sm" onClick={() => run(() => toThreeD(object))} data-testid="result-detail-to3d"><Box />{text('ui.studio.result.to_3d')}</Action>
          ) : null}
          {splatTool && displayTarget.kind === 'image' && actions.sendTo3D && object ? (
            <HintTooltip name={text('ui.studio.action.to_splat')} reason={splatOfferingsLoading ? text('ui.workspace.models.loading')
              : splatOfferingsError ? text('ui.workspace.models.load_failed')
                : splatCanRun ? undefined : text(splatAvailableCount === 0 ? 'ui.studio.action.splat_unavailable' : 'ui.studio.action.splat_ambiguous')}>
              <Action type="button" variant="outline" size="sm" disabled={splatOfferingsLoading || Boolean(splatOfferingsError) || !splatCanRun}
                onClick={() => { if (!toGaussianSplat(object)) return; closeResultDetail(); }} data-testid="result-detail-to-splat">
                <Box />{text('ui.studio.action.to_splat')}
              </Action>
            </HintTooltip>
          ) : null}
          {actions.sendTo2D && object ? (
            <Action type="button" variant="outline" size="sm" onClick={() => run(() => captureToTwoD(object))} data-testid="result-detail-to2d"><Share2 />{text('ui.result.detail.send_to_2d')}</Action>
          ) : null}
          {blockedReason && conversion === 'sendTo3D' ? (
            <HintTooltip name={text('ui.studio.result.to_3d')} reason={blockedText}>
              <Action type="button" variant="outline" size="sm" disabled aria-describedby="result-detail-blocked-reason" aria-label={`${text('ui.studio.result.to_3d')}: ${blockedText}`} data-testid="result-detail-to3d-blocked"><Box />{text('ui.studio.result.to_3d')}</Action>
            </HintTooltip>
          ) : null}
          {blockedReason && conversion === 'sendTo2D' ? (
            <HintTooltip name={text('ui.result.detail.send_to_2d')} reason={blockedText}>
              <Action type="button" variant="outline" size="sm" disabled aria-describedby="result-detail-blocked-reason" aria-label={`${text('ui.result.detail.send_to_2d')}: ${blockedText}`} data-testid="result-detail-to2d-blocked"><Share2 />{text('ui.result.detail.send_to_2d')}</Action>
            </HintTooltip>
          ) : null}
          {blockedReason ? (
            <HintTooltip name={text('ui.result.detail.open_canvas')} reason={blockedText}>
              <Action type="button" variant="outline" size="sm" disabled aria-describedby="result-detail-blocked-reason" aria-label={`${text('ui.result.detail.open_canvas')}: ${blockedText}`} data-testid="result-detail-open-canvas-blocked"><Eye />{text('ui.result.detail.open_canvas')}</Action>
            </HintTooltip>
          ) : null}
          {actions.openOnCanvas && object ? (
            <Action type="button" variant="outline" size="sm" onClick={() => run(() => openOnStage(object))} data-testid="result-detail-open-canvas"><Eye />{text('ui.result.detail.open_canvas')}</Action>
          ) : null}
        </div>

        {target.taskId && attachments.length ? <ResultAttachments assets={attachments} taskId={target.taskId} ownerId={user?.id} /> : null}

        {/* One download area under the actions. A finished 3D task gets the ZIP and its delivery formats (the same
            panel as the right panel's 格式 tab); any other finished file gets one download row. */}
        {showFormats || singleDownload ? (
          <section className="flex flex-col gap-2 border-t border-border pt-3" aria-labelledby="result-detail-downloads-title" data-testid="result-detail-downloads">
            <h3 id="result-detail-downloads-title" className="text-sm font-semibold">{text('ui.account.download')}</h3>
            {showFormats && target.taskId ? (
              <>
                <ModelArchiveLink taskId={target.taskId} className="self-start" />
                <ModelFormatsPanel taskId={target.taskId} sessionId={target.sessionId} />
              </>
            ) : (
              <a
                href={file}
                download={displayTarget.fileName ?? true}
                className="flex items-center justify-between gap-3 rounded-md border border-border px-3 py-2 text-xs hover:bg-muted"
                data-testid="result-detail-download"
              >
                <span className="min-w-0 truncate">{fileLabel}</span>
                <Download className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
              </a>
            )}
          </section>
        ) : null}
      </div>
    </div>
  );
}

/**
 * A React key for a picture address. A picture sent before #292 is a data address of up to tens of megabytes:
 * React compares keys, which would copy it, so a long address is keyed by its length and ends.
 */
export function addressKey(url: string): string {
  return url.length > 512 ? `${url.length}:${url.slice(0, 64)}:${url.slice(-64)}` : url;
}

/** The dialog around ResultDetail. Outside click, the close button and Esc all close it. */
export function ResultDetailDialog({ target, onClose }: { target: ResultTarget | null; onClose: () => void }) {
  const { text } = useProductSchema();
  const initialTitle = useRef<HTMLHeadingElement | null>(null);
  return (
    <Dialog open={target !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent initialFocus={initialTitle} closeLabel={text('ui.account.close')} overlayClassName="bg-black/70" className="grid h-[min(92dvh,44rem)] grid-cols-1 grid-rows-[auto_minmax(0,1fr)] overflow-hidden sm:max-w-3xl" data-testid="result-detail-dialog">
        {target ? (
          <>
            <DialogHeader className="pr-8">
              <DialogTitle ref={initialTitle} tabIndex={-1} className="truncate">{target.title}</DialogTitle>
              <DialogDescription className="sr-only">{text(target.taskId ? 'ui.result.detail.title' : 'ui.result.detail.asset_title')}</DialogDescription>
            </DialogHeader>
            <ResultDetail key={`${target.taskId ?? ''}:${target.assetId ?? addressKey(target.url)}`} target={target} />
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

/** Mounted once in the studio: opens the dialog for whatever any entry put in the store. */
export function ResultDetailHost() {
  const target = useStudioStore((state) => state.resultDetail);
  const close = useStudioStore((state) => state.closeResultDetail);
  // Leaving the page (studio or 我的资源) closes it, so it does not pop up again on the next page.
  useEffect(() => close, [close]);
  return <ResultDetailDialog target={target} onClose={close} />;
}
