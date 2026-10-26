import { beforeEach, describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { matchesShortcut } from '@/lib/canvas/shortcut';
import type { ProductSchema } from '@/lib/product-schema';
import { useStudioStore } from '@/store/studio-store';
import {
  activePanelTab, ALL_PANEL_TABS_AVAILABLE, defaultPanelTab, isPanelTabId, PANEL_TAB_IDS, panelContextOf, panelFamilyOf,
  panelShortcutTabs, panelTabHintContent, panelTabIds, panelTabViews, panelWidth, steppedPanelTab,
} from './panel-tabs';

const schema = defaultCatalog.application as unknown as ProductSchema;
const windows = { mac: false };
const noProcess = { unavailable: ['process'] } as const;

/** A schema whose panel_tabs table is replaced, to prove the helpers read the table and hold none of their own. */
function withTabs(change: (tabs: ProductSchema['canvas']['frontend']['panel_tabs']) => void): ProductSchema {
  const copy = structuredClone(schema);
  change(copy.canvas.frontend.panel_tabs);
  return copy;
}

describe('which tabs a kind of thing gets', () => {
  it('reads the Schema table: a picture, the bare board and a model', () => {
    expect(panelTabIds(schema, 'image')).toEqual(['layers', 'history', 'details', 'operations']);
    expect(panelTabIds(schema, 'board')).toEqual(['layers', 'history', 'operations']);
    expect(panelTabIds(schema, 'model')).toEqual(['operations', 'process', 'formats', 'parts', 'details']);
    expect(panelWidth(schema)).toBe(300);
  });

  it('has a body in code for every tab id the default table lists', () => {
    for (const tab of schema.canvas.frontend.panel_tabs.tabs) expect(isPanelTabId(tab.id), tab.id).toBe(true);
    expect([...schema.canvas.frontend.panel_tabs.tabs.map((tab) => tab.id)].sort()).toEqual([...PANEL_TAB_IDS].sort());
  });

  it('follows the Schema order and set, not a list of its own', () => {
    const changed = withTabs((tabs) => { tabs.kinds.find((entry) => entry.kind === 'image')!.tabs = ['details', 'layers']; });
    expect(panelTabIds(changed, 'image')).toEqual(['details', 'layers']);
    expect(panelTabIds(schema, 'image')).toEqual(['layers', 'history', 'details', 'operations']);
  });

  it('leaves out an id the code has no body for, and one the table does not declare', () => {
    const unknown = withTabs((tabs) => { tabs.kinds.find((entry) => entry.kind === 'board')!.tabs = ['layers', 'sparkle', 'history']; });
    expect(panelTabIds(unknown, 'board')).toEqual(['layers', 'history']);
    const undeclared = withTabs((tabs) => { tabs.tabs = tabs.tabs.filter((tab) => tab.id !== 'history'); });
    expect(panelTabIds(undeclared, 'board')).toEqual(['layers', 'operations']);
  });

  it('words every tab from Schema messages: name, one-line hint, icon and shortcut setting', () => {
    for (const kind of ['image', 'board', 'model'] as const) {
      for (const view of panelTabViews(schema, kind)) {
        expect(view.label, `${kind}/${view.id}`).not.toBe('');
        expect(view.hint, `${kind}/${view.id}`).not.toBe('');
        expect(view.icon, `${kind}/${view.id}`).not.toBe('');
      }
    }
    const names = Object.fromEntries(panelTabViews(schema, 'model').map((view) => [view.id, view.label]));
    expect(names).toEqual({ operations: '功能', process: '加工', formats: '格式', parts: '部件', details: '详情' });
    expect(panelTabViews(schema, 'image').map((view) => view.shortcut)).toEqual(['layers_panel_shortcut', 'history_panel_shortcut', undefined, undefined]);
  });
});

describe('what the panel is showing', () => {
  it('is the object on the stage, else the board when it has something on it, else nothing', () => {
    expect(panelContextOf({ kind: 'image' }, 0)).toBe('image');
    expect(panelContextOf({ kind: 'image' }, 4)).toBe('image');
    expect(panelContextOf({ kind: 'model' }, 0)).toBe('model');
    expect(panelContextOf(null, 3)).toBe('board');
    expect(panelContextOf(null, 0)).toBe('none');
  });

  it('shares the 2D choice between a picture and the board, and keeps 3D apart', () => {
    expect(panelFamilyOf('image')).toBe(panelFamilyOf('board'));
    expect(panelFamilyOf('model')).not.toBe(panelFamilyOf('image'));
  });
});

describe('the tab a kind opens on', () => {
  it('is the Schema default of the kind', () => {
    expect(defaultPanelTab(schema, 'image', ALL_PANEL_TABS_AVAILABLE)).toBe('layers');
    expect(defaultPanelTab(schema, 'board', ALL_PANEL_TABS_AVAILABLE)).toBe('layers');
    expect(defaultPanelTab(schema, 'model', ALL_PANEL_TABS_AVAILABLE)).toBe('operations');
  });

  it('keeps function settings as the default while processing is unavailable', () => {
    expect(defaultPanelTab(schema, 'model', noProcess)).toBe('operations');
    // A kind whose default is available, or that names no fallback, is not moved.
    expect(defaultPanelTab(schema, 'image', noProcess)).toBe('layers');
    const noFallback = withTabs((tabs) => { delete tabs.kinds.find((entry) => entry.kind === 'model')!.default_when_unavailable; });
    expect(defaultPanelTab(noFallback, 'model', noProcess)).toBe('operations');
  });

  it('reads the default and the fallback from the table', () => {
    const changed = withTabs((tabs) => {
      const model = tabs.kinds.find((entry) => entry.kind === 'model')!;
      model.default = 'formats';
      model.default_when_unavailable = 'parts';
    });
    expect(defaultPanelTab(changed, 'model', ALL_PANEL_TABS_AVAILABLE)).toBe('formats');
    expect(defaultPanelTab(changed, 'model', { unavailable: ['formats'] })).toBe('parts');
  });

  it('is the first tab the kind offers when the table names a default it does not offer', () => {
    const broken = withTabs((tabs) => { tabs.kinds.find((entry) => entry.kind === 'image')!.default = 'parts'; });
    expect(defaultPanelTab(broken, 'image', ALL_PANEL_TABS_AVAILABLE)).toBe('layers');
  });

  it('is nothing for a kind that offers no tab', () => {
    const empty = withTabs((tabs) => { tabs.kinds.find((entry) => entry.kind === 'board')!.tabs = []; });
    expect(defaultPanelTab(empty, 'board', ALL_PANEL_TABS_AVAILABLE)).toBeNull();
    expect(activePanelTab(empty, 'board', 'layers', ALL_PANEL_TABS_AVAILABLE)).toBeNull();
  });
});

describe('the tab shown with a remembered choice', () => {
  it('is the remembered tab when the kind offers it', () => {
    expect(activePanelTab(schema, 'image', 'history', ALL_PANEL_TABS_AVAILABLE)).toBe('history');
    expect(activePanelTab(schema, 'board', 'history', ALL_PANEL_TABS_AVAILABLE)).toBe('history');
    expect(activePanelTab(schema, 'model', 'parts', ALL_PANEL_TABS_AVAILABLE)).toBe('parts');
  });

  it('wins over the fallback: a person who chose 加工 keeps it even when it has nothing to offer', () => {
    expect(activePanelTab(schema, 'model', 'process', noProcess)).toBe('process');
  });

  it('is the default when the kind does not offer the remembered tab (2D remembered 详情, now the board)', () => {
    expect(activePanelTab(schema, 'image', 'details', ALL_PANEL_TABS_AVAILABLE)).toBe('details');
    expect(activePanelTab(schema, 'board', 'details', ALL_PANEL_TABS_AVAILABLE)).toBe('layers');
    expect(activePanelTab(schema, 'image', 'parts', ALL_PANEL_TABS_AVAILABLE)).toBe('layers');
    expect(activePanelTab(schema, 'model', 'layers', noProcess)).toBe('operations');
  });

  it('is the default when nothing was chosen', () => {
    expect(activePanelTab(schema, 'image', null, ALL_PANEL_TABS_AVAILABLE)).toBe('layers');
    expect(activePanelTab(schema, 'model', null, ALL_PANEL_TABS_AVAILABLE)).toBe('operations');
  });
});

describe('tab memory in the studio store', () => {
  beforeEach(() => {
    useStudioStore.setState({ panelTab2d: null, panelTab3d: null });
  });
  const shown = (kind: 'image' | 'board' | 'model', availability = ALL_PANEL_TABS_AVAILABLE) => {
    const state = useStudioStore.getState();
    return activePanelTab(schema, kind, kind === 'model' ? state.panelTab3d : state.panelTab2d, availability);
  };

  it('keeps 历史 for a picture and for the board, and 部件 for a model, each in its own slot', () => {
    useStudioStore.getState().setPanelTab('image', 'history');
    useStudioStore.getState().setPanelTab('model', 'parts');
    expect(shown('image')).toBe('history');
    expect(shown('board')).toBe('history');
    expect(shown('model')).toBe('parts');
  });

  it('shows the board default again after 2D remembered 详情 on a picture', () => {
    useStudioStore.getState().setPanelTab('image', 'details');
    expect(shown('image')).toBe('details');
    expect(shown('board')).toBe('layers');
    // The memory itself is kept: the picture comes back on 详情.
    expect(shown('image')).toBe('details');
  });

  it('opens 加工 for a model whatever was remembered, through showProcess', () => {
    useStudioStore.getState().setPanelTab('model', 'details');
    useStudioStore.getState().showProcess('retexture');
    expect(shown('model', noProcess)).toBe('process');
  });
});

describe('F7 and Alt+F9', () => {
  const key = (key: string, code: string, extra: { altKey?: boolean } = {}) => ({
    key, code, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...extra,
  });
  const pressed = (event: ReturnType<typeof key>) => panelShortcutTabs(schema).find((entry) => matchesShortcut(event, entry.descriptor))?.id ?? null;

  beforeEach(() => {
    useStudioStore.setState({ panelTab2d: null, panelTab3d: null });
  });

  it('takes the keys from the keyboard settings the tabs name', () => {
    expect(panelShortcutTabs(schema)).toEqual([
      { id: 'layers', descriptor: schema.canvas.frontend.keyboard.layers_panel_shortcut },
      { id: 'history', descriptor: schema.canvas.frontend.keyboard.history_panel_shortcut },
    ]);
  });

  it('switch to the 图层 and 历史 tabs and nothing else', () => {
    expect(pressed(key('F7', 'F7'))).toBe('layers');
    expect(pressed(key('F9', 'F9', { altKey: true }))).toBe('history');
    expect(pressed(key('F9', 'F9'))).toBeNull();
    expect(pressed(key('F8', 'F8'))).toBeNull();
  });

  it('only select a tab: pressing a key again leaves the same tab, and no panel is ever closed', () => {
    for (const event of [key('F9', 'F9', { altKey: true }), key('F9', 'F9', { altKey: true })]) {
      const id = pressed(event);
      expect(id).toBe('history');
      useStudioStore.getState().setPanelTab('image', id!);
      expect(activePanelTab(schema, 'image', useStudioStore.getState().panelTab2d, ALL_PANEL_TABS_AVAILABLE)).toBe('history');
    }
    useStudioStore.getState().setPanelTab('image', pressed(key('F7', 'F7'))!);
    expect(useStudioStore.getState().panelTab2d).toBe('layers');
    // The 3D choice is untouched.
    expect(useStudioStore.getState().panelTab3d).toBeNull();
  });

  it('skips a tab whose setting holds no key, and a tab the code has no body for', () => {
    const changed = withTabs((tabs) => {
      tabs.tabs.find((tab) => tab.id === 'layers')!.shortcut = 'no_such_setting';
      tabs.tabs.push({ id: 'sparkle', icon: 'info', label_key: 'a', hint_key: 'b', shortcut: 'history_panel_shortcut' });
    });
    expect(panelShortcutTabs(changed).map((entry) => entry.id)).toEqual(['history']);
  });
});

describe('hover hint of a tab', () => {
  it('has the name, the shortcut caps (when it has a key) and one line of hint', () => {
    const [layers, history, details] = panelTabViews(schema, 'image');
    expect(panelTabHintContent(schema, layers, windows)).toMatchObject({ name: '图层', shortcut: ['F7'], hint: layers.hint });
    expect(panelTabHintContent(schema, history, windows)).toMatchObject({ name: '历史', shortcut: ['Alt', 'F9'] });
    const noKey = panelTabHintContent(schema, details, windows);
    expect(noKey).toMatchObject({ name: '详情' });
    expect(noKey).not.toHaveProperty('shortcut');
  });
});

describe('moving between tabs with the keyboard', () => {
  const ids = ['operations', 'process', 'formats', 'parts', 'details'] as const;
  it('steps with Left and Right and wraps', () => {
    expect(steppedPanelTab(ids, 'process', 'ArrowRight')).toBe('formats');
    expect(steppedPanelTab(ids, 'details', 'ArrowRight')).toBe('operations');
    expect(steppedPanelTab(ids, 'operations', 'ArrowLeft')).toBe('details');
    expect(steppedPanelTab(ids, 'parts', 'ArrowLeft')).toBe('formats');
  });

  it('jumps with Home and End and ignores every other key', () => {
    expect(steppedPanelTab(ids, 'parts', 'Home')).toBe('operations');
    expect(steppedPanelTab(ids, 'parts', 'End')).toBe('details');
    expect(steppedPanelTab(ids, 'parts', 'Enter')).toBeNull();
    expect(steppedPanelTab([], 'parts', 'Home')).toBeNull();
  });
});
