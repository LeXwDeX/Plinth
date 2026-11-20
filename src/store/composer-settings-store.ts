import { create } from 'zustand';

import { isComposerSettingsTab, type ComposerSettingsTab } from '@/lib/workspace/composer-settings';

/**
 * #405: the composer's settings dialog (the gear beside the send button). Whether it is open and which tab it shows
 * are this visit's UI state: nothing here is written to the server or to storage, and the last tab is only what the
 * dialog opens on next time. Any code that sends the person to a setting calls `openComposerSettings` with the tab
 * that holds it, instead of reaching into the dialog.
 */
interface ComposerSettingsState {
  open: boolean;
  /** The tab shown now, or last shown while closed. */
  tab: ComposerSettingsTab;
  /** Opens the dialog, on `tab` when given and on the last tab otherwise. */
  openSettings: (tab?: ComposerSettingsTab) => void;
  setOpen: (open: boolean) => void;
  setTab: (tab: ComposerSettingsTab) => void;
}

export const useComposerSettingsStore = create<ComposerSettingsState>((set) => ({
  open: false,
  tab: 'models',
  openSettings: (tab) => set((state) => ({ open: true, tab: tab && isComposerSettingsTab(tab) ? tab : state.tab })),
  setOpen: (open) => set({ open }),
  setTab: (tab) => set((state) => (isComposerSettingsTab(tab) ? { tab } : state)),
}));

/** Opens the composer's settings on the tab that holds a setting (the last tab when none is named). */
export function openComposerSettings(tab?: ComposerSettingsTab): void {
  useComposerSettingsStore.getState().openSettings(tab);
}
