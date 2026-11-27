import { resultKey } from '@/lib/workspace/canvas-auto-join';
import type { VersionTile } from '@/lib/workspace/versions';
import type { CurrentObject } from '@/store/workspace-store';

/**
 * #239：把当前对象（Agent 结果、版本条选择等）放到画布上是一次性的事件，不是持续同步。
 * - 每个当前对象（按对象身份）最多放置一次；之后用户删除、涂改的结果保持原样，
 *   重渲染、自动保存、SSE 更新都不会把它放回来。
 * - 打开会话时恢复的当前对象（`restored`）只决定舞台显示什么，不往画布上放东西，
 *   所以刷新后用户删掉的图不会复活。
 * - #559/#681：只查看（`placement: 'view'`：版本条单击、结果卡单击、选用等一切单击）永不放置，
 *   画板为空时也不放；显式插入（`placement: 'insert'`：双击、「插入到画布」「放入画板」）只放这一张，
 *   回执由调用方写入。
 * - 没有 `placement` 的当前对象只剩 Agent 新结果的一次性自动加入（#239/#262），按整组放置。
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
  // #681: a click never changes the board, an empty one included.
  if (selection.placement === 'view') return [];
  if (selection.placement === 'insert') return [selection.url];
  return selection.imageUrls ?? [selection.url];
}

/**
 * #559: the placement-receipt key of a picture put on the board by hand, as the automatic join registers it
 * (`<version task>#<asset>`, a group's representative task). Null for what the automatic join never places
 * (uploads, Agent crops, screenshots, merged pictures): no receipt is needed for those.
 */
export function placementReceiptKey(tiles: readonly VersionTile[], selection: Pick<CurrentObject, 'assetId'>): string | null {
  const assetId = selection.assetId;
  if (!assetId) return null;
  const tile = tiles.find((entry) => entry.domain === 'image' && entry.origin === 'task'
    && entry.assets.some((asset) => asset.id === assetId && asset.kind === 'image'));
  return tile ? resultKey(tile.taskId, assetId) : null;
}
