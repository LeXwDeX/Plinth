import type { ChatSession } from '@/store/chat-store';

/**
 * Why a conversation cannot be branched right now. 3D state is no longer a
 * reason: the branch copies the current stage/versions/captures (#186). Only
 * work that is still in flight or needs a decision blocks the snapshot.
 */
export function branchUnavailableReason(state: { busy?: boolean; compacting?: boolean; approval?: boolean; running?: boolean }): string {
  if (state.busy) return 'ui.unavailable.reason.branch.busy';
  if (state.compacting) return 'ui.unavailable.reason.branch.compacting';
  if (state.approval) return 'ui.unavailable.reason.branch.approval';
  if (state.running) return 'ui.unavailable.reason.branch.running';
  return '';
}

export type BranchCopyResult = {
  canvasCopied: boolean;
  sceneCopied: boolean;
  capturesCopied: boolean;
};

export function branchCopiedCurrentState(result: BranchCopyResult): boolean {
  return result.canvasCopied || result.sceneCopied || result.capturesCopied;
}

export async function branchConversation(
  branch: (sessionId: string, messageId: string) => Promise<{ session: ChatSession } & BranchCopyResult>,
  sessionId: string,
  messageId: string,
  actions: { flush: () => Promise<boolean>; isCurrent: () => boolean; clear: () => void; select: (session: ChatSession) => void; remember: (session: ChatSession) => void; done: (result: BranchCopyResult) => void; failed: (error: unknown) => void },
): Promise<void> {
  try {
    if (!await actions.flush()) throw new Error('');
    if (!actions.isCurrent()) return;
    const result = await branch(sessionId, messageId);
    if (actions.isCurrent()) {
      actions.clear();
      actions.select(result.session);
    } else actions.remember(result.session);
    actions.done({ canvasCopied: result.canvasCopied, sceneCopied: result.sceneCopied, capturesCopied: result.capturesCopied });
  } catch (error) {
    actions.failed(error);
  }
}