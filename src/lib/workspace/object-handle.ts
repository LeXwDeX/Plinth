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

/** The task and asset a platform asset address names (`…/tasks/{task}/assets/{asset}`), or null. */
function assetRoute(url: string | undefined): { taskId: string; assetId: string } | null {
  const route = url ? /\/tasks\/([^/?#]+)\/assets\/([^/?#]+)/.exec(url) : null;
  return route ? { taskId: decodeURIComponent(route[1]), assetId: decodeURIComponent(route[2]) } : null;
}

/**
 * The handle (@img5) of one exact picture shown somewhere else than the chat: a canvas layer or a version
 * tile. Unlike `handleOfObject` it never falls back to "the only picture of the task": a layer painted over
 * or cropped into a new picture keeps its task id, and it must not borrow the handle of the original. The
 * picture matches an attachment by its model capture, by the task and asset its platform address names,
 * (or the task and asset IDs the caller already holds), or by the very same address (a sent picture). Nothing found:
 * no handle. A picture sent again gets one more handle for the same object; the newest one is shown, the one the
 * server's history lookup also lists it under, so the badge matches what the Agent writes.
 */
export function handleOfPicture(
  messages: readonly (Pick<ChatMessage, 'attachments'> & Partial<Pick<ChatMessage, 'images'>>)[],
  picture: { url: string; captureId?: string; taskId?: string; assetId?: string },
): string {
  if (!picture.url && !picture.captureId) return '';
  // Platform IDs the client already holds for the picture (an attachment's source) name it as its address would.
  const route = picture.taskId && picture.assetId ? { taskId: picture.taskId, assetId: picture.assetId } : assetRoute(picture.url);
  let newest = '';
  for (const message of messages) {
    for (const attachment of message.attachments ?? []) {
      if (attachment.kind !== 'image' || !attachment.handle?.trim()) continue;
      if (picture.captureId) {
        if (attachment.captureId === picture.captureId) newest = attachment.handle;
        continue;
      }
      if (route) {
        const own = attachment.assetId && attachment.taskId
          ? { taskId: attachment.taskId, assetId: attachment.assetId }
          : assetRoute(attachment.url);
        if (own && own.taskId === route.taskId && own.assetId === route.assetId) newest = attachment.handle;
        continue;
      }
      const address = attachment.url ?? (attachment.imageSlot ? message.images?.[attachment.imageSlot - 1]?.url : undefined);
      if (address && address === picture.url) newest = attachment.handle;
    }
  }
  return newest;
}

/** A handle as a short label ("img5"): the Schema prefix's leading marker is dropped, as the server's parser does. */
export function handleLabel(handle: string): string {
  return handle.replace(/^[@#]+/, '');
}
