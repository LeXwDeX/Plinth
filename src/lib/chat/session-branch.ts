import type { ChatSession } from '@/store/chat-store';

export async function branchSceneExists(template: string, sessionId: string, fetcher: typeof fetch = fetch): Promise<boolean> {
  const response = await fetcher(template.replace('{sessionID}', encodeURIComponent(sessionId)));
  if (response.status === 404) return false;
  if (!response.ok) throw new Error('scene unavailable');
  // A scene revision exists only after an object was created, even if it was later deleted.
  return true;
}

export function branchUnavailableReason(state: { busy?: boolean; compacting?: boolean; approval?: boolean; running?: boolean; scene?: boolean; sceneUnknown?: boolean }): string {
  if (state.busy) return 'ui.unavailable.reason.branch.busy';
  if (state.compacting) return 'ui.unavailable.reason.branch.compacting';
  if (state.approval) return 'ui.unavailable.reason.branch.approval';
  if (state.running) return 'ui.unavailable.reason.branch.running';
  if (state.scene) return 'ui.unavailable.reason.branch.scene';
  if (state.sceneUnknown) return 'ui.unavailable.reason.branch.scene_unknown';
  return '';
}

export async function branchConversation(
  branch: (sessionId: string, messageId: string) => Promise<{ session: ChatSession; canvasCopied: boolean }>,
  sessionId: string,
  messageId: string,
  actions: { flush: () => Promise<boolean>; isCurrent: () => boolean; clear: () => void; select: (session: ChatSession) => void; remember: (session: ChatSession) => void; done: (canvasCopied: boolean) => void; failed: (error: unknown) => void },
): Promise<void> {
  try {
    if (!await actions.flush()) throw new Error('');
    if (!actions.isCurrent()) return;
    const result = await branch(sessionId, messageId);
    if (actions.isCurrent()) {
      actions.clear();
      actions.select(result.session);
    } else actions.remember(result.session);
    actions.done(result.canvasCopied);
  } catch (error) {
    actions.failed(error);
  }
}
