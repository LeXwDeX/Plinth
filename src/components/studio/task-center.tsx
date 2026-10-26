'use client';

/**
 * 顶栏任务中心：按钮（运行数 + 总进度）和右侧抽屉。抽屉不遮挡对话与舞台。
 *
 * 展示 workspace store 中跟踪的任务：状态/进度来自 Schema task_states 与
 * 原生详情轮询（use-task-tracker），错误文案经 error_mapping 词汇解析
 * （resolveErrorText）。抽屉只是任务列表：每个任务一行，整行打开共用的任务详情
 * （请求参数、失败原因、下载入口都在那里），列表里不再放下载或格式入口。
 * 非终态任务可经 Schema 声明的 task_cancel 路由取消；取消响应即原生任务
 * 详情，直接投影回 store，不等下一次轮询。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  Ban,
  Bell,
  Box,
  FileText,
  Loader2,
  ListTodo,
  RotateCw,
  X,
} from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { formatSchemaText } from '@/lib/product-schema';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { openResultDetail } from '@/components/result/result-card-parts';
import { cn } from '@/lib/utils';
import { Surface } from '@/components/ui/surface';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { SideDrawer } from '@/components/ui/side-drawer';
import { useStudioStore } from '@/store/studio-store';
import { usePreferences } from '@/store/preference-store';
import { filterTasks, TASK_FILTERS, type TaskFilter } from '@/lib/workspace/task-filter';
import {
  findTaskState,
  projectNativeTaskDetail,
  resolveErrorText,
} from '@/lib/workspace/task-projection';
import {
  useWorkspaceStore,
  type TrackedTask,
} from '@/store/workspace-store';
import { useChatStore } from '@/store/chat-store';
import { useSessionSkillRuns } from '@/hooks/use-session-skill-runs';
import { useTaskRetry } from '@/hooks/use-task-retry';
import { modelPreviewAsset } from '@/lib/workspace/task-projection';
import { targetOfTaskAssets } from '@/lib/workspace/result-target';
import { canRetryTask, isBalanceFailure } from '@/lib/workspace/task-retry';
import {
  canRequestPermission,
  DESKTOP_QUERY,
  readNoticePermission,
  requestPermissionFromGesture,
} from '@/lib/workspace/completion-notice';

/** Counts shown on the top-bar button; the drawer lists the same tasks. */
export function useTaskSummary() {
  const { schema } = useProductSchema();
  const trackedTasks = useWorkspaceStore((state) => state.trackedTasks);
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const streamingBuffer = useChatStore((state) => activeSessionId ? state.streamingBuffers[activeSessionId] : undefined);
  const pendingMessages = useChatStore((state) => activeSessionId ? state.messagesBySession[activeSessionId] : undefined);
  const tasks = useMemo(
    () => trackedTasks.filter((task) => Boolean(activeSessionId) && task.sessionId === activeSessionId),
    [trackedTasks, activeSessionId],
  );
  const activeTasks = tasks.filter((task) => !task.terminal && !task.notFound);
  const agentPending = Boolean(streamingBuffer) || Boolean(pendingMessages?.some((message) => message.role === 'assistant' && message.isPending));
  const overallProgress = activeTasks.length > 0
    ? Math.round(activeTasks.reduce((total, task) => {
      const maximum = task.progressMaximum > 0 ? task.progressMaximum : schema.runtime.frontend.percentage_multiplier;
      return total + (maximum > 0 ? Math.min(1, Math.max(0, task.progress / maximum)) : 0);
    }, 0) / activeTasks.length * 100)
    : 0;
  return {
    tasks, activeTasks, agentPending,
    agentStatus: streamingBuffer?.genState?.statusText?.trim() ?? '',
    // Running tasks only: a turn that waits on a person (an approval) is not work in progress.
    liveCount: activeTasks.length,
    listedCount: tasks.length + (agentPending ? 1 : 0),
    overallProgress,
  };
}

export function TaskCenterButton() {
  const { schema, text } = useProductSchema();
  const { activeTasks, liveCount, listedCount, overallProgress } = useTaskSummary();
  const open = useStudioStore((state) => state.taskDrawerOpen);
  const setOpen = useStudioStore((state) => state.setTaskDrawerOpen);
  const title = text('ui.workspace.tasks.title');
  const openLabel = text('ui.workspace.tasks.open');
  return (
    <HintTooltip name={openLabel} className="shrink-0">
      <button
        type="button"
        aria-label={listedCount > 0 ? formatSchemaText(text('ui.common.parenthetical'), {
          text: openLabel,
          detail: activeTasks.length > 0
            ? `${listedCount}${text('ui.common.clause_separator')}${text('ui.workspace.tasks.progress')} ${overallProgress}${schema.runtime.frontend.percentage_suffix}`
            : String(listedCount),
        }) : openLabel}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        data-testid="workspace-task-center"
        className={cn(
          'relative flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary',
          open && 'bg-primary/15 text-primary',
        )}
      >
        <ListTodo className="size-4" aria-hidden="true" />
        <span>{title}</span>
        {liveCount > 0 ? (
          <span className="flex min-h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-semibold text-primary-foreground" data-testid="workspace-active-count" aria-hidden="true">
            {liveCount}
          </span>
        ) : null}
        {activeTasks.length > 0 ? (
          <span className="h-1 w-6 overflow-hidden rounded-full bg-muted" aria-hidden="true">
            <span className="block h-full rounded-full bg-primary transition-[width]" style={{ width: `${overallProgress}%` }} />
          </span>
        ) : null}
      </button>
    </HintTooltip>
  );
}

/** The task list as a right drawer inside the studio body. */
export function TaskDrawer() {
  const { schema, text } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { runs: skillRuns, error: skillRunsError, isLoading: skillRunsLoading, reload: reloadSkillRuns } = useSessionSkillRuns(activeSessionId);
  // FEA-02：只显示当前会话的任务；旧会话轮次（切换会话后仍在完成的）不得串入
  // 新会话的任务列表。
  const { tasks: allTasks, agentPending, agentStatus: liveStatus } = useTaskSummary();
  // The filter is remembered in this browser (preference store); the agent and Skill rows belong to "all" and "in progress".
  const taskFilter = usePreferences((state) => state.taskFilter);
  const setTaskFilter = usePreferences((state) => state.setTaskFilter);
  const tasks = useMemo(() => filterTasks(allTasks, taskFilter), [allTasks, taskFilter]);
  const showLiveRows = taskFilter !== 'failed';
  const dismissTask = useWorkspaceStore((state) => state.dismissTask);
  const applyTaskDetail = useWorkspaceStore((state) => state.applyTaskDetail);
  const [canceling, setCanceling] = useState<Record<string, boolean>>({});
  const [noticePermission, setNoticePermission] = useState(readNoticePermission);
  const [noticeDesktop, setNoticeDesktop] = useState(false);
  const [noticeRequesting, setNoticeRequesting] = useState(false);
  const noticeRequestPending = useRef(false);
  const { retrying, awaiting, retried, retryTask } = useTaskRetry();
  // Cancel asks twice in place: a running task may already be billed upstream.
  const [confirmingCancel, setConfirmingCancel] = useState<Record<string, boolean>>({});
  const open = useStudioStore((state) => state.taskDrawerOpen);
  const setOpen = useStudioStore((state) => state.setTaskDrawerOpen);

  useEffect(() => {
    const desktopQuery = window.matchMedia(DESKTOP_QUERY);
    const syncPermission = () => {
      setNoticeDesktop(desktopQuery.matches);
      setNoticePermission(readNoticePermission());
    };
    syncPermission();
    desktopQuery.addEventListener('change', syncPermission);
    window.addEventListener('focus', syncPermission);
    document.addEventListener('visibilitychange', syncPermission);
    return () => {
      desktopQuery.removeEventListener('change', syncPermission);
      window.removeEventListener('focus', syncPermission);
      document.removeEventListener('visibilitychange', syncPermission);
    };
  }, []);

  const requestNoticePermission = useCallback(() => {
    if (typeof Notification === 'undefined' || noticeRequestPending.current) return;
    let result: Promise<NotificationPermission> | null;
    try {
      result = requestPermissionFromGesture(
        () => {
          const permission = readNoticePermission();
          const desktop = window.matchMedia(DESKTOP_QUERY).matches;
          setNoticePermission(permission);
          setNoticeDesktop(desktop);
          return {
            desktop,
            supported: permission !== 'unsupported',
            permission,
            pending: noticeRequestPending.current,
          };
        },
        () => {
          noticeRequestPending.current = true;
          setNoticeRequesting(true);
        },
        () => Notification.requestPermission(),
      );
    } catch {
      noticeRequestPending.current = false;
      setNoticeRequesting(false);
      setNoticePermission(readNoticePermission());
      return;
    }
    if (!result) return;
    void result
      .then(setNoticePermission)
      .catch(() => setNoticePermission(readNoticePermission()))
      .finally(() => {
        noticeRequestPending.current = false;
        setNoticeRequesting(false);
      });
  }, [noticeDesktop, noticePermission]);

  const ui = useMemo(
    () => ({
      title: text('ui.workspace.tasks.title'),
      empty: text('ui.workspace.tasks.empty'),
      progress: text('ui.workspace.tasks.progress'),
      resourcesPending: text('ui.workspace.tasks.resources_pending'),
      errorTitle: text('ui.workspace.tasks.error_title'),
      retryable: text('ui.workspace.tasks.retryable'),
      dismiss: text('ui.workspace.tasks.dismiss'),
      notFound: text('ui.workspace.tasks.not_found'),
      cancel: text('ui.workspace.tasks.cancel'),
      cancelConfirm: text('ui.workspace.tasks.cancel_confirm'),
      cancelFailed: text('ui.workspace.tasks.cancel_failed'),
      cancelConfirmAction: text('ui.studio.tasks.cancel_confirm_action'),
      cancelKeep: text('ui.studio.tasks.cancel_keep'),
      retry: text('ui.workspace.tasks.retry'),
      retryConfirm: text('ui.workspace.tasks.retry_confirm'),
      retryAwaiting: text('ui.workspace.tasks.retry_awaiting'),
      open: text('ui.workspace.tasks.open'),
      close: text('ui.workspace.tasks.close'),
      agentPreparing: text('ui.workspace.tasks.agent_preparing'),
      skillRunsLoadFailed: text('ui.workspace.tasks.skill_runs_load_failed'),
      noticeEnable: text('ui.workspace.notice.enable'),
      noticeRequesting: text('ui.workspace.notice.requesting'),
      noticeEnabled: text('ui.workspace.notice.enabled'),
      noticeDenied: text('ui.workspace.notice.denied'),
    }),
    [text],
  );

  const noticeControl = noticeDesktop && noticePermission !== 'unsupported' ? (
    <div className="flex min-h-8 items-center justify-between gap-3 rounded-lg bg-muted/50 px-2.5 py-1.5 text-xs" data-testid="workspace-notice-permission">
      {noticePermission === 'granted' ? (
        <p role="status" className="text-muted-foreground">{ui.noticeEnabled}</p>
      ) : noticePermission === 'denied' ? (
        <p role="status" className="text-muted-foreground">{ui.noticeDenied}</p>
      ) : (
        <Action
          type="button"
          variant="outline"
          size="xs"
          disabled={!canRequestPermission({
            desktop: noticeDesktop,
            supported: true,
            permission: noticePermission,
            pending: noticeRequesting,
          })}
          onClick={requestNoticePermission}
          data-testid="workspace-notice-enable"
        >
          <Bell className="size-3.5" aria-hidden="true" />
          {noticeRequesting ? ui.noticeRequesting : ui.noticeEnable}
        </Action>
      )}
    </div>
  ) : null;

  const capabilityLabels = useMemo(
    () =>
      new Map(
        schema.capabilities.map((capability) => [
          capability.id,
          capability.label_cn || capability.label,
        ]),
      ),
    [schema],
  );

  const errorText = useCallback(
    (task: TrackedTask): string | null => {
      const detail = task.failure?.detail || task.liveError || null;
      if (!detail && !task.notFound) return null;
      if (task.notFound) return ui.notFound;
      // A balance failure reads as the Schema's own message, whatever detail came with it.
      if (isBalanceFailure(task.failure)) return text('failure.insufficient_balance');
      return resolveErrorText(schema.content.messages, detail, ui.errorTitle);
    },
    [schema.content.messages, text, ui.errorTitle, ui.notFound],
  );

  const stateChip = useCallback(
    (task: TrackedTask) => {
      const presentation = findTaskState(schema.task_states, task.state);
      if (!presentation) return null;
      return {
        label: presentation.label,
        className: presentation.color,
      };
    },
    [schema.task_states],
  );

  const progressText = useCallback(
    (task: TrackedTask) => {
      const maximum =
        task.progressMaximum > 0
          ? task.progressMaximum
          : schema.runtime.frontend.percentage_multiplier;
      const ratio = maximum > 0 ? task.progress / maximum : 0;
      const percent = Math.round(ratio * 100);
      return `${percent}${schema.runtime.frontend.percentage_suffix}`;
    },
    [
      schema.runtime.frontend.percentage_multiplier,
      schema.runtime.frontend.percentage_suffix,
    ],
  );

  const barWidth = useCallback((task: TrackedTask) => {
    const maximum = task.progressMaximum > 0 ? task.progressMaximum : 100;
    const percent = Math.min(
      100,
      Math.max(0, Math.round((task.progress / maximum) * 100)),
    );
    return `${percent}%`;
  }, []);

  const agentStatus = liveStatus || ui.agentPreparing;
  const trackedTaskIds = new Set(allTasks.map((task) => task.taskId));
  // SkillRun phases are durable checkpoints. A grant may be rejected and a
  // bound Task may fail while its phase remains unchanged.
  const visibleSkillSteps = !showLiveRows ? [] : skillRuns.flatMap((run) => run.steps
    .filter((step) => !step.taskId || !trackedTaskIds.has(step.taskId))
    .map((step) => ({ ...step, runId: run.id })));
  const cancelTask = useCallback(
    async (task: TrackedTask) => {
      if (canceling[task.taskId]) return;
      if (!confirmingCancel[task.taskId]) {
        setConfirmingCancel((current) => ({ ...current, [task.taskId]: true }));
        return;
      }
      setConfirmingCancel((current) => { const next = { ...current }; delete next[task.taskId]; return next; });
      setCanceling((current) => ({ ...current, [task.taskId]: true }));
      try {
        const route = schema.runtime.http.routes.task_cancel.replace(
          '{taskID}',
          encodeURIComponent(task.taskId),
        );
        const response = await fetch(route, { method: 'POST' });
        if (!response.ok) throw new Error(`cancel ${response.status}`);
        const projection = projectNativeTaskDetail(
          await response.json(),
          schema.task_states,
          schema.runtime.frontend.percentage_multiplier,
        );
        if (projection) applyTaskDetail(task.taskId, projection, useChatStore.getState().activeSessionId);
      } catch {
        toast.error(ui.cancelFailed);
      } finally {
        setCanceling((current) => {
          const next = { ...current };
          delete next[task.taskId];
          return next;
        });
      }
    },
    [applyTaskDetail, canceling, confirmingCancel, schema, ui.cancelFailed],
  );

  /** Opens the shared 任务详情 on a task (a click anywhere on its row). */
  const openTaskDetail = (task: TrackedTask) => {
    openResultDetail(targetOfTaskAssets({
      taskId: task.taskId,
      ...(task.sessionId ? { sessionId: task.sessionId } : {}),
      title: task.prompt || (capabilityLabels.get(task.capability) ?? task.capability),
      capability: task.capability,
      status: !task.terminal ? 'running' : task.failure || task.liveError || task.notFound ? 'failed' : 'ready',
      ...(task.prompt ? { prompt: task.prompt } : {}),
    }, task.downloadables));
  };

  const taskList = (
    <div className="flex min-h-0 flex-col gap-2 overflow-y-auto p-3">
      {noticeControl}
      <div role="group" aria-label={text('ui.workspace.tasks.filter.label')} className="flex gap-1" data-testid="workspace-task-filter">
        {TASK_FILTERS.map((value: TaskFilter) => (
          <Action
            key={value}
            type="button"
            variant={taskFilter === value ? 'secondary' : 'ghost'}
            size="xs"
            aria-pressed={taskFilter === value}
            onClick={() => setTaskFilter(value)}
            data-testid={`workspace-task-filter-${value}`}
          >
            {text(`ui.workspace.tasks.filter.${value}`)}
          </Action>
        ))}
      </div>
      {agentPending && showLiveRows ? <Surface as="article" variant="inset" radius="xl" padding="md" className="flex items-center gap-3" data-testid="workspace-agent-progress">
        <Loader2 className="size-4 shrink-0 animate-spin text-primary" aria-hidden="true" />
        <span className="min-w-0 truncate text-sm">{agentStatus}</span>
      </Surface> : null}
      {/* A failed load is reported under every filter, with its retry: the 失败 filter would otherwise hide the very failure. */}
      {skillRunsError ? <p role="alert" className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-xs text-destructive" data-testid="workspace-task-load-error">
        <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
        <span className="min-w-0 flex-1">{ui.skillRunsLoadFailed}</span>
        <Action type="button" variant="outline" size="xs" onClick={reloadSkillRuns} data-testid="workspace-task-load-retry">
          <RotateCw className="h-3 w-3" aria-hidden="true" />{ui.retry}
        </Action>
      </p> : null}
      {skillRunsLoading && !skillRunsError && showLiveRows ? <p role="status" className="flex items-center gap-2 px-2 py-1.5 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />{ui.title}
      </p> : null}
      {visibleSkillSteps.map((step) => <Surface
        as="article"
        variant="inset"
        radius="xl"
        padding="md"
        key={`${step.runId}:${step.key}`}
        className="flex items-center justify-between gap-3"
        data-testid="workspace-skill-step"
      >
        <span className="min-w-0 truncate text-sm font-medium">{step.label}</span>
        <span className="flex shrink-0 flex-col items-end gap-0.5 text-xs text-muted-foreground">
          <span className="rounded bg-muted px-1.5 py-0.5">{text('ui.workspace.tasks.skill_checkpoint')}</span>
          <span>{text(`ui.workspace.tasks.skill_phase.${step.phase}`)}</span>
        </span>
      </Surface>)}
      {tasks.length === 0 && !(agentPending && showLiveRows) && visibleSkillSteps.length === 0 && !(skillRunsLoading && showLiveRows) && !skillRunsError
        ? <p className="py-6 text-center text-sm text-muted-foreground" data-testid="workspace-task-empty">{allTasks.length > 0 ? text('ui.workspace.tasks.filter.empty') : ui.empty}</p> : null}
        {tasks.map((task) => {
          const chip = stateChip(task);
          const error = errorText(task);
          const running = !task.terminal && !task.notFound;
          const capabilityLabel = capabilityLabels.get(task.capability) ?? task.capability;
          const name = task.prompt !== '' ? task.prompt : capabilityLabel;
          const hasModel = task.downloadables.some((asset) => asset.kind === 'model');
          const thumb = hasModel
            ? modelPreviewAsset(task.downloadables)
            : task.downloadables.find((asset) => asset.kind === 'image');
          // #290: a 56 px row picture is the downscaled preview of a task picture.
          const thumbURL = thumb ? previewAssetUrl(thumb.url) : '';
          // One row per task. The whole row opens the shared 任务详情 (request parameters, failure,
          // download); only the row's own buttons take pointer events back and keep their own action.
          return (
            <Surface
              as="article"
              variant="inset"
              radius="xl"
              padding="md"
              key={task.taskId}
              className="relative flex w-full min-w-0 shrink-0 gap-3 transition-colors hover:bg-muted/60"
              data-testid="workspace-task-card"
            >
              <button
                type="button"
                onClick={() => openTaskDetail(task)}
                aria-label={`${name} · ${text('ui.result.card.detail')}`}
                className="absolute inset-0 rounded-xl focus-visible:outline-2 focus-visible:outline-ring"
                data-testid="workspace-task-open"
              />
              <div className="pointer-events-none relative shrink-0">
                {thumbURL ? (
                  <img
                    src={thumbURL}
                    alt={name}
                    className="size-14 shrink-0 rounded-lg border border-border object-cover"
                  />
                ) : (
                  <span className="flex size-14 shrink-0 items-center justify-center rounded-lg border border-border bg-muted">
                    {hasModel && !running
                      ? <Box aria-hidden className="size-4 text-muted-foreground" />
                      : task.terminal && task.downloadables.some(asset => asset.kind === 'other') ? <FileText aria-hidden className="size-4 text-muted-foreground" data-testid="workspace-task-attachment-icon" />
                      : <Loader2 aria-hidden className={cn('size-4 text-muted-foreground', running && 'animate-spin')} />}
                  </span>
                )}
              </div>
              <div className="pointer-events-none relative min-w-0 flex-1">
              <header className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{name}</p>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">{capabilityLabel}</p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  {running ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />
                  ) : null}
                  {chip ? (
                    <span
                      className={cn(
                        'rounded-full px-2 py-0.5 text-xs',
                        chip.className,
                      )}
                    >
                      {chip.label}
                    </span>
                  ) : null}
                  {running ? (
                    <HintTooltip name={ui.cancel} className="pointer-events-auto">
                      <Action
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        aria-label={ui.cancel}
                        disabled={canceling[task.taskId] === true}
                        onClick={() => void cancelTask(task)}
                        className="pointer-events-auto text-muted-foreground"
                      >
                        <Ban className="h-3.5 w-3.5" />
                      </Action>
                    </HintTooltip>
                  ) : null}
                  {task.terminal ? (
                    <HintTooltip name={ui.dismiss} className="pointer-events-auto">
                      <Action
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        aria-label={ui.dismiss}
                        onClick={() => dismissTask(task.taskId)}
                        className="pointer-events-auto text-muted-foreground"
                      >
                        <X className="h-3.5 w-3.5" />
                      </Action>
                    </HintTooltip>
                  ) : null}
                </div>
              </header>

              <div
                className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"
                role="progressbar"
                aria-label={ui.progress}
                aria-valuenow={task.progress}
                aria-valuemin={0}
                aria-valuemax={task.progressMaximum}
              >
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: barWidth(task) }}
                />
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                {progressText(task)}
                {task.resourcesPending ? ` · ${ui.resourcesPending}` : ''}
              </p>

              {confirmingCancel[task.taskId] ? (
                <div role="alert" className="mt-2 flex flex-col gap-1.5 rounded-md bg-warning/10 p-2 text-xs text-warning" data-testid="workspace-task-cancel-confirm">
                  <span>{ui.cancelConfirm}</span>
                  <span className="flex gap-1.5">
                    <Action type="button" variant="destructive" size="xs" className="pointer-events-auto" onClick={() => void cancelTask(task)}>{ui.cancelConfirmAction}</Action>
                    <Action type="button" variant="ghost" size="xs" className="pointer-events-auto" onClick={() => setConfirmingCancel((current) => { const next = { ...current }; delete next[task.taskId]; return next; })}>{ui.cancelKeep}</Action>
                  </span>
                </div>
              ) : null}

              {error ? (
                <p className="mt-2 flex items-start gap-1.5 text-xs text-destructive">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>
                    {error}
                    {task.failure?.retryable && !isBalanceFailure(task.failure) ? formatSchemaText(text('ui.common.parenthetical'), { text: '', detail: ui.retryable }) : ''}
                  </span>
                </p>
              ) : null}

              {canRetryTask(task) && !retried[task.taskId] ? (
                <div className="mt-2 flex flex-col items-start gap-1">
                  {awaiting[task.taskId] ? <p className="text-xs text-muted-foreground">{ui.retryAwaiting}</p> : null}
                  <Action
                    type="button"
                    variant="outline"
                    size="xs"
                    className="pointer-events-auto"
                    disabled={retrying[task.taskId] === true}
                    onClick={() => void retryTask(task)}
                    data-testid="workspace-task-retry"
                  >
                    <RotateCw className="h-3 w-3" aria-hidden="true" />
                    {awaiting[task.taskId] ? ui.retryConfirm : ui.retry}
                  </Action>
                </div>
              ) : null}
              </div>
            </Surface>
          );
        })}
    </div>
  );

  if (!open) return null;
  return (
    <SideDrawer title={ui.title} closeLabel={ui.close} onClose={() => setOpen(false)} testId="workspace-task-drawer">
      {taskList}
    </SideDrawer>
  );
}
