import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as unknown as ProductSchema;
}

interface Seen { url: string; method: string; body: unknown }

function recording(schema: ProductSchema, reply: () => Response) {
  const seen: Seen[] = [];
  const client = createChatBrowserClient(schema, {
    fetcher: async (input, init) => {
      seen.push({ url: String(input), method: String(init?.method ?? 'GET'), body: init?.body === undefined ? undefined : JSON.parse(String(init.body)) });
      return reply();
    },
  });
  return { client, seen };
}

describe('ChatBrowserClient thinking level (#322)', () => {
  it('declares the route and the members it reads in the Schema', () => {
    const schema = schemaFixture();
    expect(schema.runtime.http.routes.chat_session_reasoning).toBe('/api/v1/chat/sessions/{sessionID}/reasoning');
    expect(schema.chat.api.fields.reasoning_effort).toBeTruthy();
    expect(schema.chat.api.response_fields.reasoning_levels).toBeTruthy();
  });

  it('reads the level, the offered levels and the default through the Schema member names', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const { client, seen } = recording(schema, () => Response.json({
      [fields.id]: 'a / b',
      [fields.reasoning_effort!]: 'high',
      [fields.reasoning_levels!]: ['low', 'high', 'max', 7],
      [fields.reasoning_default!]: 'high',
    }));
    expect(await client.getReasoning('a / b')).toEqual({ effort: 'high', levels: ['low', 'high', 'max'], defaultLevel: 'high' });
    expect(seen).toEqual([{ url: '/api/v1/chat/sessions/a%20%2F%20b/reasoning', method: 'GET', body: undefined }]);
  });

  it('reads a model without levels as an empty list', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const { client } = recording(schema, () => Response.json({ [fields.id]: 'a', [fields.reasoning_effort!]: '', [fields.reasoning_levels!]: [] }));
    expect(await client.getReasoning('a')).toEqual({ effort: '', levels: [], defaultLevel: '' });
  });

  it('switches the level with a PUT that carries only the Schema request member', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const { client, seen } = recording(schema, () => Response.json({
      [fields.id]: 'a', [fields.reasoning_effort!]: 'max', [fields.reasoning_levels!]: ['low', 'high', 'max'], [fields.reasoning_default!]: 'high',
    }));
    expect((await client.setReasoning('a', 'max')).effort).toBe('max');
    expect(seen).toEqual([{ url: '/api/v1/chat/sessions/a/reasoning', method: 'PUT', body: { [schema.chat.api.fields.reasoning_effort!]: 'max' } }]);
  });

  it('turns a refused switch into an error with the server text and status', async () => {
    const schema = schemaFixture();
    const { client } = recording(schema, () => Response.json({ [schema.chat.api.response_fields.error]: 'no levels' }, { status: 409 }));
    await expect(client.setReasoning('a', 'high')).rejects.toMatchObject({ name: ChatBrowserError.name, status: 409, message: 'no levels' });
  });
});

describe('ChatBrowserClient context usage and compaction (#322)', () => {
  it('reads the token count, the window and the compactions through the Schema member names', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const { client, seen } = recording(schema, () => Response.json({
      [fields.id]: 's',
      [fields.context_tokens!]: 356_000,
      [fields.context_vision_tokens!]: 1_105,
      [fields.context_vision_token_budget!]: 12_000,
      [fields.context_vision_budget!]: 10,
      [fields.context_window!]: 1_000_000,
      [fields.compactions!]: [
        { [fields.id]: 'c1', [fields.created_at]: '2026-10-22T01:00:00Z', [fields.compaction_trigger!]: 'manual', [fields.compacted_rounds!]: 6, [fields.through_message_id!]: 'm12' },
        { [fields.id]: 'c2', [fields.created_at]: '2026-10-22T02:00:00Z', [fields.compaction_trigger!]: 'auto', [fields.compacted_rounds!]: 3, [fields.through_message_id!]: 'm20' },
      ],
    }));
    expect(await client.getContext('s')).toEqual({
      tokens: 356_000,
      visionTokens: 1_105,
      visionTokenBudget: 12_000,
      visionBudget: 10,
      window: 1_000_000,
      compactions: [
        { id: 'c1', createdAt: '2026-10-22T01:00:00Z', trigger: 'manual', rounds: 6, throughMessageId: 'm12' },
        { id: 'c2', createdAt: '2026-10-22T02:00:00Z', trigger: 'auto', rounds: 3, throughMessageId: 'm20' },
      ],
    });
    expect(seen).toEqual([{ url: '/api/v1/chat/sessions/s/context', method: 'GET', body: undefined }]);
  });

  it('keeps an unreported count and an undeclared window unknown instead of zero', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const { client } = recording(schema, () => Response.json({ [fields.id]: 's', [fields.context_tokens!]: null, [fields.context_vision_tokens!]: null, [fields.context_vision_token_budget!]: null, [fields.context_vision_budget!]: null, [fields.context_window!]: null, [fields.compactions!]: [] }));
    expect(await client.getContext('s')).toEqual({ tokens: null, visionTokens: null, visionTokenBudget: null, visionBudget: null, window: null, compactions: [] });
    const missing = recording(schema, () => Response.json({ [fields.id]: 's' }));
    expect(await missing.client.getContext('s')).toEqual({ tokens: null, visionTokens: null, visionTokenBudget: null, visionBudget: null, window: null, compactions: [] });
  });

  it('compacts with an empty POST and returns the context after the compaction', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const { client, seen } = recording(schema, () => Response.json({
      [fields.id]: 's', [fields.context_tokens!]: null, [fields.context_window!]: 1_000_000,
      [fields.compactions!]: [{ [fields.id]: 'c1', [fields.created_at]: '2026-10-22T01:00:00Z', [fields.compaction_trigger!]: 'manual', [fields.compacted_rounds!]: 6, [fields.through_message_id!]: 'm12' }],
    }));
    const context = await client.compactContext('s');
    expect(context.tokens).toBeNull();
    expect(context.compactions).toHaveLength(1);
    expect(seen).toEqual([{ url: '/api/v1/chat/sessions/s/context/compaction', method: 'POST', body: undefined }]);
  });

  it.each([409, 422, 502])('carries the server text and the status %d of a refused compaction', async (status) => {
    const schema = schemaFixture();
    const { client } = recording(schema, () => Response.json({ [schema.chat.api.response_fields.error]: `refused ${status}` }, { status }));
    await expect(client.compactContext('s')).rejects.toMatchObject({ name: ChatBrowserError.name, status, message: `refused ${status}` });
  });

  it('leaves the message empty when the server gave no text, so the caller uses its own', async () => {
    const schema = schemaFixture();
    const { client } = recording(schema, () => new Response('bad gateway', { status: 502 }));
    await expect(client.compactContext('s')).rejects.toMatchObject({ name: ChatBrowserError.name, status: 502, message: '' });
  });
});

// #314: a rate-limited read tells the page when to come back; the retry of the session's chat model and thinking
// level waits for it instead of spending its few retries inside the limit's window.
describe('ChatBrowserClient reads refused with a rate limit (#314)', () => {
  const reads = ['getReasoning', 'getChatModel'] as const;

  it.each(reads)('%s carries the Retry-After seconds of a 429', async (read) => {
    const schema = schemaFixture();
    const { client } = recording(schema, () => Response.json({ error: 'slow down' }, { status: 429, headers: { 'Retry-After': '60' } }));
    const error = await client[read]('s').catch((cause: unknown) => cause);
    expect(error).toBeInstanceOf(ChatBrowserError);
    expect(error).toMatchObject({ status: 429, retryAfterSeconds: 60 });
  });

  it.each(reads)('%s leaves the wait off when the header is missing or not a number of seconds', async (read) => {
    const schema = schemaFixture();
    const unusable: Array<Record<string, string>> = [{}, { 'Retry-After': 'soon' }, { 'Retry-After': '0' }];
    for (const headers of unusable) {
      const { client } = recording(schema, () => Response.json({}, { status: 429, headers }));
      const error = await client[read]('s').catch((cause: unknown) => cause) as ChatBrowserError;
      expect(error.status).toBe(429);
      expect(error.retryAfterSeconds).toBeUndefined();
    }
  });

  it.each(reads)('%s ignores Retry-After on any other status', async (read) => {
    const schema = schemaFixture();
    const { client } = recording(schema, () => Response.json({}, { status: 503, headers: { 'Retry-After': '30' } }));
    const error = await client[read]('s').catch((cause: unknown) => cause) as ChatBrowserError;
    expect(error.status).toBe(503);
    expect(error.retryAfterSeconds).toBeUndefined();
  });
});
