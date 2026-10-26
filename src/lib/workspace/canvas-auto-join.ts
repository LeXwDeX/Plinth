import type { VersionTile } from '@/lib/workspace/versions';

/**
 * #262 / #239：会话里完成的图片结果默认进入本会话画布一次；之后用户删除、涂改都以用户为准。
 *
 * 画布保存一份「已放置结果」清单（与节点同一 revision 持久化，服务端视为不透明字符串）：
 * - 结果键 `<taskId>#<assetId>`：已经放置过（无论之后是否被删除）的结果，不会再自动放置。
 * - 水位 `since:<ISO 时间>`：只有创建时间不早于水位的任务结果才会自动加入，
 *   这样旧画布第一次启用时、或翻出更早的版本时，不会把历史结果铺满画布。
 * 清单为 null 表示旧画布尚未登记：只登记（seed），不放置任何东西。
 */
export const SINCE_PREFIX = 'since:';

export interface AutoJoinCandidate {
  key: string;
  taskId: string;
  url: string;
  /** The server's frozen operation for the task; an inpaint result is placed only by its patch job. */
  operation?: string;
}

export interface AutoJoinOptions {
  /** Tasks whose raw result is placed by a dedicated UI path (an inpaint patch) and must not be auto-added. */
  suppressTaskIds?: ReadonlySet<string>;
}

export interface AutoJoinPlan {
  /** 需要写回的完整清单；null 表示不变。 */
  placed: string[] | null;
  /**
   * 可以立即写回的登记项：旧画布的水位种子，以及由专用路径落层的 raw inpaint 结果。
   * 普通结果只有真正落到画布上（或被确认已在画布上）后才由调用方登记，避免加载失败或切换会话
   * 留下“有回执、无节点”的永久记录。
   */
  register: string[];
  /** 要放到画布上的结果（按版本顺序）。 */
  place: AutoJoinCandidate[];
}

export function resultKey(taskId: string, assetId: string): string {
  return `${taskId}#${assetId}`;
}

/** 本会话中可以自动加入 2D 画布的结果：已完成的任务图片（不含上传、截图、合并图和 3D）。 */
export function imageResults(tiles: readonly VersionTile[]): Array<AutoJoinCandidate & { createdAt: string }> {
  return tiles.flatMap((tile) => tile.domain === 'image' && tile.status === 'ready' && tile.origin === 'task'
    ? tile.assets.filter((asset) => asset.kind === 'image' && asset.url).map((asset) => ({
      key: resultKey(tile.taskId, asset.id), taskId: tile.taskId, url: asset.url,
      operation: tile.operation, createdAt: tile.createdAt,
    }))
    : []);
}

/**
 * 时间按时刻比较，不按字符串：本地进行中的任务用 `toISOString`（3 位毫秒），服务端用 Go RFC3339Nano
 * （小数位数不定，可为 0 位或带时区偏移），字符串比较会把 "…00.123456Z" 排在 "…00.123Z" 之前。
 * 无法解析（含空串）视为最早。
 */
function instant(value: string): number {
  const time = Date.parse(value);
  return Number.isNaN(time) ? -Infinity : time;
}

function watermark(placed: readonly string[]): string {
  const since = placed.find((entry) => entry.startsWith(SINCE_PREFIX));
  return since ? since.slice(SINCE_PREFIX.length) : '';
}

export function planAutoJoin(
  tiles: readonly VersionTile[],
  placed: readonly string[] | null,
  options: AutoJoinOptions = {},
): AutoJoinPlan {
  const results = imageResults(tiles);
  const suppressed = options.suppressTaskIds;
  if (placed === null) {
    // 旧画布第一次登记：已完成的结果都记为已放置；仍在进行的任务完成后照常加入。
    const running = tiles.filter((tile) => tile.status === 'pending' || tile.status === 'running').map((tile) => tile.createdAt);
    const all = tiles.map((tile) => tile.createdAt).filter(Boolean);
    const since = running.length > 0 ? running.reduce((a, b) => (instant(b) < instant(a) ? b : a))
      : all.length > 0 ? all.reduce((a, b) => (instant(b) > instant(a) ? b : a)) : new Date(0).toISOString();
    const seed = [SINCE_PREFIX + since, ...results.map((result) => result.key)];
    return { placed: seed, register: seed, place: [] };
  }
  const known = new Set(placed);
  const since = instant(watermark(placed));
  // A dedicated result path (inpaint) registers its raw keys as placed without putting raw pictures on the
  // board. This is a UI placement receipt, not a second task lifecycle: the Task still owns its result.
  // The server's frozen operation covers retries: their new task has no local job, but the lineage still
  // reports `inpaint`, so a result whose size cannot be proven is never auto-added as a raw layer.
  // The lineage may repeat a task or an asset while it is still settling; each result is placed and
  // registered once per plan.
  const isSuppressed = (result: AutoJoinCandidate) => result.operation === 'inpaint' || suppressed?.has(result.taskId) === true;
  const suppressedKeys = [...new Set(results
    .filter((result) => isSuppressed(result) && !known.has(result.key))
    .map((result) => result.key))];
  const suppressedKeySet = new Set(suppressedKeys);
  const placedKeys = new Set<string>();
  const place = results.filter((result) => {
    if (isSuppressed(result) || known.has(result.key) || suppressedKeySet.has(result.key) ||
        placedKeys.has(result.key) || instant(result.createdAt) < since) return false;
    placedKeys.add(result.key);
    return true;
  });
  const additions = [...suppressedKeys, ...place.map((result) => result.key)];
  return {
    placed: additions.length > 0 ? [...placed, ...additions] : null,
    register: suppressedKeys,
    place,
  };
}

/** 用户明确把某个结果放上画布时（选中版本、拖入）也记为已放置。 */
export function withPlaced(placed: readonly string[] | null, keys: readonly string[]): string[] | null {
  if (placed === null) return null;
  const missing = keys.filter((key) => !placed.includes(key));
  return missing.length > 0 ? [...placed, ...missing] : null;
}
