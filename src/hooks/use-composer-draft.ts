import { useEffect, useRef } from 'react';
import { toast } from 'sonner';

import type { AttachmentPurpose } from '@/lib/chat/attachment-purpose';
import { draftFromState, loadDraft, saveDraft, type ComposerDraftSource } from '@/lib/chat/composer-draft';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';

interface ComposerDraftState {
  sessionId: string | null;
  text: string;
  setText: (value: string) => void;
  images: readonly string[];
  sources: Readonly<Record<string, ComposerDraftSource | undefined>>;
  purpose: AttachmentPurpose | null;
  setPurpose: (value: AttachmentPurpose | null) => void;
  /** Schema text for the note shown when local pictures could not be kept. */
  droppedNote: (count: number) => string;
}

/**
 * Keeps the unsent composer of the active session in the browser: restored when the session comes back (after
 * a switch, a refresh or a new login), rewritten as it changes, and gone once it is sent (an empty draft is
 * removed). The first pass of a visit only restores, so the empty state a visit starts with never overwrites it.
 */
export function useComposerDraft(state: ComposerDraftState): void {
  const revision = useChatStore((store) => store.activeSessionRevision);
  const { sessionId, text, setText, images, sources, purpose, setPurpose, droppedNote } = state;
  const visit = sessionId ? `${sessionId}#${revision}` : '';
  const restoredVisit = useRef('');
  const skipSave = useRef(false);

  useEffect(() => {
    if (!sessionId || restoredVisit.current === visit) return;
    restoredVisit.current = visit;
    const draft = loadDraft(sessionId);
    if (!draft) return;
    // The pass that restores still sees the empty state a visit starts with; it must not erase the draft.
    skipSave.current = true;
    if (draft.text) setText(draft.text);
    if (draft.images.length > 0) {
      useWorkspaceStore.getState().queueChatAttachments(draft.images.map((url) => ({ url, ...draft.sources[url] })));
      if (draft.purpose) setPurpose(draft.purpose);
    }
    if (draft.dropped > 0) {
      toast.message(droppedNote(draft.dropped));
      // Said once: the stored draft forgets the count.
      saveDraft(sessionId, { ...draft, dropped: 0 });
    }
  }, [visit, sessionId, setText, setPurpose, droppedNote]);

  useEffect(() => {
    if (!sessionId || restoredVisit.current !== visit) return;
    if (skipSave.current) { skipSave.current = false; return; }
    saveDraft(sessionId, draftFromState({ text, images, sources, purpose }));
  }, [visit, sessionId, text, images, sources, purpose]);
}
