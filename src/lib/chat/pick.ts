import type { ChatAttachment, ChatImage, ChatMessage } from '@/store/chat-store';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';

/**
 * 候选图"选用"：一条消息的图片就是候选，选用把其中一张定为会话当前对象。
 * 以下是不依赖界面的判断；服务端对每个声明重新核对 owner 与会话。
 */

/** 候选图（消息第 index 张图）对应的结构化附件；消息还没有附件记录时为空。 */
export function candidateAttachment(message: Pick<ChatMessage, 'attachments'>, index: number): ChatAttachment | undefined {
  return message.attachments?.find((attachment) => attachment.kind === 'image' && attachment.imageSlot === index + 1);
}

/**
 * Agent 裁出的图（附件角色 crop，#587）：没有来源任务，但它是本会话的资产，可以选用。
 * 只认消息附件记录的角色，用户自己上传的图仍不能选用；服务端再核对 owner 与该会话确实展示过它。
 */
export function cropAssetOf(attachment?: ChatAttachment): string | undefined {
  return attachment?.kind === 'image' && attachment.role === 'crop' && !attachment.taskId && !attachment.captureId
    ? attachment.assetId || undefined : undefined;
}

/** 选用该候选图时发给服务端的声明；生成结果按来源任务，Agent 裁图按资产；其他没有来源任务的图片不能选用。 */
export function pickClaim(candidate: ChatImage, attachment?: ChatAttachment): ChatAttachmentClaim | null {
  const taskId = attachment?.taskId || candidate.taskId;
  if (!taskId) {
    const crop = cropAssetOf(attachment);
    return crop ? { kind: 'image', assetId: crop } : null;
  }
  if (attachment?.assetId) return { kind: 'image', taskId, assetId: attachment.assetId };
  return candidate.url ? { kind: 'image', taskId, url: candidate.url } : null;
}

/** 该候选图是否就是会话当前选用的那一张。 */
export function isPickedCandidate(
  pick: ChatAttachment | null | undefined,
  candidate: ChatImage,
  attachment?: ChatAttachment,
): boolean {
  if (!pick || pick.kind !== 'image') return false;
  const taskId = attachment?.taskId || candidate.taskId;
  if (!taskId) {
    const crop = cropAssetOf(attachment);
    return Boolean(crop) && !pick.taskId && pick.assetId === crop;
  }
  if (pick.taskId !== taskId) return false;
  if (attachment?.assetId && pick.assetId) return pick.assetId === attachment.assetId;
  return pick.url !== undefined && pick.url === candidate.url;
}
