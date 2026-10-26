import { assetKind } from '@/lib/workspace/task-projection';

/** 会话列表每行的封面：这个会话里最新一次产出的图片（3D 任务取它的缩略图）。 */
export interface SessionCover {
  taskId: string;
  url: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/**
 * 从服务端任务列表（最新的在前，只含成功的任务）里取封面：第一个带图片文件的任务。
 * 没有图片的任务（比如只有模型文件）跳过；一个都没有就没有封面。
 */
export function coverOfTasks(payload: unknown): SessionCover | null {
  const tasks = isRecord(payload) && Array.isArray(payload.tasks) ? payload.tasks : [];
  for (const task of tasks) {
    if (!isRecord(task) || typeof task.id !== 'string' || !Array.isArray(task.assets)) continue;
    const images = task.assets.filter((asset): asset is Record<string, unknown> =>
      isRecord(asset) && typeof asset.url === 'string' && asset.url !== '' && assetKind(typeof asset.media_type === 'string' ? asset.media_type : '') === 'image');
    // The picture itself over a thumbnail; a model task offers its thumbnail, never a texture map.
    const chosen = images.find((asset) => asset.role !== 'thumbnail' && asset.role !== 'texture')
      ?? images.find((asset) => asset.role === 'thumbnail');
    if (chosen) return { taskId: task.id, url: chosen.url as string };
  }
  return null;
}
