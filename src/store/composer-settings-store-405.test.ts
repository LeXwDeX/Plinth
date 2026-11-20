import { afterEach, describe, expect, it } from 'vitest';

import { openComposerSettings, useComposerSettingsStore } from '@/store/composer-settings-store';

// #405: the settings dialog's open state and last tab are this visit's UI state; any code that wants to send the
// person to a setting (a hint, a failed check) calls openComposerSettings with the tab that holds it.
afterEach(() => useComposerSettingsStore.setState({ open: false, tab: 'models' }));

describe('the composer settings store', () => {
  it('starts closed on the first tab', () => {
    const state = useComposerSettingsStore.getState();
    expect(state.open).toBe(false);
    expect(state.tab).toBe('models');
  });

  it('opens on the tab it is asked for', () => {
    openComposerSettings('skills');
    expect(useComposerSettingsStore.getState()).toMatchObject({ open: true, tab: 'skills' });
  });

  it('remembers the last tab when it is closed and opened again without one', () => {
    openComposerSettings('params');
    useComposerSettingsStore.getState().setOpen(false);
    expect(useComposerSettingsStore.getState()).toMatchObject({ open: false, tab: 'params' });
    openComposerSettings();
    expect(useComposerSettingsStore.getState()).toMatchObject({ open: true, tab: 'params' });
  });

  it('follows the tab the person switches to', () => {
    openComposerSettings('models');
    useComposerSettingsStore.getState().setTab('chat');
    expect(useComposerSettingsStore.getState().tab).toBe('chat');
    useComposerSettingsStore.getState().setOpen(false);
    openComposerSettings();
    expect(useComposerSettingsStore.getState().tab).toBe('chat');
  });

  it('ignores a tab id it does not know', () => {
    openComposerSettings('skills');
    useComposerSettingsStore.getState().setTab('scope' as never);
    expect(useComposerSettingsStore.getState().tab).toBe('skills');
  });
});
