'use client';

import { useCallback, useState } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { submitDirectTask, type PendingDirectTask } from '@/components/workspace/canvas-image-actions';
import { reasonOf } from '@/lib/problem-reference';
import { requestReroll, RerollRefused, taskRerollKey } from '@/lib/workspace/task-reroll';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';

/**
 * 「再来一个」 (#242) of a succeeded task, shared by the chat result cards. The
 * first click asks the server for a new execution; a manual-approval session
 * answers "awaiting_decision" and the second click confirms that paid step.
 * Each new request uses its own Idempotency-Key; a pending decision keeps its
 * key so the retry decision and submit stay one identity.
 */
export function useTaskReroll() {
  const { schema, text } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const [rerolling, setRerolling] = useState<Record<string, boolean>>({});
  const [awaiting, setAwaiting] = useState<Record<string, PendingDirectTask>>({});
  const [rerolled, setRerolled] = useState<Record<string, string>>({});

  const rerollTask = useCallback(async (task: TrackedTask) => {
    if (rerolling[task.taskId] || !task.sessionId) return;
    setRerolling((current) => ({ ...current, [task.taskId]: true }));
    const started = (taskId: string) => {
      registerTask({
        taskId, sessionId: task.sessionId, capability: task.capability, prompt: task.prompt,
        progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false,
      });
      setAwaiting((current) => { const next = { ...current }; delete next[task.taskId]; return next; });
      setRerolled((current) => ({ ...current, [task.taskId]: taskId }));
      toast.success(text('ui.workspace.tasks.reroll_started'));
    };
    try {
      const waiting = awaiting[task.taskId];
      if (waiting) {
        started(await submitDirectTask(schema, waiting));
        return;
      }
      const key = taskRerollKey();
      const outcome = await requestReroll(schema, task.taskId, key, text('ui.workspace.tasks.reroll_failed'));
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
      toast.message(text('ui.workspace.tasks.reroll_awaiting'));
    } catch (cause) {
      toast.error(cause instanceof RerollRefused ? cause.message : reasonOf(cause) || text('ui.workspace.tasks.reroll_failed'));
    } finally {
      setRerolling((current) => {
        const next = { ...current };
        delete next[task.taskId];
        return next;
      });
    }
  }, [awaiting, registerTask, rerolling, schema, text]);

  return { rerolling, awaiting, rerolled, rerollTask };
}