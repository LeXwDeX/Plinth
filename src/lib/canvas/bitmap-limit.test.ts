import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { bitmapCapacity, bitmapLimitMessage, CanvasBitmapLimitError } from './bitmap-limit';

const application = defaultCatalog.application as unknown as ProductSchema;
const withLimits = (maximum: number, warning: number): ProductSchema => ({
  ...application,
  canvas: { ...application.canvas, limits: { ...application.canvas.limits, maximum_bitmap_nodes: maximum, bitmap_warning_remaining: warning } },
});
const pictures = (count: number) => Array.from({ length: count }, () => ({ type: 'image' }));

describe('the canvas picture limit in the browser (#149)', () => {
  it('reads the limit and the warning margin from the Schema, never a literal', () => {
    expect(application.canvas.limits.maximum_bitmap_nodes).toBeGreaterThan(application.canvas.limits.bitmap_warning_remaining);
    const small = withLimits(10, 3);
    expect(bitmapCapacity(small, pictures(6))).toEqual({ used: 6, limit: 10, remaining: 4, full: false, near: false });
    expect(bitmapCapacity(small, pictures(7))).toMatchObject({ remaining: 3, full: false, near: true });
    expect(bitmapCapacity(small, pictures(10))).toMatchObject({ remaining: 0, full: true, near: false });
    expect(bitmapCapacity(small, pictures(12))).toMatchObject({ remaining: 0, full: true });
    expect(bitmapCapacity(withLimits(200, 20), pictures(180))).toMatchObject({ limit: 200, near: true });
  });

  it('counts only picture layers, and an unknown canvas as empty', () => {
    const mixed = [...pictures(2), { type: 'text' }, { type: 'shape' }, { type: 'pencil' }, { type: 'frame' }];
    expect(bitmapCapacity(withLimits(3, 1), mixed)).toMatchObject({ used: 2, remaining: 1, near: true });
    expect(bitmapCapacity(withLimits(3, 1), undefined)).toMatchObject({ used: 0, full: false });
  });

  it('names the limit and what to do, in Chinese and English, without a hard-coded 50', () => {
    const capacity = bitmapCapacity(withLimits(12, 3), pictures(12));
    const reached = bitmapLimitMessage(withLimits(12, 3), 'reached', capacity);
    expect(reached).toContain('12');
    expect(reached).toContain('删除');
    expect(reached).toContain('新建');
    expect(reached).not.toMatch(/\{|50/);
    const near = bitmapLimitMessage(withLimits(12, 3), 'near', bitmapCapacity(withLimits(12, 3), pictures(10)));
    expect(near).toContain('10');
    expect(near).toContain('12');
    expect(near).toContain('2');
    const raw = JSON.stringify(defaultCatalog);
    for (const key of ['ui.workspace.canvas.bitmap_limit_reached', 'ui.workspace.canvas.bitmap_limit_near']) {
      expect(raw.split(`"${key}"`).length - 1, key).toBe(2);
    }
    const english = [...raw.matchAll(/"ui\.workspace\.canvas\.bitmap_limit_(?:reached|near)":"([^"]+)"/g)].map((match) => match[1]);
    expect(english.every((text) => !/50/.test(text))).toBe(true);
    expect(english.filter((text) => /[Dd]elet(?:e|ing) images/.test(text))).toHaveLength(2);
  });

  it('is an error the capture entry can tell from a failed screenshot', () => {
    const error = new CanvasBitmapLimitError(50, 50);
    expect(error).toBeInstanceOf(Error);
    expect(error).toMatchObject({ name: 'CanvasBitmapLimitError', used: 50, limit: 50 });
  });
});
