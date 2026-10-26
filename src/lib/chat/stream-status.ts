import type { ProductSchema } from '@/lib/product-schema';
import type { ChatSemanticEvent } from '@/lib/chat/browser-client';
import type { GenState } from '@/store/chat-store';

/**
 * The one status slot of a streaming reply (#285). The model's thinking status,
 * the tool status and generation progress share it, so they never show twice:
 * - `thinking` shows the Schema thinking text until visible reply text arrives;
 * - text that is not blank replaces the thinking status (a tool status stays);
 * - `status`, `progress` and `images` replace whatever was shown, as before.
 * Returns `current` itself when the event leaves the slot unchanged.
 */
export function nextStreamStatus(
  schema: ProductSchema,
  current: GenState,
  event: ChatSemanticEvent,
): GenState {
  switch (event.type) {
    case 'thinking':
      return { statusText: event.content, progress: null, thinking: true };
    case 'text':
      return current.thinking && event.content.trim() !== ''
        ? { statusText: '', progress: null }
        : current;
    case 'status':
      return { statusText: event.content, progress: null };
    case 'progress':
      return {
        statusText: schema.chat.presentation.status.generating,
        // 上游进度偶发越界（如 Meshy >100），气泡展示前钳制到声明量程。
        progress: Math.min(
          schema.runtime.frontend.percentage_multiplier,
          Math.max(0, event.percent),
        ),
      };
    case 'images':
      return { statusText: '', progress: null };
    default:
      return current;
  }
}
