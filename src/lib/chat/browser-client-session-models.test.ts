import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from '@/lib/chat/browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as unknown as ProductSchema;
}

describe('ChatBrowserClient session models (#318)', () => {
  it('reads the per-domain models of a session and keeps only plain strings', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    expect(fields.generation_models).toBeTruthy();
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({
        [fields.id]: 's1',
        [fields.title]: 'S',
        [fields.generation_models!]: { image: 'img-a', 'model-3d': 7, other: '' },
      }),
    });
    expect((await client.getSession('s1')).generationModels).toEqual({ image: 'img-a' });
  });

  it('leaves the member absent when the server sends none', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ [fields.id]: 's1', [fields.title]: 'S' }),
    });
    expect((await client.getSession('s1')).generationModels).toBeUndefined();
  });

  // #329 follow-up C: the server reports a retired model as its successor and names the retired one, so the
  // workspace can say it switched.
  it('reads which domains the server moved off a retired model', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    expect(fields.generation_models_retired).toBeTruthy();
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({
        [fields.id]: 's1',
        [fields.title]: 'S',
        [fields.generation_models!]: { 'model-3d': 'meshy-7.1' },
        [fields.generation_models_retired!]: { 'model-3d': 'meshy-7' },
      }),
    });
    const session = await client.getSession('s1');
    expect(session.generationModels).toEqual({ 'model-3d': 'meshy-7.1' });
    expect(session.generationModelsRetired).toEqual({ 'model-3d': 'meshy-7' });
  });
});
