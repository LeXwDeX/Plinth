import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';
import { replaceFailureMessage } from '@/lib/chat/turn-replace-error';

// #403: replacing the last turn (regenerate, edit and resend) deletes its messages first. While the
// turn still runs the server refuses with 409 and says why; that reason is what the person reads.

const FALLBACK = 'Failed to replace the previous turn, please retry';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

describe('the text of a failed replace', () => {
  it('shows the reason the server gave for refusing with 409', () => {
    const refused = new ChatBrowserError('This session still has a task in progress', 409);
    expect(replaceFailureMessage(refused, FALLBACK)).toBe('This session still has a task in progress');
  });

  it('keeps the general text for a 409 that gave no reason', () => {
    expect(replaceFailureMessage(new ChatBrowserError('', 409), FALLBACK)).toBe(FALLBACK);
  });

  it('keeps the general text for every other failure', () => {
    expect(replaceFailureMessage(new ChatBrowserError('Internal error', 500), FALLBACK)).toBe(FALLBACK);
    expect(replaceFailureMessage(new ChatBrowserError('Not found', 404), FALLBACK)).toBe(FALLBACK);
    expect(replaceFailureMessage(new Error('network down'), FALLBACK)).toBe(FALLBACK);
    expect(replaceFailureMessage('409', FALLBACK)).toBe(FALLBACK);
    expect(replaceFailureMessage(undefined, FALLBACK)).toBe(FALLBACK);
  });

  it('reads the reason from the delete the server refused', async () => {
    const schema = schemaFixture();
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ [schema.chat.api.response_fields.error]: schema.chat.presentation.messages.turn_active }, { status: 409 }),
    });
    const refused = await client.deleteMessage('session', 'assistant').catch((error: unknown) => error);
    expect(refused).toBeInstanceOf(ChatBrowserError);
    expect(replaceFailureMessage(refused, FALLBACK)).toBe(schema.chat.presentation.messages.turn_active);
    expect(schema.chat.presentation.messages.turn_active).not.toBe('');
  });
});
