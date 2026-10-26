import { expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { workspaceOfferingsKey } from '@/hooks/use-workspace-offerings';
import type { ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;

it('keys the Offering cache by schema revision, route, domain, and capability IDs', () => {
  const key = workspaceOfferingsKey(schema, 'image');
  expect(key).not.toBeNull();
  expect(key?.slice(0, 4)).toEqual([
    'workspace-offerings',
    schema.revision,
    schema.runtime.http.routes.offerings,
    'image',
  ]);
  expect(key?.[4]).toEqual(expect.arrayContaining(['text-to-image', 'image-to-image']));

  const nextRevision = { ...schema, revision: `${schema.revision}-next` };
  expect(workspaceOfferingsKey(nextRevision, 'image')).not.toEqual(key);
  expect(workspaceOfferingsKey(schema, 'model-3d')).not.toEqual(key);
});
