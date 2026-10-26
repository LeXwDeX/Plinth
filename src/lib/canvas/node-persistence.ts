import type { CanvasNode } from '@/store/canvas-store';
import { isInpaintSource } from './inpaint-source';

// ── Canvas 节点 ↔ DB 转换 ──

export interface DBNodeRecord {
  id: string;
  type: string;
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  data: string;
  visible: boolean;
  locked: boolean;
}

export function nodeToRecord(node: CanvasNode): DBNodeRecord {
  const { id, type, x, y, width, height, zIndex, visible, locked } = node;
  const data: Record<string, unknown> = {};
  if (node.imageUrl) data.imageUrl = node.imageUrl;
  if (node.text) data.text = node.text;
  if (node.taskId) data.taskId = node.taskId;
  if (node.captureId) data.captureId = node.captureId;
  if (node.shapeType) data.shapeType = node.shapeType;
  if (node.pathData) data.pathData = node.pathData;
  if (node.groupId) data.groupId = node.groupId;
  if (node.strokeWidth !== undefined) data.strokeWidth = node.strokeWidth;
  if (node.strokeColor !== undefined) data.strokeColor = node.strokeColor;
  if (node.fillColor !== undefined) data.fillColor = node.fillColor;
  if (node.fontSize !== undefined) data.fontSize = node.fontSize;
  if (node.rotation !== undefined) data.rotation = node.rotation;
  if (node.opacity !== undefined) data.opacity = node.opacity;
  if (node.crop !== undefined) data.crop = node.crop;
  if (node.mergedFrom) data.mergedFrom = node.mergedFrom;
  if (node.mergedAt) data.mergedAt = node.mergedAt;
  if (node.name) data.name = node.name;
  if (node.blank === true) data.blank = true;
  if (node.type === 'image' && isInpaintSource(node.inpaint)) {
    data.inpaint = {
      maskAssetId: node.inpaint.maskAssetId,
      width: node.inpaint.width,
      height: node.inpaint.height,
    };
  }
  return { id, type, x, y, width, height, zIndex, data: JSON.stringify(data), visible: visible !== false, locked: locked === true };
}

/** 持久化数据不可信：crop 四个字段必须是有限数值且尺寸为正。 */
function isValidCrop(value: unknown): value is NonNullable<CanvasNode['crop']> {
  if (!value || typeof value !== 'object') return false;
  const crop = value as Record<string, unknown>;
  return (
    typeof crop.x === 'number' && Number.isFinite(crop.x) &&
    typeof crop.y === 'number' && Number.isFinite(crop.y) &&
    typeof crop.width === 'number' && Number.isFinite(crop.width) && crop.width > 0 &&
    typeof crop.height === 'number' && Number.isFinite(crop.height) && crop.height > 0
  );
}

export function recordToNode(record: DBNodeRecord): CanvasNode | null {
  let data: Partial<CanvasNode>;
  try {
    data = JSON.parse(record.data || '{}') as Partial<CanvasNode>;
  } catch (e) {
    console.warn(`[Canvas] skipped node ${record.id}: its data field is not valid JSON`, e);
    return null;
  }
  return {
    id: record.id,
    type: record.type as CanvasNode['type'],
    imageUrl: data.imageUrl || '',
    text: data.text,
    taskId: data.taskId || '',
    captureId: typeof data.captureId === 'string' ? data.captureId : undefined,
    x: record.x,
    y: record.y,
    width: record.width,
    height: record.height,
    zIndex: record.zIndex,
    shapeType: data.shapeType as CanvasNode['shapeType'],
    pathData: data.pathData,
    visible: record.visible,
    locked: record.locked,
    groupId: data.groupId || undefined,
    strokeWidth: typeof data.strokeWidth === 'number' ? data.strokeWidth : undefined,
    strokeColor: typeof data.strokeColor === 'string' ? data.strokeColor : undefined,
    fillColor: typeof data.fillColor === 'string' ? data.fillColor : undefined,
    fontSize: typeof data.fontSize === 'number' && data.fontSize > 0 ? data.fontSize : undefined,
    rotation: typeof data.rotation === 'number' ? data.rotation : undefined,
    opacity: typeof data.opacity === 'number' ? data.opacity : undefined,
    crop: isValidCrop(data.crop) ? data.crop : undefined,
    mergedFrom: typeof data.mergedFrom === 'string' && data.mergedFrom !== '' ? data.mergedFrom : undefined,
    mergedAt: typeof data.mergedAt === 'string' && data.mergedAt !== '' ? data.mergedAt : undefined,
    name: typeof data.name === 'string' && data.name !== '' ? data.name : undefined,
    // An unpainted initial layer (#246) only while it still has no picture.
    blank: data.blank === true && record.type === 'image' && !data.imageUrl ? true : undefined,
    inpaint: record.type === 'image' && isInpaintSource(data.inpaint)
      ? { maskAssetId: data.inpaint.maskAssetId, width: data.inpaint.width, height: data.inpaint.height }
      : undefined,
  };
}

export function computeNodesHash(nodes: CanvasNode[]): string {
  return JSON.stringify(nodes.map(nodeToRecord));
}
