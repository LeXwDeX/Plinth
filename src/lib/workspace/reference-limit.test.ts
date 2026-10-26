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

// #329 follow-up A: the composer says, before sending, that a single-picture 3D model uses only the first
// pictures. The limit is the Offering's reference-image max_items; nothing is cut or blocked here.
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

// #329 dev-test note 99647 (observations 8, 9): the composer, the task API, the approval card and the finished
// message say it with one Schema pair (runtime.generation_messages), worded for one picture when the model takes one.
describe('referenceLimitNote', () => {
  const messages = defaultCatalog.application.runtime.generation_messages as Record<string, string>;
  const english = (defaultCatalog.application.localization.bundles as Record<string, { fields: Record<string, string> }>).en.fields;
  const schemaWith = (generation: Record<string, string>) => ({ runtime: { generation_messages: generation } }) as unknown as ProductSchema;
  const fill = (template: string, values: Record<string, string | number>) => template.replace(/\{(\w+)\}/g, (_, name) => String(values[name]));

  it('uses the one-picture text for a model that takes one, and the general text otherwise', () => {
    const schema = schemaWith(messages);
    expect(referenceLimitNote(schema, { model: 'Meshy 7.1', count: 1, total: 2 }))
      .toBe(fill(messages.reference_limit_one, { model: 'Meshy 7.1', total: 2 }));
    expect(referenceLimitNote(schema, { model: 'Meshy 7.1', count: 1, total: 2 })).toContain('第 1 张');
    expect(referenceLimitNote(schema, { model: 'Model X', count: 2, total: 5 }))
      .toBe(fill(messages.reference_limit, { model: 'Model X', count: 2, total: 5 }));
  });

  it('reads right in English for one picture and for several', () => {
    const schema = schemaWith({
      reference_limit: english['runtime.generation_messages.reference_limit'],
      reference_limit_one: english['runtime.generation_messages.reference_limit_one'],
    });
    const one = referenceLimitNote(schema, { model: 'Meshy 7.1', count: 1, total: 2 });
    const several = referenceLimitNote(schema, { model: 'Model X', count: 2, total: 5 });
    for (const text of [one, several]) {
      expect(text).not.toContain('(s)');
      expect(text).not.toMatch(/first 1 /);
    }
    expect(one).toContain('only the first is used');
    expect(several).toContain('only the first 2 are used');
  });

  it('is empty when the Schema declares no text', () => {
    expect(referenceLimitNote(schemaWith({}), { model: 'Meshy 7.1', count: 1, total: 2 })).toBe('');
  });
});
