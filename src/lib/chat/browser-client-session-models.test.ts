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

describe('ChatBrowserClient session not found (#665)', () => {
  const failure = async (response: Response) => {
    const schema = schemaFixture();
    const client = createChatBrowserClient(schema, { fetcher: async () => response });
    return client.getSession('s1').then(() => null, (error: unknown) => error as { status: number; serverAnswered: boolean });
  };
  const errorField = () => schemaFixture().chat.api.response_fields.error;

  it('counts only the API\'s own chat error as the server saying the session is gone', async () => {
    const id = 'req_0123456789abcdef';
    // The API: its error field and the request ID its header names.
    const api = await failure(Response.json({ [errorField()]: '会话不存在', request_id: id }, { status: 404, headers: { 'X-Request-ID': id } }));
    expect(api).toMatchObject({ status: 404, serverAnswered: true });
    // A gateway's JSON 404 with an `error` string, one with an ID the header does not name, and a plain-text page.
    const gateway = await failure(Response.json({ [errorField()]: 'Not Found' }, { status: 404 }));
    expect(gateway?.serverAnswered).toBe(false);
    const foreignId = await failure(Response.json({ [errorField()]: 'Not Found', request_id: id }, { status: 404, headers: { 'X-Request-ID': 'req_ffffffffffffffff' } }));
    expect(foreignId?.serverAnswered).toBe(false);
    const page = await failure(new Response('404 page not found', { status: 404 }));
    expect(page?.serverAnswered).toBe(false);
  });

  it('finds the API\'s request ID among the X-Request-ID values a proxy joined to it', async () => {
    const id = 'req_0123456789abcdef';
    const joined = (values: string[]) => {
      const headers = new Headers();
      for (const value of values) headers.append('X-Request-ID', value);
      return headers;
    };
    // `Headers.get` gives the proxy's and the API's values as one comma-joined string, in either order.
    for (const values of [['proxy-abc', id], [id, 'proxy-abc']]) {
      const answer = await failure(Response.json({ [errorField()]: '会话不存在', request_id: id }, { status: 404, headers: joined(values) }));
      expect(answer).toMatchObject({ status: 404, serverAnswered: true });
    }
    // None of the joined values is the body's ID: not the API's answer.
    const other = await failure(Response.json({ [errorField()]: 'Not Found', request_id: id }, { status: 404, headers: joined(['proxy-abc', 'req_ffffffffffffffff']) }));
    expect(other?.serverAnswered).toBe(false);
    // A prefix of a joined value is not a match.
    const prefix = await failure(Response.json({ [errorField()]: 'Not Found', request_id: id }, { status: 404, headers: joined([`${id}x`]) }));
    expect(prefix?.serverAnswered).toBe(false);
  });
});
