import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  CANVAS_TOOL_IDS, VIEWER_TOOL_IDS, isToolId, toolHintContent, toolIds, toolSections, toolShortcutCaps, toolView,
} from './tools';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
const windows = { mac: false };
const mac = { mac: true };
const view = (id: string) => [...toolSections(schema, 'canvas'), ...toolSections(schema, 'viewer')].flatMap((group) => group.tools).find((item) => item.id === id)!;

describe('tool ids and the Schema table', () => {
  it('has a handler id for every tool the default catalog lists on each rail, and none the catalog lacks', () => {
    const declared = (rail: 'canvas' | 'viewer') => schema.canvas.frontend.tools.filter((tool) => tool.rail === rail).map((tool) => tool.id);
    expect([...declared('canvas')].sort()).toEqual([...CANVAS_TOOL_IDS].sort());
    expect([...declared('viewer')].sort()).toEqual([...VIEWER_TOOL_IDS].sort());
    for (const id of declared('canvas')) expect(isToolId('canvas', id), id).toBe(true);
    for (const id of declared('viewer')) expect(isToolId('viewer', id), id).toBe(true);
    expect(isToolId('canvas', 'texture')).toBe(false);
    expect(isToolId('viewer', 'select')).toBe(false);
    expect(isToolId('canvas', 'uv')).toBe(false);
  });

  it('words every tool from Schema messages: a name and a hint', () => {
    for (const tool of schema.canvas.frontend.tools) {
      expect(messages[tool.label_key], `${tool.id}: ${tool.label_key}`).toBeTruthy();
      expect(messages[tool.hint_key], `${tool.id}: ${tool.hint_key}`).toBeTruthy();
    }
    for (const key of ['ui.studio.rail.expand', 'ui.studio.rail.collapse', 'ui.studio.rail.expand_hint', 'ui.studio.rail.collapse_hint']) {
      expect(messages[key], key).toBeTruthy();
    }
  });

  it('has no cost fields: rail tools never call a model', () => {
    for (const tool of schema.canvas.frontend.tools) {
      for (const field of ['calls_model', 'cost_hint', 'target_key']) expect(tool, `${tool.id}.${field}`).not.toHaveProperty(field);
    }
  });
});

describe('which buttons a rail gets, and in what order', () => {
  it('lays the 2D rail out in four sections: select, draw, snap, history (UI spec §5)', () => {
    const sections = toolSections(schema, 'canvas');
    expect(sections.map((group) => group.section)).toEqual(['select', 'draw', 'snap', 'history']);
    expect(sections.map((group) => group.tools.map((tool) => tool.id))).toEqual([
      ['select', 'hand', 'marquee', 'lasso', 'mask', 'eyedropper', 'zoom'],
      ['upload', 'shape', 'text', 'pencil', 'brush', 'eraser', 'crop'],
      ['snap'],
      ['undo', 'redo'],
    ]);
  });

  it('puts the mask with the selection tools (R13), not with the drawing tools', () => {
    const select = toolSections(schema, 'canvas')[0].tools.map((tool) => tool.id);
    const draw = toolSections(schema, 'canvas')[1].tools.map((tool) => tool.id);
    expect(select).toContain('mask');
    expect(draw).not.toContain('mask');
  });

  it('gives the 3D view bar texture, clay and wire in one section, and no UV', () => {
    const sections = toolSections(schema, 'viewer');
    expect(sections.map((group) => group.section)).toEqual(['views']);
    expect(toolIds(schema, 'viewer')).toEqual(['texture', 'clay', 'wire']);
    expect(schema.canvas.frontend.tools.some((tool) => /uv/i.test(tool.id))).toBe(false);
  });

  it('draws no button for a tool id the code has no handler for, and no section left empty', () => {
    const extended = structuredClone(schema);
    extended.canvas.frontend.tools.push({ ...extended.canvas.frontend.tools[0], id: 'mystery' });
    extended.canvas.frontend.tools.push({ ...extended.canvas.frontend.tools[0], id: 'ghost', section: 'history', rail: 'canvas' });
    expect(toolIds(extended, 'canvas')).not.toContain('mystery');
    expect(toolIds(extended, 'canvas')).not.toContain('ghost');
    expect(toolIds(extended, 'canvas')).toHaveLength(CANVAS_TOOL_IDS.length);
    const onlyGhost = structuredClone(schema);
    onlyGhost.canvas.frontend.tools = [{ ...schema.canvas.frontend.tools[0], id: 'ghost' }];
    expect(toolSections(onlyGhost, 'canvas')).toEqual([]);
  });

  it('keeps a viewer tool off the canvas rail and the other way round', () => {
    const moved = structuredClone(schema);
    moved.canvas.frontend.tools.find((tool) => tool.id === 'clay')!.rail = 'canvas';
    expect(toolIds(moved, 'canvas')).not.toContain('clay');
    expect(toolIds(moved, 'viewer')).toEqual(['texture', 'wire']);
  });

  it('follows the Schema order, not the code order, for buttons and for sections', () => {
    const reordered = structuredClone(schema);
    reordered.canvas.frontend.tools = reordered.canvas.frontend.tools.filter((tool) => tool.rail === 'canvas').reverse();
    expect(toolSections(reordered, 'canvas').map((group) => group.section)).toEqual(['history', 'snap', 'draw', 'select']);
    expect(toolIds(reordered, 'canvas').slice(0, 2)).toEqual(['redo', 'undo']);
  });

  it('groups a section by name even if the Schema interleaves it with another', () => {
    const interleaved = structuredClone(schema);
    const [select, hand] = ['select', 'hand'].map((id) => interleaved.canvas.frontend.tools.find((tool) => tool.id === id)!);
    hand.section = 'draw';
    interleaved.canvas.frontend.tools = [select, hand, ...interleaved.canvas.frontend.tools.filter((tool) => tool !== select && tool !== hand)];
    const sections = toolSections(interleaved, 'canvas');
    expect(sections.filter((group) => group.section === 'select')).toHaveLength(1);
    expect(sections[0].tools.map((tool) => tool.id)).toEqual(['select', 'marquee', 'lasso', 'mask', 'eyedropper', 'zoom']);
  });
});

describe('names, hints and the renamed tools', () => {
  it('words the unified names (UI spec §10): 蒙版 and 画笔, not 局部重绘蒙版 and 像素画笔', () => {
    expect(view('mask').label).toBe('蒙版');
    expect(view('brush').label).toBe('画笔');
    expect(view('snap').label).toBe('吸附');
    for (const tool of schema.canvas.frontend.tools) {
      expect(messages[tool.label_key]).not.toMatch(/局部重绘蒙版|像素画笔/);
      expect(messages[tool.hint_key]).not.toMatch(/局部重绘蒙版|像素画笔/);
    }
    for (const text of Object.values(messages)) expect(text).not.toMatch(/局部重绘蒙版|像素画笔/);
    expect(messages['ui.shortcuts.item.tool_mask']).toBe('蒙版');
  });

  it('names the 3D views after what the viewer shows', () => {
    expect(toolSections(schema, 'viewer')[0].tools.map((tool) => tool.label)).toEqual(['贴图', '白模', '线框']);
  });

  it('writes every hint as short sentences with no cost line', () => {
    for (const tool of schema.canvas.frontend.tools) {
      const hint = messages[tool.hint_key];
      expect(hint.length, tool.id).toBeLessThanOrEqual(40);
      expect(hint, tool.id).not.toMatch(/积分|✦|会调用模型|免费/);
    }
  });

  it('carries no unused tooltip texts and no UV texts any more', () => {
    for (const key of Object.keys(messages)) {
      expect(key, key).not.toMatch(/^ui\.canvas\.toolbar\..+_tooltip$/);
      expect(key, key).not.toMatch(/^ui\.studio\.stage\.view\.uv/);
    }
  });
});

describe('shortcuts', () => {
  it('prints a tool key bare, and with its modifiers when the Schema lists them', () => {
    expect(toolShortcutCaps(schema, view('brush'), windows)).toEqual(['B']);
    expect(toolShortcutCaps(schema, view('pencil'), windows)).toEqual(['Shift', 'B']);
    expect(toolShortcutCaps(schema, view('undo'), windows)).toEqual(['Ctrl', 'Z']);
    expect(toolShortcutCaps(schema, view('redo'), windows)).toEqual(['Ctrl', 'Shift', 'Z']);
    expect(toolShortcutCaps(schema, view('undo'), mac)).toEqual(['⌘', 'Z']);
    expect(toolShortcutCaps(schema, view('redo'), mac)).toEqual(['⌘', '⇧', 'Z']);
    expect(toolShortcutCaps(schema, view('mask'), windows)).toEqual(['Q']);
  });

  it('prints nothing for a tool without a shortcut, or whose setting is not a key', () => {
    for (const id of ['upload', 'snap', 'texture', 'clay', 'wire']) expect(toolShortcutCaps(schema, view(id), windows), id).toBeUndefined();
    const broken = { shortcut: 'no_such_key', modifiers: ['mod'] as const };
    expect(toolShortcutCaps(schema, broken, windows)).toBeUndefined();
    expect(toolShortcutCaps(schema, { shortcut: 'delete_keys', modifiers: [] }, windows)).toBeUndefined();
  });

  it('reads the key from the keyboard setting, so a rebound key changes the hint', () => {
    const rebound = structuredClone(schema);
    rebound.canvas.frontend.keyboard.brush_tool_key = 'n';
    expect(toolShortcutCaps(rebound, toolView(rebound, rebound.canvas.frontend.tools.find((tool) => tool.id === 'pencil')!), windows)).toEqual(['Shift', 'N']);
  });

  it('gives every shortcut a tool names a key the keyboard Schema really holds', () => {
    const keyboard = schema.canvas.frontend.keyboard as unknown as Record<string, unknown>;
    for (const tool of schema.canvas.frontend.tools) {
      if (!tool.shortcut) continue;
      expect(typeof keyboard[tool.shortcut], `${tool.id}: ${tool.shortcut}`).toBe('string');
    }
  });
});

describe('the hover hint of a rail button', () => {
  it('is the name with its shortcut, then what it does, and nothing about cost or target', () => {
    const content = toolHintContent(schema, view('brush'), { platform: windows });
    expect(content).toEqual({ name: '画笔', shortcut: ['B'], hint: messages['ui.canvas.toolbar.brush_hint'] });
    expect(Object.keys(content)).not.toContain('cost');
    expect(Object.keys(content)).not.toContain('target');
  });

  it('leads with the reason when the button cannot be used right now', () => {
    const reason = '没有可以撤销的操作。';
    const content = toolHintContent(schema, view('undo'), { reason, platform: windows });
    expect(content.reason).toBe(reason);
    expect(content.shortcut).toEqual(['Ctrl', 'Z']);
    expect(Object.keys(content)[0]).toBe('name');
  });

  it('omits the shortcut for a tool that has none', () => {
    expect(toolHintContent(schema, view('wire'), { platform: windows })).toEqual({ name: '线框', hint: messages['ui.studio.stage.view.wire_hint'] });
  });
});
