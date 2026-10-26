import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import type { ChatMessage } from '@/store/chat-store';
import type { QueuedChatAttachment } from '@/store/workspace-store';

/**
 * 最近一轮的"重新生成"和"编辑后重发"：只针对会话里最后的用户消息及其回复。
 * 以下是不依赖界面的判断；删除与重发仍走服务端校验过的接口。
 */

export interface TurnPayload {
  text: string;
  images: string[];
  claims: ChatAttachmentClaim[];
}

/** 用一条用户消息的文本、图片与附件声明重新提交时要发送的内容。 */
export function turnPayload(message: Pick<ChatMessage, 'content' | 'images' | 'attachments'>): TurnPayload {
  const images = (message.images ?? []).map((image) => image.url);
  const claims = (message.attachments ?? []).flatMap((attachment): ChatAttachmentClaim[] =>
    attachment.kind === 'image' && attachment.imageSlot
      ? [{
          kind: 'image',
          imageSlot: attachment.imageSlot,
          ...(attachment.assetId ? { assetId: attachment.assetId } : {}),
          ...(attachment.taskId ? { taskId: attachment.taskId } : {}),
          ...(attachment.captureId ? { captureId: attachment.captureId } : {}),
        }]
      : []);
  return { text: message.content, images, claims };
}

export interface LastExchange {
  user: ChatMessage;
  /** 该用户消息之后的全部消息（助手回复）。 */
  replies: ChatMessage[];
}

function settled(message: ChatMessage, optimisticPrefix: string): boolean {
  return !message.isPending && !message.isStreaming && !message.id.startsWith(optimisticPrefix);
}

/**
 * 会话最后一轮：最后一条用户消息和它之后的回复。轮次未结束（有流式、待定
 * 或乐观消息）、没有用户消息、或用户消息之后混有别的用户消息时没有最后一轮。
 */
export function lastExchange(messages: readonly ChatMessage[], optimisticPrefix: string): LastExchange | null {
  let index = -1;
  for (let position = messages.length - 1; position >= 0; position -= 1) {
    if (messages[position].role === 'user') { index = position; break; }
  }
  if (index < 0) return null;
  const tail = messages.slice(index);
  if (!tail.every((message) => settled(message, optimisticPrefix))) return null;
  return { user: tail[0], replies: tail.slice(1) };
}

/** "重新生成"挂在最后一轮的最后一条助手回复上；返回要删除的消息和要重发的内容。 */
export function regenerateTarget(
  messages: readonly ChatMessage[],
  optimisticPrefix: string,
): { assistantId: string; user: ChatMessage; deleteIds: string[] } | null {
  const exchange = lastExchange(messages, optimisticPrefix);
  if (!exchange) return null;
  const assistants = exchange.replies.filter((message) => message.role === 'assistant');
  const last = assistants.at(-1);
  if (!last) return null;
  return { assistantId: last.id, user: exchange.user, deleteIds: [...exchange.replies.map((message) => message.id), exchange.user.id] };
}

/** "编辑后重发"挂在最后一条用户消息上；发送时才删除这一轮，放弃编辑不丢任何东西。 */
export function editTarget(
  messages: readonly ChatMessage[],
  optimisticPrefix: string,
): { userId: string; deleteIds: string[] } | null {
  const exchange = lastExchange(messages, optimisticPrefix);
  if (!exchange) return null;
  return { userId: exchange.user.id, deleteIds: [...exchange.replies.map((message) => message.id), exchange.user.id] };
}

/**
 * 编辑时把原消息的图片放回输入框：每张图带回它原来的来源（资产、任务或截图），
 * 重发时仍按服务端校验过的 ID 声明，而不是只靠 URL。
 */
export function editAttachments(message: Pick<ChatMessage, 'images' | 'attachments'>): QueuedChatAttachment[] {
  return (message.images ?? []).map((image, index) => {
    const source = (message.attachments ?? []).find((attachment) => attachment.kind === 'image' && attachment.imageSlot === index + 1);
    return {
      url: image.url,
      ...(source?.assetId ? { assetId: source.assetId } : {}),
      ...(source?.taskId ? { taskId: source.taskId } : {}),
      ...(source?.captureId ? { captureId: source.captureId } : {}),
    };
  });
}

/** 重发没有送出时，按原来的来源声明把图片放回输入框。 */
export function restoreAttachments(images: readonly string[], claims: readonly ChatAttachmentClaim[]): QueuedChatAttachment[] {
  return images.map((url, index) => {
    const claim = claims.find((item) => item.imageSlot === index + 1);
    return {
      url,
      ...(claim?.assetId ? { assetId: claim.assetId } : {}),
      ...(claim?.taskId ? { taskId: claim.taskId } : {}),
      ...(claim?.captureId ? { captureId: claim.captureId } : {}),
    };
  });
}
