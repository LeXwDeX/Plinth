import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

// #137: "stop waiting, keep the task running" uses the Schema's route (shaped like the stop route) and
// member names; the server decides, and a refusal carries the server's Schema reason.
describe('ChatBrowserClient background continue', () => {
  it('reads the availability and continues through the Schema route and members', async () => {
    const schema = schemaFixture();
    schema.runtime.http.legacy_routes.chat_turn_background = '/configured/{sessionID}/keep-running';
    schema.chat.api.fields.turn_background_available = 'configuredAvailable';
    schema.chat.api.fields.turn_backgrounded = 'configuredBackgrounded';
    const calls: Array<{ url: string; method: string }> = [];
    const client = createChatBrowserClient(schema, {
      fetcher: async (input, init) => {
        const method = init?.method ?? 'GET';
        calls.push({ url: String(input), method });
        return Response.json(method === 'GET' ? { configuredAvailable: true } : { configuredBackgrounded: true });
      },
    });
    expect(await client.turnBackgroundAvailable('s 1')).toBe(true);
    await client.continueTurnInBackground('s 1');
    expect(calls).toEqual([
      { url: '/configured/s%201/keep-running', method: 'GET' },
      { url: '/configured/s%201/keep-running', method: 'POST' },
    ]);
  });

  it('reads false without the route, on a failed read or on a missing member', async () => {
    const schema = schemaFixture();
    const failing = createChatBrowserClient(schema, { fetcher: async () => { throw new Error('offline'); } });
    expect(await failing.turnBackgroundAvailable('s1')).toBe(false);
    const empty = createChatBrowserClient(schema, { fetcher: async () => Response.json({}) });
    expect(await empty.turnBackgroundAvailable('s1')).toBe(false);
    delete schema.runtime.http.legacy_routes.chat_turn_background;
    let called = false;
    const undeclared = createChatBrowserClient(schema, { fetcher: async () => { called = true; return Response.json({}); } });
    expect(await undeclared.turnBackgroundAvailable('s1')).toBe(false);
    expect(called).toBe(false);
  });

  it('throws the server reason when the turn does not qualify', async () => {
    const schema = schemaFixture();
    const reason = defaultCatalog.application.chat.agent.background_wait.unavailable_message;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ [schema.chat.api.response_fields.error]: reason, request_id: 'req-1' }, { status: 409 }),
    });
    const error = await client.continueTurnInBackground('s1').catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ChatBrowserError);
    expect((error as ChatBrowserError).message).toBe(reason);
    expect((error as ChatBrowserError).status).toBe(409);
  });
});
