import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as unknown as ProductSchema;
}

describe('ChatBrowserClient agent scope', () => {
  it('reads the scope of each session from the list and keeps an absent one absent', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json([
        { [fields.id]: 'a', [fields.title]: 'A', [fields.agent_scope]: '2d' },
        { [fields.id]: 'b', [fields.title]: 'B' },
        { [fields.id]: 'c', [fields.title]: 'C', [fields.agent_scope]: 'bogus' },
      ]),
    });
    const sessions = await client.listSessions();
    expect(sessions.map((session) => session.agentScope)).toEqual(['2d', undefined, 'auto']);
  });

  it('reads one session scope from the Schema route', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    let requested = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requested = String(input);
        return Response.json({ [fields.id]: 'a / b', [fields.agent_scope]: '3d' });
      },
    });
    expect(await client.getAgentScope('a / b')).toBe('3d');
    expect(requested).toBe('/api/v1/chat/sessions/a%20%2F%20b/agent-scope');
  });

  it('switches the scope with a PUT that carries only the Schema field', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    let method = '';
    let body: unknown;
    let requested = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        requested = String(input);
        method = String(init?.method);
        body = JSON.parse(String(init?.body));
        return Response.json({ [fields.id]: 'a', [fields.agent_scope]: '2d' });
      },
    });
    expect(await client.setAgentScope('a', '2d')).toBe('2d');
    expect(method).toBe('PUT');
    expect(requested).toBe('/api/v1/chat/sessions/a/agent-scope');
    expect(body).toEqual({ [schema.chat.api.fields.agent_scope]: '2d' });
  });

  it('turns a refused switch into an error instead of pretending it worked', async () => {
    const schema = schemaFixture();
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ [schema.chat.api.response_fields.error]: 'no' }, { status: 400 }),
    });
    await expect(client.setAgentScope('a', '3d')).rejects.toMatchObject({ name: ChatBrowserError.name, status: 400, message: 'no' });
  });
});
