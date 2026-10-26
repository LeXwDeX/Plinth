import { create } from 'zustand';

/**
 * The 3D result whose viewer is open inside its chat card. A browser keeps only a few WebGL contexts alive,
 * so the whole chat shows one live viewer at a time: opening another card hands the viewer over, and the
 * previous card goes back to its cover.
 */
interface InlineModelPreviewState {
  taskId: string | null;
  open: (taskId: string) => void;
  /** Gives the viewer up, but only when this task still holds it (a card already replaced must not close its successor). */
  close: (taskId: string) => void;
}

export const useInlineModelPreviewStore = create<InlineModelPreviewState>((set, get) => ({
  taskId: null,
  open: (taskId) => set({ taskId }),
  close: (taskId) => { if (get().taskId === taskId) set({ taskId: null }); },
}));
