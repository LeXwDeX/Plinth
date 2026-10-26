'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Loader2, RotateCw, X } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { formatSchemaText } from '@/lib/product-schema';
import { useTaskRetry } from '@/hooks/use-task-retry';
import { canRetryTask, isBalanceFailure } from '@/lib/workspace/task-retry';
import { resolveErrorText } from '@/lib/workspace/task-projection';
import { chatTaskCards, elapsedClockNeeded, elapsedSeconds, formatElapsed, progressPercent } from '@/lib/workspace/task-progress';
import { cn } from '@/lib/utils';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';

const CLOCK_MILLISECONDS = 1000;

/**
 * 对话里的任务卡：每个进行中的任务一张进度卡（百分比和已用时间，3D 这样的长任务
 * 也能看出还在走），本页看着它跑、后来失败的任务一张失败说明。余额不足只说明
 * 原因并请联系管理员，不给重试：钱没补上之前重试只会再失败一次。
 *
 * `part` 选择渲染哪一部分（#244）：进行中的卡悬浮在消息区底部、不参与消息滚动；
 * 失败说明留在消息流里。两部分各自观察同一组任务，失败只报给本页看着跑过的任务。
 */
export function TaskStatusCards({ part }: { part: 'running' | 'failed' }) {
  const { schema, text } = useProductSchema();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const tracked = useWorkspaceStore((state) => state.trackedTasks);
  const { retrying, awaiting, retried, retryTask } = useTaskRetry();
  const [now, setNow] = useState(() => Date.now());
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set());
  // When this page first saw each task running; a failure is only announced for a task it watched.
  const seen = useRef(new Map<string, number>());
  for (const task of tracked) {
    if (!task.terminal && !task.notFound && !seen.current.has(task.taskId)) seen.current.set(task.taskId, Date.now());
  }
  const watched = useMemo(() => new Set(seen.current.keys()), [tracked]); // eslint-disable-line react-hooks/exhaustive-deps -- `seen` is filled above on every render
  const cards = useMemo(() => chatTaskCards(tracked, sessionId, watched, dismissed), [tracked, sessionId, watched, dismissed]);

  const clockNeeded = elapsedClockNeeded(part, cards);
  useEffect(() => {
    if (!clockNeeded) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), CLOCK_MILLISECONDS);
    return () => window.clearInterval(timer);
  }, [clockNeeded]);

  const capabilityLabel = (task: TrackedTask) => {
    const capability = schema.capabilities.find((item) => item.id === task.capability);
    return capability?.label_cn || capability?.label || task.capability;
  };
  const failureText = (task: TrackedTask) => {
    // A balance failure reads as the Schema's own message, whatever detail came with it.
    if (isBalanceFailure(task.failure)) return text('failure.insufficient_balance');
    return resolveErrorText(schema.content.messages, task.failure?.detail || task.liveError, text('ui.workspace.tasks.error_title'));
  };
  const suffix = schema.runtime.frontend.percentage_suffix;

  if (part === 'running') {
    if (cards.running.length === 0) return null;
    return (
      <div className="flex max-h-full flex-col gap-1.5 overflow-y-auto overscroll-contain rounded-lg bg-sidebar" data-testid="chat-task-running">
        {cards.running.map((task) => {
        const percent = progressPercent(task, schema.runtime.frontend.percentage_multiplier);
        return (
          <article
            key={task.taskId}
            data-testid="chat-task-progress"
            data-task-id={task.taskId}
            aria-label={text('ui.studio.task.list')}
            className="flex shrink-0 flex-col gap-1.5 rounded-lg border border-border bg-panel-raised p-3 text-xs shadow-md"
          >
            <header className="flex items-center gap-2">
              <Loader2 aria-hidden className="size-3.5 shrink-0 animate-spin text-primary" />
              <span className="min-w-0 flex-1 truncate text-[13px] font-medium">
                {formatSchemaText(text('ui.studio.task.progress_title'), { capability: capabilityLabel(task) })}
              </span>
              <span className="tabular-nums font-medium" data-testid="chat-task-percent">{percent}{suffix}</span>
            </header>
            <div
              role="progressbar"
              aria-label={text('ui.workspace.tasks.progress')}
              aria-valuenow={percent}
              aria-valuemin={0}
              aria-valuemax={100}
              className="h-1.5 overflow-hidden rounded-full bg-muted"
            >
              <div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${percent}%` }} />
            </div>
            <p className="tabular-nums text-muted-foreground" data-testid="chat-task-elapsed">
              {formatSchemaText(text('ui.studio.task.elapsed'), { time: formatElapsed(elapsedSeconds(task, now, seen.current.get(task.taskId))) })}
            </p>
          </article>
        );
        })}
      </div>
    );
  }
  if (cards.failed.length === 0) return null;
  return (
    <div className="mt-2 flex flex-col gap-2 px-1" data-testid="chat-task-cards">
      {cards.failed.map((task) => (
        <article
          key={task.taskId}
          role="alert"
          data-testid="chat-task-failure"
          data-task-id={task.taskId}
          data-failure-class={task.failure?.failureClass ?? ''}
          className={cn('flex items-start gap-2 rounded-lg border border-destructive/40 bg-panel-raised p-3 text-xs')}
        >
          <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium">
              {formatSchemaText(text('ui.studio.task.failed_title'), { capability: capabilityLabel(task) })}
            </p>
            <p className="mt-0.5 text-muted-foreground [overflow-wrap:anywhere]">{failureText(task)}</p>
            {/* Retry re-runs the frozen input; never offered on insufficient balance (canRetryTask). */}
            {canRetryTask(task) && !retried[task.taskId] ? (
              <div className="mt-2 flex flex-col items-start gap-1">
                {awaiting[task.taskId] ? <p className="text-muted-foreground">{text('ui.workspace.tasks.retry_awaiting')}</p> : null}
                <Action
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={retrying[task.taskId] === true}
                  onClick={() => void retryTask(task)}
                  data-testid="chat-task-retry"
                >
                  <RotateCw aria-hidden />{awaiting[task.taskId] ? text('ui.workspace.tasks.retry_confirm') : text('ui.workspace.tasks.retry')}
                </Action>
              </div>
            ) : null}
          </div>
          <HintTooltip name={text('ui.workspace.tasks.dismiss')} className="shrink-0">
            <Action
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={text('ui.workspace.tasks.dismiss')}
              onClick={() => setDismissed((current) => new Set(current).add(task.taskId))}
              className="text-muted-foreground"
            >
              <X className="size-3.5" />
            </Action>
          </HintTooltip>
        </article>
      ))}
    </div>
  );
}
