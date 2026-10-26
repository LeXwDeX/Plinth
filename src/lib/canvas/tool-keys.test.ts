import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { CanvasToolType, ShapeType } from '@/store/canvas-store';
import { toolSections } from '@/lib/workspace/tools';
import { layerDraggable, nextShape, pansView, resolveToolKey } from './tool-keys';

const keyboard = (defaultCatalog.application as unknown as ProductSchema).canvas.frontend.keyboard;
const press = (key: string, tool: CanvasToolType = 'select', shapeType: ShapeType = 'rectangle', shiftKey = false) =>
  resolveToolKey({ key, shiftKey }, keyboard, { tool, shapeType });

describe('Photoshop tool keys (Schema canvas.frontend.keyboard)', () => {
  it.each([
    ['v', 'select'], ['h', 'hand'], ['m', 'marquee'], ['l', 'lasso'], ['q', 'mask'], ['c', 'crop'],
    ['b', 'brush'], ['e', 'eraser'], ['t', 'text'], ['u', 'shape'], ['z', 'zoom'], ['i', 'eyedropper'],
  ])('%s selects the %s tool', (key, tool) => {
    expect(press(key)?.tool).toBe(tool);
    expect(press(key.toUpperCase())?.tool).toBe(tool);
  });

  it('has no key for the pencil, the old rectangle, line, ellipse and generate keys', () => {
    for (const key of ['p', 'r', 'o', 'k', 'a', 'x', 'd']) expect(press(key)).toBeNull();
  });

  it('Shift+B swaps the brush and the pencil, and any other tool lands on the brush', () => {
    expect(press('B', 'brush', 'rectangle', true)?.tool).toBe('pencil');
    expect(press('B', 'pencil', 'rectangle', true)?.tool).toBe('brush');
    expect(press('B', 'select', 'rectangle', true)?.tool).toBe('brush');
    expect(press('b', 'pencil')?.tool).toBe('brush');
  });

  it('U enters the shape tool, Shift+U cycles rectangle, ellipse, line, arrow', () => {
    expect(press('u', 'select', 'line')).toEqual({ tool: 'shape' });
    expect(press('U', 'select', 'line', true)).toEqual({ tool: 'shape' });
    const order: ShapeType[] = [];
    let shape: ShapeType = 'rectangle';
    for (let step = 0; step < 4; step += 1) {
      const result = press('U', 'shape', shape, true);
      shape = result?.shapeType ?? shape;
      order.push(shape);
    }
    expect(order).toEqual(['ellipse', 'line', 'arrow', 'rectangle']);
  });

  it('is the key the rail prints beside each tool: pressing it selects that very tool', () => {
    const tools = toolSections(defaultCatalog.application as unknown as ProductSchema, 'canvas')
      .flatMap((group) => group.tools)
      .filter((tool) => tool.shortcut?.endsWith('_tool_key'));
    // Every board tool has a key and a rail button, and nothing else on the rail pretends to be one.
    expect(tools.map((tool) => tool.id).sort()).toEqual([
      'brush', 'crop', 'eraser', 'eyedropper', 'hand', 'lasso', 'marquee', 'mask', 'pencil', 'select', 'shape', 'text', 'zoom',
    ]);
    for (const tool of tools) {
      const key = (keyboard as unknown as Record<string, string>)[tool.shortcut as string];
      // Shift+B walks brush -> pencil, so the pencil is reached from the brush.
      const result = press(key, tool.id === 'pencil' ? 'brush' : 'select', 'rectangle', tool.modifiers.includes('shift'));
      expect(result?.tool, tool.id).toBe(tool.id);
      expect(tool.modifiers.every((modifier) => modifier === 'shift'), tool.id).toBe(true);
    }
  });

  it('nextShape falls back to the first listed shape for an unknown current one', () => {
    expect(nextShape(['ellipse', 'line'], 'arrow')).toBe('ellipse');
    expect(nextShape(['ellipse', 'line'], 'line')).toBe('ellipse');
  });
});

describe('who a drag moves: the view or the layer under the pointer', () => {
  it('pans the view with the hand tool or while Space is held, on every tool', () => {
    expect(pansView('hand', false)).toBe(true);
    expect(pansView('select', true)).toBe(true);
    expect(pansView('crop', true)).toBe(true);
    expect(pansView('select', false)).toBe(false);
  });

  it('drags a layer only with the move tool, never while Space is held or when it is locked', () => {
    expect(layerDraggable('select', false, false)).toBe(true);
    expect(layerDraggable('select', false, undefined)).toBe(true);
    // Space + drag starting on a picture pans like it does on empty canvas (#263).
    expect(layerDraggable('select', true, false)).toBe(false);
    expect(layerDraggable('select', false, true)).toBe(false);
    expect(layerDraggable('hand', false, false)).toBe(false);
    expect(layerDraggable('brush', false, false)).toBe(false);
  });
});
