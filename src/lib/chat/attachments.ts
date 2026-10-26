import type { ProductSchema } from '@/lib/product-schema';
import type { ChatAttachment } from '@/store/chat-store';
import type { CanvasNode } from '@/store/canvas-store';
import type { CurrentObject } from '@/store/workspace-store';

/**
 * 客户端对一个图片或模型的声明。服务端会按 owner 与会话逐项校验，
 * 校验不过的声明被丢弃——这里只描述"用户看到并正在使用的对象"。
 */
export interface ChatAttachmentClaim {
  kind: 'image' | 'model';
  url?: string;
  assetId?: string;
  taskId?: string;
  captureId?: string;
  /** 声明对应消息图片的 1 起序号（只用于 attachments，不用于 selection）。 */
  imageSlot?: number;
  /**
   * 画板拍平图（#272）：拍进这张图的可见图层数。服务端只在画板来源的回合里读取并限幅；
   * 画板角色与尺寸由服务端自己判定和测量，不由客户端声明。
   */
  canvasLayers?: number;
  /** 画板拍平图（#189）：拍进这张图的生成图片图层地址。服务端按 owner 与会话逐个校验，只留下本会话的任务。 */
  canvasSources?: string[];
}

/** An `@` reference written in the message text, with the claim about the picture it names. */
export interface ChatMentionClaim {
  token: string;
  claim: ChatAttachmentClaim;
}

type WireObject = Record<string, unknown>;

/** Encode mentions with the Schema's member names; the token travels next to the claim's members. */
export function encodeMentions(schema: ProductSchema, mentions: readonly ChatMentionClaim[]): WireObject[] {
  return mentions.map((mention) => ({
    ...encodeAttachmentClaims(schema, [mention.claim])[0],
    [schema.chat.api.attachment.token]: mention.token,
  }));
}

/** 按 Schema 声明的成员名编码声明。空值不发送。 */
export function encodeAttachmentClaims(
  schema: ProductSchema,
  claims: readonly ChatAttachmentClaim[],
): WireObject[] {
  const names = schema.chat.api.attachment;
  return claims.map((claim) => {
    const wire: WireObject = { [names.kind]: claim.kind };
    if (claim.url) wire[names.url] = claim.url;
    if (claim.assetId) wire[names.asset_id] = claim.assetId;
    if (claim.taskId) wire[names.task_id] = claim.taskId;
    if (claim.captureId) wire[names.capture_id] = claim.captureId;
    if (claim.imageSlot && claim.imageSlot > 0) wire[names.image_slot] = claim.imageSlot;
    if (claim.canvasLayers && claim.canvasLayers > 0) wire[names.canvas_layers] = claim.canvasLayers;
    if (claim.canvasSources?.length && names.canvas_sources) wire[names.canvas_sources] = claim.canvasSources;
    return wire;
  });
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

type AttachmentNames = ProductSchema['chat']['api']['attachment'];

/** 解码一个附件对象；类型未知的返回空，缺少句柄时 `handle` 为空串（requireHandle 时改为返回空）。 */
function decodeAttachment(names: AttachmentNames, item: unknown, requireHandle: boolean): ChatAttachment | null {
  if (item === null || typeof item !== 'object') return null;
  const source = item as WireObject;
  const handle = text(source[names.handle]);
  const kind = source[names.kind];
  if ((requireHandle && !handle) || (kind !== 'image' && kind !== 'model')) return null;
  const slot = source[names.image_slot];
  return {
    handle: handle ?? '',
    kind,
    role: text(source[names.role]),
    assetId: text(source[names.asset_id]),
    taskId: text(source[names.task_id]),
    captureId: text(source[names.capture_id]),
    imageSlot: typeof slot === 'number' && slot > 0 ? slot : undefined,
    url: text(source[names.url]),
  };
}

/** 解码消息响应里的附件对象；缺少句柄或类型未知的条目被忽略。 */
export function decodeAttachments(schema: ProductSchema, raw: unknown): ChatAttachment[] {
  if (!Array.isArray(raw)) return [];
  const names = schema.chat.api.attachment;
  return raw.flatMap((item) => decodeAttachment(names, item, true) ?? []);
}

/**
 * 解码会话选用。选用是服务端已接受的结果，没有句柄（旧消息里的图片、模型截图）也照样保留，
 * 句柄为空串；只有类型未知或不是对象时才视为没有选用。
 */
export function decodePick(schema: ProductSchema, raw: unknown): ChatAttachment | null {
  return decodeAttachment(schema.chat.api.attachment, raw, false);
}

export interface SelectionSources {
  sessionId: string | null;
  /** 工作区正在查看的对象（已按会话过滤）。 */
  currentObject: CurrentObject | null;
  /** 当前会话画布上的节点与被选中的节点 ID。 */
  nodes: readonly CanvasNode[];
  selectedNodeIds: readonly string[];
}

/**
 * 工作区"当前选中对象"：舞台上的对象是模型时就是这个模型；是图片时先看画布上
 * 被选中的图片节点（带来源任务与截图记录），再看舞台上刚截取的图片。没有可核实
 * ID 的对象不上报。
 */
export function buildChatSelection(sources: SelectionSources): ChatAttachmentClaim[] {
  if (!sources.sessionId) return [];
  const { currentObject } = sources;
  if (currentObject?.kind === 'model') {
    if (!currentObject.taskId) return [];
    return [{
      kind: 'model',
      taskId: currentObject.taskId,
      ...(currentObject.assetId ? { assetId: currentObject.assetId } : {}),
    }];
  }
  const selected = new Set(sources.selectedNodeIds);
  const claims: ChatAttachmentClaim[] = [];
  const seen = new Set<string>();
  for (const node of sources.nodes) {
    if (!selected.has(node.id) || node.type !== 'image' || node.visible === false) continue;
    const claim = nodeClaim(node);
    if (!claim) continue;
    const key = `${claim.captureId ?? ''}|${claim.taskId ?? ''}|${claim.url ?? ''}`;
    if (seen.has(key)) continue;
    seen.add(key);
    claims.push(claim);
  }
  if (claims.length === 0 && currentObject?.kind === 'image' && currentObject.captureId) {
    // 刚从 3D 视图截取、还没被点选的图片也是用户正在看的对象。
    claims.push({
      kind: 'image',
      url: currentObject.url,
      captureId: currentObject.captureId,
      taskId: currentObject.taskId,
      ...(currentObject.assetId ? { assetId: currentObject.assetId } : {}),
    });
  }
  return claims;
}

function nodeClaim(node: CanvasNode): ChatAttachmentClaim | null {
  if (!node.imageUrl) return null;
  return {
    kind: 'image',
    url: node.imageUrl,
    // A picture with no task of its own (made on the board, or uploaded) is named by its address only.
    ...(node.taskId && !node.taskId.startsWith('canvas:') && !node.taskId.startsWith('upload:') ? { taskId: node.taskId } : {}),
    ...(node.captureId ? { captureId: node.captureId } : {}),
  };
}
