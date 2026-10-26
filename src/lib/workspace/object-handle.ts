import type { ChatMessage } from '@/store/chat-store';
import type { CurrentObject } from '@/store/workspace-store';

/**
 * 当前对象在对话里的句柄（@img5 / @model2）。句柄只由服务端在消息附件里给出；
 * 找不到就返回空串——不自己编号。
 */
export function handleOfObject(
  messages: readonly Pick<ChatMessage, 'attachments'>[],
  object: Pick<CurrentObject, 'taskId' | 'assetId' | 'kind' | 'url'> | null,
): string {
  if (!object?.taskId) return '';
  const exactAssets: string[] = [];
  const exactURLs: string[] = [];
  const sameTask: string[] = [];
  for (const message of messages) {
    for (const attachment of message.attachments ?? []) {
      if (attachment.kind !== object.kind || attachment.taskId !== object.taskId) continue;
      if (attachment.assetId && object.assetId) {
        if (attachment.assetId === object.assetId) exactAssets.push(attachment.handle);
        // Model handles identify the public task across its delivered formats.
        // Images with different asset IDs still identify different pictures.
        if (object.kind === 'model') sameTask.push(attachment.handle);
        continue;
      }
      if (attachment.url && object.url && attachment.url === object.url) exactURLs.push(attachment.handle);
      sameTask.push(attachment.handle);
    }
  }
  // Resolve each priority across the whole history, never by message order.
  // Missing or conflicting handles at the selected priority fail closed.
  const candidates = exactAssets.length ? exactAssets : exactURLs.length ? exactURLs : sameTask;
  const handles = new Set(candidates);
  return handles.size === 1 && candidates[0]?.trim() ? candidates[0] : '';
}
