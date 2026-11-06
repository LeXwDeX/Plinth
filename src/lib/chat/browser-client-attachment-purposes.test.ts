import { expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from './browser-client';

it('sends per-slot purposes with Schema names and preserves their order', async () => {
  const schema = structuredClone(catalog.application) as unknown as ProductSchema;
  schema.chat.api.fields.attachment_purposes = 'configuredPurposes';
  schema.chat.api.attachment.image_slot = 'configuredSlot';
  schema.chat.api.attachment.purpose = 'configuredPurpose';
  const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
    new Response(JSON.stringify({ messageId: 'm', sessionId: 's' }), { status: 202 }));
  const client = createChatBrowserClient(schema, { fetcher });
  await client.startTurn('s', 'edit', { imageURLs: ['/same', '/same'], attachmentPurposes: [
    { imageSlot: 1, purpose: 'style' }, { imageSlot: 2, purpose: 'input' },
  ] });
  expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toMatchObject({ configuredPurposes: [
    { configuredSlot: 1, configuredPurpose: 'style' }, { configuredSlot: 2, configuredPurpose: 'input' },
  ] });
  await client.startTurn('s', 'legacy', {});
  expect(JSON.parse(String(fetcher.mock.calls[1]?.[1]?.body))).not.toHaveProperty('configuredPurposes');
});

it('shows the Schema detail of an attachment-purpose rejection', async () => {
  const schema = catalog.application as unknown as ProductSchema;
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ code: 'attachment_purpose_invalid', detail: 'Choose only one input image' }), { status: 400 }));
  const client = createChatBrowserClient(schema, { fetcher });
  await expect(client.startTurn('s', 'edit', { attachmentPurposes: [{ imageSlot: 1, purpose: 'input' }] }))
    .rejects.toMatchObject({ message: 'Choose only one input image', status: 400 });
});
