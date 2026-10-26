import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient, type ChatSemanticEvent } from '@/lib/chat/browser-client';
import { nextStreamStatus } from '@/lib/chat/stream-status';
import type { GenState } from '@/store/chat-store';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

/** Plays events through the status slot and returns the text it showed after each. */
function play(schema: ProductSchema, events: ChatSemanticEvent[]): Array<string> {
  let state: GenState = { statusText: '', progress: null };
  return events.map((event) => {
    state = nextStreamStatus(schema, state, event);
    return state.statusText + (state.thinking ? ' [thinking]' : '');
  });
}

describe('streaming reply status (#285)', () => {
  it('shows 思考中… until the answer starts, then only the answer', () => {
    const schema = schemaFixture();
    const thinking = schema.chat.presentation.status.thinking ?? '';
    expect(thinking).toBe('思考中…');
    expect(play(schema, [
      { type: 'messageCreated', messageID: 'm' },
      { type: 'thinking', content: thinking },
      { type: 'text', content: '\n' },
      { type: 'text', content: '好的' },
      { type: 'text', content: '，马上画。' },
    ])).toEqual(['', '思考中… [thinking]', '思考中… [thinking]', '', '']);
  });

  it('shows 思考中… again after a tool round, in the same slot as the tool status', () => {
    const schema = schemaFixture();
    const thinking = schema.chat.presentation.status.thinking ?? '';
    const generating = schema.chat.presentation.status.generating;
    expect(play(schema, [
      { type: 'thinking', content: thinking },
      { type: 'text', content: '我来生成' },
      { type: 'status', content: generating },
      { type: 'progress', percent: 42 },
      { type: 'images', taskID: 't', urls: ['/a.png'] },
      { type: 'thinking', content: thinking },
      { type: 'text', content: '完成了' },
    ])).toEqual([
      '思考中… [thinking]', '', generating, generating, '',
      '思考中… [thinking]', '',
    ]);
  });

  it('keeps a tool status while text streams and replaces it with a later thinking status', () => {
    const schema = schemaFixture();
    const generating = schema.chat.presentation.status.generating;
    let state: GenState = { statusText: '', progress: null };
    state = nextStreamStatus(schema, state, { type: 'status', content: generating });
    const kept = nextStreamStatus(schema, state, { type: 'text', content: 'still running' });
    expect(kept).toBe(state);
    state = nextStreamStatus(schema, kept, { type: 'thinking', content: '思考中…' });
    expect(state).toEqual({ statusText: '思考中…', progress: null, thinking: true });
  });

  it('decodes the Schema thinking event without any reasoning text', () => {
    const schema = schemaFixture();
    schema.chat.stream.event_types.thinking = 'configured-thinking';
    const source = {
      onmessage: null as ((event: MessageEvent<string>) => void) | null,
      onerror: null as (() => void) | null,
      close: () => {},
    };
    const client = createChatBrowserClient(schema, {
      eventSourceFactory: () => source,
    });
    const events: ChatSemanticEvent[] = [];
    client.subscribe('session', { onEvent: (event) => events.push(event) });
    const emit = (data: unknown) => source.onmessage?.({ data: JSON.stringify(data) } as MessageEvent<string>);
    emit({ [schema.chat.stream.event_fields.type]: 'configured-thinking', [schema.chat.stream.event_fields.content]: '思考中…' });
    emit({ [schema.chat.stream.event_fields.type]: 'configured-thinking' });
    expect(events).toEqual([
      { type: 'thinking', content: '思考中…' },
      { type: 'thinking', content: '思考中…' },
    ]);
  });
});
