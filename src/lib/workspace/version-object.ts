import { viewableModelAsset } from '@/lib/workspace/task-projection';
import type { VersionTile } from '@/lib/workspace/versions';
import type { CurrentObject } from '@/store/workspace-store';
import { orderedImageResults } from '@/lib/workspace/image-result-group';

export interface TilePick {
  taskId?: string;
  assetId?: string;
}

/**
 * 版本条上的一格在舞台上就是哪个对象：图片版本取候选里被选中（或第一张）的图，
 * 分组版本取来源槽位（正面）或被点的槽位，3D 版本取模型文件。进行中和失败的版本
 * 没有可看的文件，返回 null。
 */
export function objectOfTile(tile: VersionTile, sessionId: string, pick: TilePick = {}): CurrentObject | null {
  if (tile.status !== 'ready') return null;
  if (tile.domain === 'model') {
    // A model upload is a source record; the 3D stage still requires a public Task.
    if (tile.origin !== 'task') return null;
    const model = viewableModelAsset(tile.assets);
    return model ? { taskId: tile.taskId, kind: 'model', url: model.url, assetId: model.id, sessionId } : null;
  }
  const images = orderedImageResults(tile.assets);
  if (tile.slots.length > 0) {
    const slot = tile.slots.find((entry) => (pick.taskId ? entry.taskId === pick.taskId : entry.taskId === tile.taskId))
      ?? tile.slots[0];
    const asset = images.find((entry) => entry.id === slot.assetId);
    return asset ? { taskId: slot.taskId, kind: 'image', url: asset.url, assetId: asset.id, imageUrls: [asset.url], sessionId } : null;
  }
  const chosen = images.find((asset) => asset.id === pick.assetId) ?? images[0];
  return chosen
    ? {
      taskId: tile.taskId, kind: 'image', url: chosen.url, assetId: chosen.id, imageUrls: images.map((asset) => asset.url), sessionId,
      // A screenshot is told from its model (whose task it carries) by its capture record.
      ...(tile.origin === 'capture' && tile.captureId ? { captureId: tile.captureId } : {}),
    }
    : null;
}
