import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

describe('ChatBrowserClient approval mode', () => {
  it('reads the mode and cumulative credits from a session payload', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json([
        { [fields.id]: 'a', [fields.title]: 'A', [fields.approval_mode]: 'auto', [fields.estimated_credits]: 12 },
        { [fields.id]: 'b', [fields.title]: 'B' },
        { [fields.id]: 'c', [fields.title]: 'C', [fields.approval_mode]: 'bogus', [fields.estimated_credits]: 'x' },
      ]),
    });
    const sessions = await client.listSessions();
    expect(sessions.map((session) => [session.approvalMode, session.estimatedCredits])).toEqual([
      ['auto', 12],
      ['manual', 0],
      ['manual', 0],
    ]);
  });

  it('treats auto as manual when the Schema does not offer it', async () => {
    const schema = schemaFixture();
    schema.chat.approval_policy.modes = ['manual'];
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ [fields.id]: 'a', [fields.approval_mode]: 'auto' }),
    });
    expect((await client.getApproval('a')).approvalMode).toBe('manual');
  });

  it('reads the approval of one session from the Schema route', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    let requested = '';
    const client = createChatBrowserClient(schema, {
      fetcher: async (input) => {
        requested = String(input);
        return Response.json({ [fields.id]: 'a / b', [fields.approval_mode]: 'auto', [fields.estimated_credits]: 9 });
      },
    });
    expect(await client.getApproval('a / b')).toEqual({ approvalMode: 'auto', estimatedCredits: 9 });
    expect(requested).toBe('/api/v1/chat/sessions/a%20%2F%20b/approval');
  });

  it('switches the mode with a PUT that carries only the Schema field', async () => {
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
        return Response.json({ [fields.id]: 'a', [fields.approval_mode]: 'auto', [fields.estimated_credits]: 4 });
      },
    });
    expect(await client.setApprovalMode('a', 'auto')).toEqual({ approvalMode: 'auto', estimatedCredits: 4 });
    expect(method).toBe('PUT');
    expect(requested).toBe('/api/v1/chat/sessions/a/approval');
    expect(body).toEqual({ [schema.chat.api.fields.approval_mode]: 'auto' });
  });

  it('turns a refused switch into an error instead of pretending it worked', async () => {
    const schema = schemaFixture();
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ [schema.chat.api.response_fields.error]: 'no' }, { status: 403 }),
    });
    await expect(client.setApprovalMode('a', 'auto')).rejects.toMatchObject({
      name: ChatBrowserError.name, status: 403, message: 'no',
    });
  });

  it('projects tool-call credits and approval source into paid steps', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ messages: [{
        [fields.id]: 'message-a',
        [fields.role]: schema.chat.roles.assistant,
        [fields.content]: 'done',
        [fields.created_at]: '2026-10-08T00:00:00Z',
        [fields.tool_calls]: JSON.stringify([
          { [fields.tool_name]: 'refine_image', [fields.task_id]: 'task-a', [fields.estimated_credits]: 3, [fields.approval_source]: 'policy' },
          { [fields.tool_name]: 'create_model_3d', [fields.task_id]: 'task-b', [fields.estimated_credits]: 40, [fields.approval_source]: 'human' },
          { [fields.tool_name]: 'refine_image', [fields.task_id]: 'task-c' },
          { [fields.tool_name]: 'create_model_3d', [fields.task_id]: 'task-d', price_mode: 'native' },
          { [fields.tool_name]: 'create_model_3d', [fields.task_id]: 'task-e', native_quote: null },
        ]),
      }], next_cursor: '' }),
    });
    const { messages: [message] } = await client.listMessagePage('a', { limit: 50 });
    expect(message.paidSteps).toEqual([
      { taskId: 'task-a', credits: 3, approvedByPolicy: true },
      { taskId: 'task-b', credits: 40, approvedByPolicy: false },
      { taskId: 'task-d', credits: 0, approvedByPolicy: false, nativeQuote: null },
      { taskId: 'task-e', credits: 0, approvedByPolicy: false, nativeQuote: null },
    ]);
    expect(message.taskIds).toEqual(['task-a', 'task-b', 'task-c', 'task-d', 'task-e']);
  });

  it('carries credits and approval source on the task registered event', () => {
    const schema = schemaFixture();
    const fields = schema.chat.stream.event_fields;
    const types = schema.chat.stream.event_types;
    const listeners: Array<(event: MessageEvent<string>) => void> = [];
    const source = {
      set onmessage(handler: ((event: MessageEvent<string>) => void) | null) { if (handler) listeners.push(handler); },
      get onmessage() { return null; },
      onerror: null,
      close() {},
    };
    const client = createChatBrowserClient(schema, { eventSourceFactory: () => source });
    const events: unknown[] = [];
    client.subscribe('a', { onEvent: (event) => events.push(event) });
    const emit = (payload: unknown) => listeners[0]({ data: JSON.stringify(payload) } as MessageEvent<string>);
    emit({ [fields.type]: types.task_registered, [fields.task_id]: 't1', [fields.capability]: 'text-to-image',
      [fields.estimated_credits]: 3, [fields.approval_source]: 'policy' });
    emit({ [fields.type]: types.task_registered, [fields.task_id]: 't2', [fields.capability]: 'text-to-3d' });
    expect(events).toMatchObject([
      { type: 'taskRegistered', taskID: 't1', estimatedCredits: 3, approvedByPolicy: true },
      { type: 'taskRegistered', taskID: 't2', approvedByPolicy: false },
    ]);
    expect((events[1] as { estimatedCredits?: number }).estimatedCredits).toBeUndefined();
  });
});
