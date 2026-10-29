import type { TilePick } from '@/lib/workspace/version-object';
import type { VersionTile } from '@/lib/workspace/versions';

/**
 * 重新打开一个会话时舞台回到上次看的对象。记在这个浏览器里（每个会话一份），换设备
 * 或清了站点数据就没有，那时退回到服务端记着的"选用"，再退回到最新的完成版本。
 */

export const LAST_OBJECT_KEY_PREFIX = 'studio.last-object.';
const KEY_PREFIX = LAST_OBJECT_KEY_PREFIX;

export interface SavedObject {
  kind: 'image' | 'model';
  taskId: string;
  assetId?: string;
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function defaultStore(): Store | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
}

/** 画板导出的合成图（`canvas:{会话}`）没有版本可回，不记；合并版本（`canvas:merged:…`）有。 */
export function isRestorable(object: { taskId: string }): boolean {
  return object.taskId !== '' && (!object.taskId.startsWith('canvas:') || object.taskId.startsWith('canvas:merged:'));
}

export function saveLastObject(
  sessionId: string,
  object: { kind: 'image' | 'model'; taskId: string; assetId?: string },
  store: Store | null = defaultStore(),
): void {
  if (!store || !isRestorable(object)) return;
  try {
    const saved: SavedObject = { kind: object.kind, taskId: object.taskId, ...(object.assetId ? { assetId: object.assetId } : {}) };
    store.setItem(KEY_PREFIX + sessionId, JSON.stringify(saved));
  } catch {
    // Private windows and blocked storage just mean nothing is remembered.
  }
}

export function readLastObject(sessionId: string, store: Store | null = defaultStore()): SavedObject | null {
  if (!store) return null;
  try {
    const raw = store.getItem(KEY_PREFIX + sessionId);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (value === null || typeof value !== 'object') return null;
    const { kind, taskId, assetId } = value as Record<string, unknown>;
    if ((kind !== 'image' && kind !== 'model') || typeof taskId !== 'string' || taskId === '') return null;
    return { kind, taskId, ...(typeof assetId === 'string' && assetId !== '' ? { assetId } : {}) };
  } catch {
    return null;
  }
}

export interface RestoreTarget {
  tile: VersionTile;
  pick: TilePick;
  source: 'saved' | 'picked' | 'newest';
}

function tileOfTask(ready: readonly VersionTile[], taskId: string, assetId?: string): VersionTile | undefined {
  // A screenshot carries its model's task ID: the picture itself tells the two apart.
  const byAsset = assetId ? ready.find((tile) => tile.origin === 'capture' && tile.assets.some((asset) => asset.id === assetId)) : undefined;
  return byAsset ?? ready.find((tile) => tile.taskIds.includes(taskId)) ?? ready.find((tile) => tile.taskId === taskId && tile.origin !== 'capture');
}

/**
 * 舞台该回到哪个版本：这个浏览器记的上一个对象，其次服务端的选用，最后最新的完成版本。
 * 记着的对象已经不在版本里（被删、被折掉、种类对不上）就往下退，不报错。
 */
export function restoreTarget(
  tiles: readonly VersionTile[],
  options: { saved: SavedObject | null; picked: { taskId?: string; assetId?: string } | null },
): RestoreTarget | null {
  const ready = tiles.filter((tile) => tile.status === 'ready');
  const attempt = (candidate: { taskId?: string; assetId?: string; kind?: 'image' | 'model' } | null, source: 'saved' | 'picked'): RestoreTarget | null => {
    if (!candidate?.taskId) return null;
    const tile = tileOfTask(ready, candidate.taskId, candidate.assetId);
    if (!tile || (candidate.kind && candidate.kind !== tile.domain)) return null;
    const assetKnown = candidate.assetId !== undefined && tile.assets.some((asset) => asset.id === candidate.assetId);
    return { tile, source, pick: { taskId: candidate.taskId, ...(assetKnown ? { assetId: candidate.assetId } : {}) } };
  };
  const found = attempt(options.saved, 'saved')
    ?? attempt(options.picked, 'picked');
  if (found) return found;
  const newest = ready.at(-1);
  return newest ? { tile: newest, pick: {}, source: 'newest' } : null;
}
