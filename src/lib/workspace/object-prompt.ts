import type { ChatMessage } from '@/store/chat-store';
import type { CurrentObject } from '@/store/workspace-store';

type MessageFacts = Pick<ChatMessage, 'role' | 'content' | 'images' | 'attachments' | 'taskIds'>;

/**
 * The words a version was asked for: what the person wrote in the turn that made it. Used
 * when the task's own prompt is not at hand (a session opened from history). Empty when
 * no turn of the conversation produced the object.
 */
export function promptOfObject(messages: readonly MessageFacts[], object: Pick<CurrentObject, 'taskId'> | null): string {
  if (!object?.taskId) return '';
  const produced = messages.findIndex((message) => message.role === 'assistant' && (
    message.taskIds?.includes(object.taskId)
    || message.images?.some((image) => image.taskId === object.taskId)
    || message.attachments?.some((attachment) => attachment.taskId === object.taskId)
  ));
  if (produced < 0) return '';
  for (let index = produced - 1; index >= 0; index -= 1) {
    if (messages[index].role === 'user') return messages[index].content.trim();
  }
  return '';
}
