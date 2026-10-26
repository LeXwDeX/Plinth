import type { CanvasNode } from '@/store/canvas-store';

/**
 * 画布上的手绘修改合并成新版本：不调用模型，不花积分，把可见内容拍平成一张新图
 * （拍平和上传在画板里做）。这里只列出已合并出来的版本。
 */

/** 合并出来的版本：画板上带 mergedFrom 的图片节点，按合并时间排序。 */
export interface MergedVersion {
  nodeId: string;
  url: string;
  /** 来源版本的任务 ID（也可能是另一个合并版本的合成 ID）。 */
  from: string;
  at: string;
}

/** 合并版本在版本条和舞台上用的合成对象 ID：沿用 `canvas:` 前缀，表示它没有服务端任务。 */
export const MERGED_OBJECT_PREFIX = 'canvas:merged:';

export function mergedObjectId(nodeId: string): string {
  return `${MERGED_OBJECT_PREFIX}${nodeId}`;
}

export function mergedVersionsOf(nodes: readonly CanvasNode[]): MergedVersion[] {
  return nodes
    .filter((node) => node.type === 'image' && node.mergedFrom && node.imageUrl)
    .map((node) => ({ nodeId: node.id, url: node.imageUrl, from: node.mergedFrom as string, at: node.mergedAt ?? '' }))
    .sort((left, right) => left.at.localeCompare(right.at) || left.nodeId.localeCompare(right.nodeId));
}
