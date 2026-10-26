import type Konva from 'konva';
import type { CanvasNode, ShapeType } from '@/store/canvas-store';

export interface CanvasPoint {
  x: number;
  y: number;
}

/** Use the rendered transform, including a pan that Konva has just applied. */
export function canvasPointerToWorld(
  stage: Pick<Konva.Node, 'getAbsoluteTransform'>,
  pointer: CanvasPoint,
): CanvasPoint {
  return stage.getAbsoluteTransform().copy().invert().point(pointer);
}

export function shapeGeometryFromDrag(
  shapeType: ShapeType,
  start: CanvasPoint,
  end: CanvasPoint,
): Pick<CanvasNode, 'x' | 'y' | 'width' | 'height' | 'rotation'> {
  const dx = end.x - start.x;
  const dy = end.y - start.y;
  if (shapeType === 'line' || shapeType === 'arrow') {
    // Existing line nodes draw the local diagonal [0, 0, width, height].
    // Rotate that diagonal to the drag direction, retaining positive sizes
    // even for horizontal and vertical lines and the existing storage format.
    const edge = Math.hypot(dx, dy) / Math.SQRT2;
    return {
      x: start.x,
      y: start.y,
      width: edge,
      height: edge,
      rotation: (Math.atan2(dy, dx) * 180) / Math.PI - 45,
    };
  }
  return {
    x: Math.min(start.x, end.x),
    y: Math.min(start.y, end.y),
    width: Math.abs(dx),
    height: Math.abs(dy),
    rotation: 0,
  };
}

export function nodeContainsCanvasPoint(node: CanvasNode, point: CanvasPoint): boolean {
  const angle = ((node.rotation ?? 0) * Math.PI) / 180;
  const dx = point.x - node.x;
  const dy = point.y - node.y;
  const x = dx * Math.cos(angle) + dy * Math.sin(angle);
  const y = -dx * Math.sin(angle) + dy * Math.cos(angle);
  return x >= 0 && x <= node.width && y >= 0 && y <= node.height;
}
