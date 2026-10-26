import type { ChatModelResult } from '@/lib/chat/model-results';
import type { ChatImage, ChatMessage } from '@/store/chat-store';
import type { TrackedTask } from '@/store/workspace-store';

/**
 * 一条助手消息的图片按结果归属拆开：2D 任务的图是候选图卡的内容，3D 任务附带的图
 * （模型的渲染图、上游的模型前 2D 图）只是 3D 结果卡的封面，不是 2D 候选，不能"选用"
 * 或"转 3D"。以下是不依赖界面的判断。
 */

/** 消息里的一张图，带它在 `message.images` 里的序号（附件的 imageSlot 是序号加 1）。 */
export interface MessagePicture {
  image: ChatImage;
  index: number;
}

export interface MessagePictures {
  /** 2D 结果：候选图卡里的图，各自保留原序号。 */
  candidates: MessagePicture[];
  /** 3D 结果附带的图：只作 3D 结果卡的封面，各自保留原序号。 */
  previews: MessagePicture[];
}

/** 一张图在卡片里的身份：与位置无关，卡片重排（例如按四视角槽位）后仍指同一张图。 */
export function pictureKey(entry: MessagePicture): string {
  return `${entry.image.url}-${entry.index}`;
}

/** 卡片当前那张图的位置：先按身份找所选的图，找不到用已选用的位置，再不行是第一张。 */
export function activePicturePosition(pictures: readonly MessagePicture[], chosenKey: string | null, pickedPosition: number): number {
  const chosen = chosenKey === null ? -1 : pictures.findIndex((entry) => pictureKey(entry) === chosenKey);
  if (chosen >= 0) return chosen;
  return pickedPosition >= 0 && pickedPosition < pictures.length ? pickedPosition : 0;
}

/** 这条消息涉及的任务：工具调用（`taskIds`）、已提交的付费步骤和图片自带的任务；去重，不含空串。 */
export function messageTaskIds(message: Pick<ChatMessage, 'taskIds' | 'paidSteps' | 'images'>): string[] {
  const ids = new Set<string>();
  for (const taskId of message.taskIds ?? []) if (taskId) ids.add(taskId);
  for (const step of message.paidSteps ?? []) if (step.taskId) ids.add(step.taskId);
  for (const image of message.images ?? []) if (image.taskId) ids.add(image.taskId);
  return Array.from(ids);
}

/**
 * 这条消息里属于 3D 任务的任务号：已有可用模型的结果，加上任务跟踪里本会话中能力属于 3D 域的任务
 * （`isModelCapability` 由 Schema 判定）。后者覆盖流式消息：图片先到、模型还不能下载时，
 * 模型的图也不会作为 2D 候选闪现。
 */
export function modelTaskIdsOf(
  message: Pick<ChatMessage, 'taskIds' | 'paidSteps' | 'images'>,
  results: readonly ChatModelResult[],
  tasks: ReadonlyArray<Pick<TrackedTask, 'taskId' | 'sessionId' | 'capability'>>,
  sessionId: string | null | undefined,
  isModelCapability: (capability: string) => boolean,
): string[] {
  const ids = new Set(results.map((result) => result.taskId));
  if (sessionId) {
    const wanted = new Set(messageTaskIds(message));
    for (const task of tasks) {
      if (wanted.has(task.taskId) && task.sessionId === sessionId && isModelCapability(task.capability)) ids.add(task.taskId);
    }
  }
  return Array.from(ids);
}

/**
 * 把消息的图片拆成 2D 候选与 3D 预览图。一张图是 3D 预览图，当且仅当：它所在槽位的附件是模型；
 * 或它的任务就是消息里某个模型附件的任务（旧消息里上游的模型前 2D 图是 `image` 附件，任务同模型）；
 * 或它的任务是这条消息的 3D 结果（`modelTaskIds`，来自任务跟踪，覆盖还没有附件的流式消息）。
 */
export function splitMessagePictures(
  message: Pick<ChatMessage, 'images' | 'attachments'>,
  modelTaskIds: Iterable<string> = [],
): MessagePictures {
  const attachments = message.attachments ?? [];
  const modelTasks = new Set(modelTaskIds);
  for (const attachment of attachments) {
    if (attachment.kind === 'model' && attachment.taskId) modelTasks.add(attachment.taskId);
  }
  const split: MessagePictures = { candidates: [], previews: [] };
  (message.images ?? []).forEach((image, index) => {
    const modelSlot = attachments.some((attachment) => attachment.kind === 'model' && attachment.imageSlot === index + 1);
    const preview = modelSlot || (image.taskId !== '' && modelTasks.has(image.taskId));
    (preview ? split.previews : split.candidates).push({ image, index });
  });
  return split;
}

/** 模型的封面：模型附件所在槽位的那张图，没有就取该任务的第一张预览图；都没有为空。 */
export function modelCover(
  message: Pick<ChatMessage, 'attachments'>,
  previews: readonly MessagePicture[],
  taskId: string,
): MessagePicture | null {
  const slot = message.attachments?.find((attachment) => attachment.kind === 'model' && attachment.taskId === taskId && attachment.imageSlot)?.imageSlot;
  const atSlot = slot ? previews.find((preview) => preview.index === slot - 1) : undefined;
  return atSlot ?? previews.find((preview) => preview.image.taskId === taskId) ?? null;
}

/** 一张 3D 结果卡：任务、可用的模型（还没有时为空）、封面与模型句柄。 */
export interface ModelCardModel {
  taskId: string;
  /** 任务跟踪里已有可下载模型时才有；没有（任务较旧、尚未就绪）时卡片只显示它的图。 */
  result: ChatModelResult | null;
  cover: MessagePicture | null;
  handle?: string;
}

/**
 * 这条消息的 3D 结果卡：先是已有模型的结果，再是只有预览图、跟踪里还没有模型的任务
 * （例如较早的会话只回灌了最近一页任务）。预览图不会因为模型暂不可用而被丢掉，也不会变成 2D 候选。
 * 没有任务号的预览图归给模型附件正好在它那个槽位上的卡；找不到就不成卡，空任务号从不单独成卡。
 */
export function modelCardsOf(
  message: Pick<ChatMessage, 'attachments'>,
  previews: readonly MessagePicture[],
  results: readonly ChatModelResult[],
): ModelCardModel[] {
  const cards: ModelCardModel[] = [];
  const seen = new Set<string>();
  const add = (taskId: string, result: ChatModelResult | null) => {
    if (!taskId || seen.has(taskId)) return;
    seen.add(taskId);
    const handle = message.attachments?.find((attachment) => attachment.kind === 'model' && attachment.taskId === taskId)?.handle;
    cards.push({ taskId, result, cover: modelCover(message, previews, taskId), ...(handle ? { handle } : {}) });
  };
  for (const result of results) add(result.taskId, result);
  for (const preview of previews) {
    const atSlot = message.attachments?.find((attachment) => attachment.kind === 'model' && attachment.imageSlot === preview.index + 1);
    add(preview.image.taskId || atSlot?.taskId || '', null);
  }
  return cards;
}
