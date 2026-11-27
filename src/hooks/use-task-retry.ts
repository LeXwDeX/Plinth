'use client';

import { useCallback } from 'react';
import { toast } from 'sonner';
import { create } from 'zustand';

import { useProductSchema } from '@/components/product-schema-provider';
import { submitDirectTask, type PendingDirectTask } from '@/components/workspace/canvas-image-actions';
import { reasonOf } from '@/lib/problem-reference';
import { requestRetry, RetryRefused } from '@/lib/workspace/task-retry';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';

interface TaskRetryState {
  retrying: Record<string, boolean>;
  awaiting: Record<string, PendingDirectTask>;
  retried: Record<string, string>;
}

/**
 * #665: one retry state for the page, keyed by task: the task center and the chat's failure cards show the same task,
 * so a retry started (or awaiting confirmation) in one place is seen in the other and cannot be submitted twice.
 */
export const useTaskRetryStore = create<TaskRetryState>(() => ({ retrying: {}, awaiting: {}, retried: {} }));

// Taken synchronously before the first await: a double click (or both places at once) gets past no re-render.
const inFlight = new Set<string>();

function without<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
}

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
  const retrying = useTaskRetryStore((state) => state.retrying);
  const awaiting = useTaskRetryStore((state) => state.awaiting);
  const retried = useTaskRetryStore((state) => state.retried);

  const retryTask = useCallback(async (task: TrackedTask) => {
    const id = task.taskId;
    if (inFlight.has(id) || !task.sessionId || useTaskRetryStore.getState().retried[id]) return;
    inFlight.add(id);
    useTaskRetryStore.setState((state) => ({ retrying: { ...state.retrying, [id]: true } }));
    const started = (taskId: string) => {
      registerTask({
        taskId, sessionId: task.sessionId, capability: task.capability, prompt: task.prompt,
        progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false,
      });
      useTaskRetryStore.setState((state) => ({ awaiting: without(state.awaiting, id), retried: { ...state.retried, [id]: taskId } }));
      toast.success(text('ui.workspace.tasks.retry_started'));
    };
    try {
      const waiting = useTaskRetryStore.getState().awaiting[id];
      if (waiting) {
        // Second click: the person confirmed this paid retry, so it goes through the ordinary approval and submit.
        started(await submitDirectTask(schema, waiting));
        return;
      }
      const outcome = await requestRetry(schema, id, text('ui.workspace.tasks.retry_failed'));
      if (outcome.status === 'submitted') {
        started(outcome.taskId);
        return;
      }
      useTaskRetryStore.setState((state) => ({
        awaiting: {
          ...state.awaiting,
          [id]: {
            preparedInputId: outcome.preparedInputId, requestId: outcome.requestId, key: outcome.key,
            sessionId: task.sessionId ?? '', offeringId: '', prompt: task.prompt,
          },
        },
      }));
      toast.message(text('ui.workspace.tasks.retry_awaiting'));
    } catch (cause) {
      toast.error(cause instanceof RetryRefused ? cause.message : reasonOf(cause) || text('ui.workspace.tasks.retry_failed'));
    } finally {
      inFlight.delete(id);
      useTaskRetryStore.setState((state) => ({ retrying: without(state.retrying, id) }));
    }
  }, [registerTask, schema, text]);

  return { retrying, awaiting, retried, retryTask };
}
