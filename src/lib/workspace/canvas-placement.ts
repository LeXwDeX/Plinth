import type { CurrentObject } from '@/store/workspace-store';

/**
 * #239：把当前对象（Agent 结果、版本条选择等）放到画布上是一次性的事件，不是持续同步。
 * - 每个当前对象（按对象身份）最多放置一次；之后用户删除、涂改的结果保持原样，
 *   重渲染、自动保存、SSE 更新都不会把它放回来。
 * - 打开会话时恢复的当前对象（`restored`）只决定舞台显示什么，不往画布上放东西，
 *   所以刷新后用户删掉的图不会复活。
 * - 用户再次明确选择同一结果会产生新的当前对象，可以再次放置（即“重新加入”）。
 */
export function imagesToPlace(
  selection: CurrentObject | null,
  sessionId: string | null,
  placed: WeakSet<CurrentObject>,
  suppressedTaskIds?: ReadonlySet<string>,
  placedResults?: readonly string[] | null,
): string[] {
  if (!selection || !sessionId || selection.kind !== 'image' || selection.restored) return [];
  if (selection.taskId.startsWith('canvas:') || selection.sessionId !== sessionId) return [];
  // A quiet automatic result for an inpaint task must not add its raw picture; a person's explicit choice
  // (not quiet) still may. The receipt survives the job removal, so a late quiet selection stays suppressed
  // after the task is no longer remembered as an inpaint job.
  const registered = selection.assetId ? `${selection.taskId}#${selection.assetId}` : '';
  const quietSuppressed = selection.quiet === true && (suppressedTaskIds?.has(selection.taskId) === true ||
    (registered !== '' && placedResults?.includes(registered) === true));
  if (quietSuppressed) {
    // The event is spent even when it was suppressed: a later re-render must not reconsider the same quiet object.
    placed.add(selection);
    return [];
  }
  if (placed.has(selection)) return [];
  placed.add(selection);
  return selection.imageUrls ?? [selection.url];
}
