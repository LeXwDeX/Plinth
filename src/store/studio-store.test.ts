import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  CHAT_WIDTH_DEFAULT,
  CHAT_WIDTH_MAX,
  CHAT_WIDTH_MIN,
  STUDIO_PREFS_KEY,
  clampChatWidth,
  readStudioPrefs,
  useStudioStore,
} from './studio-store';

function stubStorage(overrides: Partial<Storage> = {}) {
  const data = new Map<string, string>();
  const storage = {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => void data.set(key, value),
    removeItem: (key: string) => void data.delete(key),
    ...overrides,
  };
  vi.stubGlobal('localStorage', storage);
  return data;
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe('chat column width', () => {
  it('stays within 320 to 520 pixels', () => {
    expect(clampChatWidth(100)).toBe(CHAT_WIDTH_MIN);
    expect(clampChatWidth(900)).toBe(CHAT_WIDTH_MAX);
    expect(clampChatWidth(401.4)).toBe(401);
    expect(clampChatWidth(Number.NaN)).toBe(CHAT_WIDTH_DEFAULT);
  });

  it('is remembered in local storage', () => {
    const data = stubStorage();
    useStudioStore.getState().setChatWidth(444);
    useStudioStore.getState().setChatCollapsed(true);
    expect(JSON.parse(data.get(STUDIO_PREFS_KEY) ?? '{}')).toMatchObject({ chatWidth: 444, chatCollapsed: true });
    expect(readStudioPrefs()).toMatchObject({ chatWidth: 444, chatCollapsed: true });
  });

  it('still works when storage throws', () => {
    stubStorage({
      getItem: () => { throw new Error('blocked'); },
      setItem: () => { throw new Error('quota'); },
    });
    expect(readStudioPrefs().chatWidth).toBe(CHAT_WIDTH_DEFAULT);
    expect(() => useStudioStore.getState().setChatWidth(480)).not.toThrow();
    expect(useStudioStore.getState().chatWidth).toBe(480);
  });

  it('ignores a damaged stored value', () => {
    const data = stubStorage();
    data.set(STUDIO_PREFS_KEY, '{"chatWidth":"wide","inspector":"sideways"}');
    expect(readStudioPrefs()).toEqual({ chatWidth: CHAT_WIDTH_DEFAULT, chatCollapsed: false, railExpanded: false });
  });
});

describe('stage rail expansion', () => {
  beforeEach(() => {
    useStudioStore.setState({ chatWidth: CHAT_WIDTH_DEFAULT, chatCollapsed: false, railExpanded: false });
  });

  it('starts collapsed when nothing is stored', () => {
    stubStorage();
    expect(readStudioPrefs().railExpanded).toBe(false);
  });

  it('is remembered in the studio prefs, next to the chat column prefs', () => {
    const data = stubStorage();
    useStudioStore.getState().setChatWidth(400);
    useStudioStore.getState().setRailExpanded(true);
    expect(useStudioStore.getState().railExpanded).toBe(true);
    expect(JSON.parse(data.get(STUDIO_PREFS_KEY) ?? '{}')).toEqual({ chatWidth: 400, chatCollapsed: false, railExpanded: true });
    expect(readStudioPrefs().railExpanded).toBe(true);
    useStudioStore.getState().setRailExpanded(false);
    expect(readStudioPrefs()).toEqual({ chatWidth: 400, chatCollapsed: false, railExpanded: false });
  });

  it('keeps the other prefs when only the rail changes, and the rail when only they change', () => {
    const data = stubStorage();
    useStudioStore.getState().setChatCollapsed(true);
    useStudioStore.getState().setRailExpanded(true);
    useStudioStore.getState().setChatWidth(480);
    expect(JSON.parse(data.get(STUDIO_PREFS_KEY) ?? '{}')).toEqual({ chatWidth: 480, chatCollapsed: true, railExpanded: true });
  });

  it('reads a stored true only, and treats anything else as collapsed', () => {
    const data = stubStorage();
    data.set(STUDIO_PREFS_KEY, '{"railExpanded":true}');
    expect(readStudioPrefs().railExpanded).toBe(true);
    for (const stored of ['{"railExpanded":"yes"}', '{"railExpanded":1}', '{"railExpanded":null}', '{}', 'not json']) {
      data.set(STUDIO_PREFS_KEY, stored);
      expect(readStudioPrefs().railExpanded, stored).toBe(false);
    }
  });

  it('still works when storage throws', () => {
    stubStorage({
      getItem: () => { throw new Error('blocked'); },
      setItem: () => { throw new Error('quota'); },
    });
    expect(readStudioPrefs().railExpanded).toBe(false);
    expect(() => useStudioStore.getState().setRailExpanded(true)).not.toThrow();
    expect(useStudioStore.getState().railExpanded).toBe(true);
  });
});

describe('right panel tab memory', () => {
  beforeEach(() => {
    useStudioStore.setState({ panelTab2d: null, panelTab3d: null, processPreset: null });
  });

  it('starts with no remembered tab, so the Schema default shows', () => {
    expect(useStudioStore.getState()).toMatchObject({ panelTab2d: null, panelTab3d: null });
  });

  it('is kept for this visit only: nothing about it reaches local storage', () => {
    const data = stubStorage();
    data.set(STUDIO_PREFS_KEY, '{"chatWidth":400,"inspector":"closed","panelTab2d":"history"}');
    expect(readStudioPrefs()).toEqual({ chatWidth: 400, chatCollapsed: false, railExpanded: false });
    data.clear();
    useStudioStore.getState().setPanelTab('image', 'history');
    useStudioStore.getState().setPanelTab('model', 'parts');
    useStudioStore.getState().setChatWidth(420);
    expect(data.get(STUDIO_PREFS_KEY)).not.toMatch(/panelTab|history|parts/);
  });

  it('shares one choice between a picture and the bare board, and keeps a separate one for a model', () => {
    useStudioStore.getState().setPanelTab('image', 'history');
    expect(useStudioStore.getState()).toMatchObject({ panelTab2d: 'history', panelTab3d: null });
    useStudioStore.getState().setPanelTab('board', 'layers');
    expect(useStudioStore.getState()).toMatchObject({ panelTab2d: 'layers', panelTab3d: null });
    useStudioStore.getState().setPanelTab('model', 'parts');
    expect(useStudioStore.getState()).toMatchObject({ panelTab2d: 'layers', panelTab3d: 'parts' });
  });

  it('opens 加工 with the requested step chosen', () => {
    useStudioStore.getState().setPanelTab('model', 'details');
    useStudioStore.getState().showProcess('retexture');
    expect(useStudioStore.getState()).toMatchObject({ panelTab3d: 'process', processPreset: 'retexture' });
    useStudioStore.getState().clearProcessPreset();
    expect(useStudioStore.getState().processPreset).toBeNull();
  });

  it('leaves the 2D choice alone when 加工 is opened', () => {
    useStudioStore.getState().setPanelTab('image', 'history');
    useStudioStore.getState().showProcess('retexture');
    expect(useStudioStore.getState().panelTab2d).toBe('history');
  });
});

describe('composer prefill', () => {
  it('fires again for the same text', () => {
    useStudioStore.getState().prefillComposer('a');
    const first = useStudioStore.getState().composerPrefill?.seq;
    useStudioStore.getState().prefillComposer('a');
    expect(useStudioStore.getState().composerPrefill?.seq).toBe((first ?? 0) + 1);
  });
});

describe('version chain refresh', () => {
  it('bumps the revision and keeps the longest watch', () => {
    const before = useStudioStore.getState().versionsRevision;
    useStudioStore.getState().bumpVersions();
    expect(useStudioStore.getState().versionsRevision).toBe(before + 1);
    const now = Date.now();
    useStudioStore.getState().watchVersions(60_000);
    const first = useStudioStore.getState().versionsWatchUntil;
    expect(first).toBeGreaterThanOrEqual(now + 60_000);
    useStudioStore.getState().watchVersions(1_000);
    expect(useStudioStore.getState().versionsWatchUntil).toBe(first);
    expect(useStudioStore.getState().versionsRevision).toBe(before + 3);
  });
});

describe('compare notice', () => {
  beforeEach(() => {
    useStudioStore.setState({ compare: null, compareNotice: null });
  });

  it('keeps the reason a version was not taken until the next one is, and drops it with the comparison', () => {
    const store = useStudioStore.getState();
    store.startCompare();
    store.setCompareNotice('unavailable');
    expect(useStudioStore.getState().compareNotice).toBe('unavailable');
    useStudioStore.getState().setCompareNotice(null);
    expect(useStudioStore.getState().compareNotice).toBeNull();
    useStudioStore.getState().setCompareNotice('unavailable');
    useStudioStore.getState().endCompare();
    expect(useStudioStore.getState().compareNotice).toBeNull();
  });

  it('starts a new comparison without the old reason', () => {
    useStudioStore.getState().setCompareNotice('unavailable');
    useStudioStore.getState().startCompare({ mode: 'two', slots: [] });
    expect(useStudioStore.getState().compareNotice).toBeNull();
  });
});

describe('model parts', () => {
  it('keeps the stage parts, the chosen part and a numbered export request', () => {
    const store = useStudioStore.getState();
    store.setStageParts({ key: 't:a', parts: [{ id: 'p1', name: 'head', triangles: 10 }] });
    store.setSelectedPart({ key: 't:a', id: 'p1' });
    expect(useStudioStore.getState()).toMatchObject({ stageParts: { key: 't:a' }, selectedPart: { id: 'p1' } });
    store.requestPartExport('t:a', 'p1');
    const first = useStudioStore.getState().partExportRequest?.seq ?? 0;
    store.requestPartExport('t:a', 'p1');
    expect(useStudioStore.getState().partExportRequest).toEqual({ key: 't:a', id: 'p1', seq: first + 1 });
    store.clearPartExportRequest();
    expect(useStudioStore.getState().partExportRequest).toBeNull();
    store.setSelectedPart(null);
    store.setStageParts(null);
  });
});
