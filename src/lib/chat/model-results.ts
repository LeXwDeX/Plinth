import { viewableGaussianSplatAsset, viewableModelAsset } from '@/lib/workspace/task-projection';
import { targetOfTaskAssets, type ResultTarget } from '@/lib/workspace/result-target';
import type { CurrentObject, TrackedTask } from '@/store/workspace-store';

/** 对话里一条 3D 结果：可在查看器打开的主模型，及任务详情用的能力与提示词。格式清单在右侧栏的“格式”里看。 */
export interface ChatModelResult {
  taskId: string;
  capability: string;
  prompt: string;
  selection: CurrentObject;
}

/** Distinct Splat results travel to their own preview/download detail, never the mesh model card/stage. */
export function chatGaussianSplatResults(
  tasks: ReadonlyArray<TrackedTask>,
  taskIds: ReadonlyArray<string>,
  sessionId: string | null | undefined,
): ResultTarget[] {
  if (!sessionId || taskIds.length === 0) return [];
  const wanted = new Set(taskIds);
  return tasks.flatMap((task) => {
    if (!wanted.has(task.taskId) || task.sessionId !== sessionId || task.capability !== 'image-to-splat') return [];
    const asset = viewableGaussianSplatAsset(task.downloadables);
    if (!asset) return [];
    return [targetOfTaskAssets({
      taskId: task.taskId, sessionId, title: task.prompt, capability: task.capability,
      prompt: task.prompt, status: 'ready',
    }, task.downloadables)];
  });
}

/**
 * 从工作区任务跟踪里取出这条消息提交的、属于当前会话且已有可下载模型的
 * 任务。跨会话任务、未完成任务与只有图片的任务都不出现，因此 3D 结果卡
 * 只在服务端确认存在已保存模型后才显示。
 */
export function chatModelResults(
  tasks: ReadonlyArray<TrackedTask>,
  taskIds: ReadonlyArray<string>,
  sessionId: string | null | undefined,
): ChatModelResult[] {
  if (!sessionId || taskIds.length === 0) return [];
  const wanted = new Set(taskIds);
  const results: ChatModelResult[] = [];
  for (const task of tasks) {
    if (!wanted.has(task.taskId) || task.sessionId !== sessionId) continue;
    const primary = viewableModelAsset(task.downloadables);
    if (!primary) continue;
    results.push({
      taskId: task.taskId,
      capability: task.capability,
      prompt: task.prompt,
      selection: { taskId: task.taskId, kind: 'model', url: primary.url, assetId: primary.id, sessionId },
    });
  }
  return results;
}
