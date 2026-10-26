import { nodeContainsCanvasPoint, type CanvasPoint } from '@/lib/canvas/drawing-geometry';
import type { CanvasNode } from '@/store/canvas-store';

/**
 * #245：画笔和橡皮的目标规则（悬停预览与落笔共用同一个判断）。
 * 作用于指针下“最上层的可见图片图层”，与是否选中无关：
 * - 该图片未锁定且位图就绪 → ready，可以落笔；
 * - 该图片已锁定 → locked，不会穿透到下面的图片；
 * - 该图片尚未加载 → loading；
 * - 指针下没有可见图片（文字、形状、空白或画板外）→ none。
 * 命中按图片矩形（含旋转）判断，透明像素也算在图片范围内。
 */
export type BrushTargetState = 'ready' | 'loading' | 'locked' | 'none';

export interface BrushTarget {
  node: CanvasNode | null;
  state: BrushTargetState;
}

export function brushTargetAt(
  nodesBottomToTop: readonly CanvasNode[],
  point: CanvasPoint,
  bitmapReady: (node: CanvasNode) => boolean,
): BrushTarget {
  for (let index = nodesBottomToTop.length - 1; index >= 0; index -= 1) {
    const node = nodesBottomToTop[index];
    if (node.type !== 'image' || node.visible === false || !nodeContainsCanvasPoint(node, point)) continue;
    if (node.locked) return { node, state: 'locked' };
    return { node, state: bitmapReady(node) ? 'ready' : 'loading' };
  }
  return { node: null, state: 'none' };
}
