import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { referenceLimit, referenceLimitNote } from './reference-limit';

const offering = (id: string) => {
  const found = defaultCatalog.offerings.find((item) => item.id === id);
  if (!found) throw new Error(`missing ${id}`);
  return found as unknown as OfferingWire;
};

// #329 follow-up A / #80: the composer says, before sending, that a 3D model takes fewer pictures than are
// attached. The limit is the Offering's reference-image max_items; nothing is cut or blocked here.
describe('referenceLimit', () => {
  it('names the model and its limit when more pictures are attached than Meshy 7.1 image to 3D takes', () => {
    expect(referenceLimit(offering('meshy/meshy-7.1/image-to-3d'), 2)).toEqual({ model: 'Meshy 7.1', count: 1, total: 2 });
    expect(referenceLimit(offering('meshy/meshy-t2.5/image-to-3d'), 3)).toMatchObject({ count: 1, total: 3 });
  });

  it('is silent within the limit, for a multi-picture model, without pictures or without an Offering', () => {
    expect(referenceLimit(offering('meshy/meshy-7.1/image-to-3d'), 1)).toBeNull();
    expect(referenceLimit(offering('meshy/meshy-7.1-multiview/image-to-3d'), 4)).toBeNull();
    expect(referenceLimit(offering('meshy/meshy-7.1/text-to-3d'), 2)).toBeNull();
    expect(referenceLimit(undefined, 2)).toBeNull();
  });

  it('is silent for an Offering that keeps its picture sequence (the step refuses instead of cutting)', () => {
    const preserve = { ...offering('meshy/meshy-7.1/image-to-3d'), reference_selection_policy: 'preserve' as const };
    expect(referenceLimit(preserve, 2)).toBeNull();
  });
});

// #80: before sending, the composer states the model's capacity and the number
// of attached pictures. It does not predict which picture is used: a three-view
// view may be replaced by its group's front view before the limit applies.
describe('referenceLimitNote', () => {
  const messages = defaultCatalog.application.runtime.generation_messages as Record<string, string>;
  const english = (defaultCatalog.application.localization.bundles as Record<string, { fields: Record<string, string> }>).en.fields;
  const schemaWith = (generation: Record<string, string>) => ({ runtime: { generation_messages: generation } }) as unknown as ProductSchema;
  const fill = (template: string, values: Record<string, string | number>) => template.replace(/\{(\w+)\}/g, (_, name) => String(values[name]));

  it('states the capacity without claiming a picture order', () => {
    const schema = schemaWith(messages);
    const one = referenceLimitNote(schema, { model: 'Meshy 7.1', count: 1, total: 2 });
    const several = referenceLimitNote(schema, { model: 'Model X', count: 2, total: 5 });
    expect(one).toBe(fill(messages.reference_capacity, { model: 'Meshy 7.1', count: 1, total: 2 }));
    expect(several).toBe(fill(messages.reference_capacity, { model: 'Model X', count: 2, total: 5 }));
    expect(one).toContain('一次最多用 1 张参考图；这次共 2 张');
    expect(several).toContain('一次最多用 2 张参考图；这次共 5 张');
    for (const text of [one, several]) {
      expect(text).not.toContain('第 1 张');
      expect(text).not.toContain('只用前');
      expect(text).toContain('一次最多用');
    }
  });

  it('reads right in English for one or several pictures without claiming the first picture', () => {
    const schema = schemaWith({ reference_capacity: english['runtime.generation_messages.reference_capacity'] });
    const one = referenceLimitNote(schema, { model: 'Meshy 7.1', count: 1, total: 2 });
    const several = referenceLimitNote(schema, { model: 'Model X', count: 2, total: 5 });
    expect(one).toContain('reference picture limit of 1 per request; 2 pictures are attached');
    expect(several).toContain('reference picture limit of 2 per request; 5 pictures are attached');
    for (const text of [one, several]) {
      expect(text).not.toContain('first is used');
      expect(text).not.toContain('(s)');
    }
  });

  it('is empty when the Schema declares no capacity text', () => {
    expect(referenceLimitNote(schemaWith({}), { model: 'Meshy 7.1', count: 1, total: 2 })).toBe('');
    expect(referenceLimitNote({ runtime: {} } as ProductSchema, { model: 'Meshy 7.1', count: 1, total: 2 })).toBe('');
  });
});
