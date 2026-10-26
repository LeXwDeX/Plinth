import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import {
  descriptorCaps, focusAreaOf, keyBindingsOf, resolveKey, shortcutSections,
  type DispatchContext, type DispatchEvent,
} from './key-dispatch';
import { toolSections, toolShortcutCaps } from './tools';

const schema = defaultCatalog.application as unknown as ProductSchema;
const bindings = keyBindingsOf(schema);
const text = (key: string) => schemaText(schema, key);
const windows = { mac: false };

const base: DispatchContext = {
  resultDetailOpen: false, shortcutsOpen: false, lightboxOpen: false, compareOpen: false,
  popupOpen: false, surface: 'canvas', canvasTool: 'select', canvasSelection: 0,
};
const press = (key: string, extra: Partial<DispatchEvent> = {}): DispatchEvent => ({
  key, code: key === ' ' ? 'Space' : key, ctrlKey: false, metaKey: false, altKey: false, target: null, ...extra,
});
const typing = { closest: (selector: string) => (selector.includes('textarea') ? {} : null), isContentEditable: false };
const button = { closest: (selector: string) => (selector.includes('button') ? { matches: () => false } : null) };
const viewer = { tagName: 'MODEL-VIEWER', closest: () => null };
const typeOf = (event: DispatchEvent, context: Partial<DispatchContext> = {}) => resolveKey(event, { ...base, ...context }, bindings)?.command.type ?? null;

describe('focus areas', () => {
  it('puts a text field first, then an open modal, then the surface under it', () => {
    expect(focusAreaOf(typing, { ...base, resultDetailOpen: true })).toBe('input');
    expect(focusAreaOf(null, { ...base, resultDetailOpen: true })).toBe('dialog');
    expect(focusAreaOf(null, { ...base, shortcutsOpen: true })).toBe('dialog');
    expect(focusAreaOf(null, base)).toBe('canvas');
    expect(focusAreaOf(null, { ...base, surface: 'model' })).toBe('stage');
    // The large view is the topmost layer, even over a dialog.
    expect(focusAreaOf(null, { ...base, resultDetailOpen: true, lightboxOpen: true })).toBe('stage');
  });
});

describe('resolveKey precedence', () => {
  it('never fires while typing, whatever is open', () => {
    for (const key of [bindings.helpKey, bindings.previousKey, bindings.nextKey, bindings.closeKey, ' ']) {
      expect(typeOf(press(key, { target: typing }), { lightboxOpen: true, compareOpen: true, resultDetailOpen: true })).toBeNull();
    }
    expect(typeOf(press(bindings.recenterKey, { ctrlKey: true, target: typing }), { surface: 'model' })).toBeNull();
  });

  it('lets a composing key press through untouched', () => {
    expect(typeOf(press(bindings.nextKey, { isComposing: true }))).toBeNull();
    expect(typeOf(press(bindings.closeKey, { isComposing: true }), { lightboxOpen: true })).toBeNull();
  });

  it('arrows step the versions, but a selection in the canvas select tool keeps them for the nudge', () => {
    expect(typeOf(press(bindings.nextKey))).toBe('stage-step');
    expect(typeOf(press(bindings.nextKey), { canvasSelection: 2 })).toBeNull();
    expect(typeOf(press(bindings.nextKey), { canvasSelection: 2, canvasTool: 'hand' })).toBe('stage-step');
    expect(typeOf(press(bindings.nextKey), { canvasSelection: 2, surface: 'model' })).toBe('stage-step');
    expect(typeOf(press(bindings.nextKey), { compareOpen: true })).toBeNull();
  });

  it('the large view takes the arrows before the nudge and the stage', () => {
    const result = resolveKey(press(bindings.previousKey), { ...base, lightboxOpen: true, canvasSelection: 3 }, bindings);
    expect(result?.command).toEqual({ type: 'lightbox-step', delta: -1 });
    expect(result?.preventDefault).toBe(true);
  });

  it('a control that owns the keys keeps Space and the arrows', () => {
    expect(typeOf(press(' ', { target: button }))).toBeNull();
    expect(typeOf(press(bindings.nextKey, { target: button }))).toBeNull();
    expect(typeOf(press(' '))).toBe('lightbox-gesture');
  });

  it('Esc closes one layer at a time: large view, popup, comparison; a mask keeps its own Esc', () => {
    const esc = press(bindings.closeKey);
    const everything = { lightboxOpen: true, popupOpen: true, compareOpen: true, canvasTool: 'mask' };
    expect(resolveKey(esc, { ...base, ...everything }, bindings)).toMatchObject({ command: { type: 'lightbox-close' }, stopPropagation: true });
    expect(typeOf(esc, { ...everything, lightboxOpen: false })).toBeNull();
    expect(resolveKey(esc, { ...base, ...everything, lightboxOpen: false, popupOpen: false }, bindings)).toMatchObject({ command: { type: 'compare-end' }, stopPropagation: true });
    expect(typeOf(esc, { ...everything, lightboxOpen: false, popupOpen: false, compareOpen: false })).toBeNull();
    expect(typeOf(esc, { canvasTool: 'mask' })).toBeNull();
    expect(typeOf(esc)).toBeNull();
  });

  it('has no focus mode: F does nothing and no focus command exists', () => {
    expect(typeOf(press('f'))).toBeNull();
    expect(typeOf(press('F'), { surface: 'model' })).toBeNull();
    expect('focusKey' in bindings).toBe(false);
    const stageItems = shortcutSections(schema, windows).find((section) => section.area === 'stage')?.items.map((item) => item.id);
    expect(stageItems).not.toContain('focus');
  });

  it('ignores Ctrl/Alt chords it does not own', () => {
    expect(typeOf(press(bindings.nextKey, { ctrlKey: true }))).toBeNull();
    expect(typeOf(press(bindings.previousKey, { altKey: true }))).toBeNull();
    expect(typeOf(press('a', { ctrlKey: true }))).toBeNull();
  });
});

describe('Ctrl+0', () => {
  const chord = press(bindings.recenterKey, { ctrlKey: true });

  it('resets the 3D view when a model is on the stage', () => {
    expect(resolveKey(chord, { ...base, surface: 'model' }, bindings)).toMatchObject({ command: { type: 'view-reset' }, preventDefault: true });
    expect(typeOf(press(bindings.recenterKey, { metaKey: true }), { surface: 'model' })).toBe('view-reset');
  });

  it('leaves the canvas to the board, which recenters, so only one of them acts', () => {
    expect(typeOf(chord, { surface: 'canvas' })).toBeNull();
  });

  it('does nothing under a dialog or without the exact chord', () => {
    expect(typeOf(chord, { surface: 'model', resultDetailOpen: true })).toBeNull();
    expect(typeOf(press(bindings.recenterKey), { surface: 'model' })).toBeNull();
  });
});

describe('result detail dialog', () => {
  it('pages through the group with the arrows and leaves Esc to the dialog', () => {
    expect(resolveKey(press(bindings.nextKey), { ...base, resultDetailOpen: true }, bindings)).toMatchObject({ command: { type: 'detail-step', delta: 1 }, preventDefault: true });
    expect(typeOf(press(bindings.previousKey), { resultDetailOpen: true })).toBe('detail-step');
    expect(typeOf(press(bindings.closeKey), { resultDetailOpen: true })).toBeNull();
  });

  it('is modal: the stage keys do not fire behind it, and a 3D viewer keeps its arrows', () => {
    for (const key of [' ', 'f']) expect(typeOf(press(key), { resultDetailOpen: true })).toBeNull();
    expect(typeOf(press(bindings.nextKey, { target: viewer }), { resultDetailOpen: true })).toBeNull();
  });
});

describe('shortcut panel key', () => {
  it('toggles the panel from the canvas and the stage', () => {
    expect(typeOf(press(bindings.helpKey))).toBe('shortcuts-toggle');
    expect(typeOf(press(bindings.helpKey), { surface: 'model' })).toBe('shortcuts-toggle');
  });

  it('closes the panel with the same key and keeps every other key while it is open', () => {
    expect(typeOf(press(bindings.helpKey), { shortcutsOpen: true })).toBe('shortcuts-toggle');
    expect(typeOf(press(bindings.nextKey), { shortcutsOpen: true })).toBeNull();
    expect(typeOf(press(' '), { shortcutsOpen: true })).toBeNull();
  });

  it('stays out of the way of other dialogs and menus', () => {
    expect(typeOf(press(bindings.helpKey), { resultDetailOpen: true })).toBeNull();
    expect(typeOf(press(bindings.helpKey), { popupOpen: true })).toBeNull();
  });
});

describe('shortcut display from the Schema', () => {
  it('prints modifiers per platform', () => {
    expect(descriptorCaps('mod+alt+shift+e', text, windows)).toEqual(['Ctrl', 'Alt', 'Shift', 'E']);
    expect(descriptorCaps('mod+/', text, { mac: true })).toEqual(['⌘', '/']);
  });

  it('prints a key setting as the caps a hover hint shows beside the name', () => {
    expect(descriptorCaps(schema.canvas.frontend.keyboard.brush_tool_key, text, windows)).toEqual(['B']);
    expect(descriptorCaps(`mod+${schema.canvas.frontend.keyboard.undo_key}`, text, windows)).toEqual(['Ctrl', 'Z']);
    expect(descriptorCaps(schema.canvas.frontend.keyboard.space_code, text, windows)).toEqual([text('ui.shortcuts.key.space')]);
  });

  it('prints the same keys beside a rail tool as the shortcut panel lists for it', () => {
    const canvas = shortcutSections(schema, windows).find((section) => section.area === 'canvas')!;
    const tools = toolSections(schema, 'canvas').flatMap((group) => group.tools);
    const printed = (id: string) => toolShortcutCaps(schema, tools.find((tool) => tool.id === id)!, windows);
    const listed = (id: string) => canvas.items.find((item) => item.id === id)?.keys ?? [];
    expect(listed('undo')).toContainEqual(printed('undo'));
    expect(listed('redo')).toContainEqual(printed('redo'));
    expect(listed('tool_brush')).toContainEqual(printed('brush'));
    expect(listed('tool_brush')).toContainEqual(printed('pencil'));
    for (const [item, tool] of [
      ['tool_select', 'select'], ['tool_hand', 'hand'], ['tool_marquee', 'marquee'], ['tool_marquee', 'lasso'], ['tool_text', 'text'],
      ['tool_shape', 'shape'], ['tool_eraser', 'eraser'], ['tool_crop', 'crop'], ['tool_mask', 'mask'], ['tool_zoom', 'zoom'],
      ['tool_eyedropper', 'eyedropper'],
    ] as const) {
      expect(listed(item), `${item} lists the key printed beside ${tool}`).toContainEqual(printed(tool));
    }
  });

  it('calls the mask tool 蒙版 in the shortcut panel, like the rail', () => {
    const canvas = shortcutSections(schema, windows).find((section) => section.area === 'canvas')!;
    expect(canvas.items.find((item) => item.id === 'tool_mask')?.label).toBe('蒙版');
    expect(text('ui.canvas.toolbar.mask')).toBe('蒙版');
    expect(canvas.items.find((item) => item.id === 'tool_brush')?.label).not.toContain('像素');
  });

  it('lists Esc beside the deselect shortcut in the canvas section', () => {
    const canvas = shortcutSections(schema, windows).find((section) => section.area === 'canvas');
    const deselect = canvas?.items.find((item) => item.id === 'deselect');
    expect(deselect?.keys.flat()).toContain('Esc');
  });

  it('lists F7 and Alt+F9 as switching the right panel to 图层 and 历史, from the Schema', () => {
    const canvas = shortcutSections(schema, windows).find((section) => section.area === 'canvas')!;
    const layers = canvas.items.find((item) => item.id === 'panel_layers');
    const history = canvas.items.find((item) => item.id === 'panel_history');
    expect(layers?.label).toBe('切到图层');
    expect(history?.label).toBe('切到历史');
    expect(layers?.keys).toEqual([['F7']]);
    expect(history?.keys).toEqual([['Alt', 'F9']]);
    // The words say nothing about opening, closing or showing a panel: the keys only switch tabs.
    for (const label of [layers?.label ?? '', history?.label ?? '']) expect(label).not.toMatch(/显示|隐藏|打开|关闭/);
  });

  it('lists the shortcuts of every area from the key settings, with text from the Schema', () => {
    const sections = shortcutSections(schema, windows);
    expect(sections.map((section) => section.area)).toEqual(['global', 'stage', 'canvas', 'dialog']);
    const ids = sections.flatMap((section) => section.items.map((item) => item.id));
    for (const required of ['help', 'lightbox', 'step_versions', 'escape', 'reset_view', 'recenter', 'select_all', 'nudge', 'detail_step']) expect(ids).toContain(required);
    for (const section of sections) {
      expect(section.title).not.toBe('');
      for (const item of section.items) {
        expect(item.label, item.id).not.toBe('');
        expect(item.keys.length, item.id).toBeGreaterThan(0);
      }
    }
    const help = sections[0].items[0];
    expect(help.keys).toEqual([['?']]);
    const reset = sections[1].items.find((item) => item.id === 'reset_view');
    expect(reset?.keys).toEqual([['Ctrl', '0']]);
  });
});
