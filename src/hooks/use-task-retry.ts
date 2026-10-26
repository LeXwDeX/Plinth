'use client';

import { useCallback, useState } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { submitDirectTask, type PendingDirectTask } from '@/components/workspace/canvas-image-actions';
import { reasonOf } from '@/lib/problem-reference';
import { requestRetry, RetryRefused } from '@/lib/workspace/task-retry';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';

/**
 * The 重试 of a failed task, shared by the task center and the failure cards in the chat. The server
 * re-runs the frozen input of the original task; a manual-approval session answers "awaiting_decision"
 * and the second click is the person's confirmation of the (possibly paid) retry.
 * Whether a task may be retried at all (not on insufficient balance) is task-retry's canRetryTask.
 */
export function useTaskRetry() {
  const { schema, text } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  // retrying stops double clicks; awaiting holds a retry that needs the person's confirmation;
  // retried records the original task that was resubmitted, whose button no longer appears.
  const [retrying, setRetrying] = useState<Record<string, boolean>>({});
  const [awaiting, setAwaiting] = useState<Record<string, PendingDirectTask>>({});
  const [retried, setRetried] = useState<Record<string, string>>({});

  const retryTask = useCallback(async (task: TrackedTask) => {
    if (retrying[task.taskId] || !task.sessionId) return;
    setRetrying((current) => ({ ...current, [task.taskId]: true }));
    const started = (taskId: string) => {
      registerTask({
        taskId, sessionId: task.sessionId, capability: task.capability, prompt: task.prompt,
        progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false,
      });
      setAwaiting((current) => { const next = { ...current }; delete next[task.taskId]; return next; });
      setRetried((current) => ({ ...current, [task.taskId]: taskId }));
      toast.success(text('ui.workspace.tasks.retry_started'));
    };
    try {
      const waiting = awaiting[task.taskId];
      if (waiting) {
        // Second click: the person confirmed this paid retry, so it goes through the ordinary approval and submit.
        started(await submitDirectTask(schema.runtime.http.routes, waiting));
        return;
      }
      const outcome = await requestRetry(schema, task.taskId, text('ui.workspace.tasks.retry_failed'));
      if (outcome.status === 'submitted') {
        started(outcome.taskId);
        return;
      }
      setAwaiting((current) => ({
        ...current,
        [task.taskId]: {
          preparedInputId: outcome.preparedInputId, requestId: outcome.requestId, key: outcome.key,
          sessionId: task.sessionId ?? '', offeringId: '', prompt: task.prompt,
        },
      }));
      toast.message(text('ui.workspace.tasks.retry_awaiting'));
    } catch (cause) {
      toast.error(cause instanceof RetryRefused ? cause.message : reasonOf(cause) || text('ui.workspace.tasks.retry_failed'));
    } finally {
      setRetrying((current) => {
        const next = { ...current };
        delete next[task.taskId];
        return next;
      });
    }
  }, [awaiting, registerTask, retrying, schema, text]);

  return { retrying, awaiting, retried, retryTask };
}
