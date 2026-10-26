import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import type { TrackedTask } from '@/store/workspace-store';

/**
 * Whether a paid chat step that failed because its source's original task is gone can run again (#326). The
 * server judges it with the rule processing plans use (generation.JudgeSourceRetry) and says so on the owner's
 * task detail as `failure.source_retry`; the card only follows it:
 * - `saved_file`: running the step again uses the saved model file; the regenerate stays and says so;
 * - `source_expired` / `source_incompatible`: running it again cannot succeed; no paid regenerate, and the reason;
 * - empty: nothing known beyond the failure.
 */
export type SourceRetryJudgments = Record<string, string>;

const BLOCKING = new Set(['source_expired', 'source_incompatible']);
const KNOWN = new Set(['saved_file', ...BLOCKING]);

/** The paid steps of a message whose tasks failed as source_expired: the ones to ask the server about. */
export function sourceExpiredSteps(taskIds: readonly string[], tasks: readonly TrackedTask[]): string[] {
  return taskIds.filter((taskId) => tasks.some((task) => task.taskId === taskId && task.failure?.failureClass === 'source_expired'));
}

/** Reads each step's judgment from the owner's task detail; a step that cannot be read is judged on nothing. */
export async function fetchSourceRetries(
  schema: ProductSchema, taskIds: readonly string[], fetcher: typeof fetch = (input, init) => fetch(input, init),
): Promise<SourceRetryJudgments> {
  const entries = await Promise.all(taskIds.map(async (taskId): Promise<[string, string]> => {
    try {
      const response = await fetcher(schema.runtime.http.routes.task_item.replace('{taskID}', encodeURIComponent(taskId)));
      if (!response.ok) return [taskId, ''];
      const projection = projectNativeTaskDetail(await response.json(), schema.task_states, schema.runtime.frontend.percentage_multiplier);
      return [taskId, projection?.failure?.sourceRetry ?? ''];
    } catch {
      return [taskId, ''];
    }
  }));
  return Object.fromEntries(entries);
}

/** A paid regenerate is not offered while one of the turn's steps is certain to fail again. */
export function regenerateBlockedBy(judgments: SourceRetryJudgments | undefined): boolean {
  return Object.values(judgments ?? {}).some((judgment) => BLOCKING.has(judgment));
}

/** The Schema words for one judgment; empty for none or one this page does not know. */
export function sourceRetryText(schema: ProductSchema, judgment: string): string {
  return KNOWN.has(judgment) ? schemaText(schema, `ui.studio.result.source_retry.${judgment}`) : '';
}
