import { expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from './browser-client';

// #697: the composer no longer asks what a picture is for; the deprecated option is accepted but never sent.
it('never sends attachment purposes, even when an older caller still passes them', async () => {
  const schema = catalog.application as unknown as ProductSchema;
  const field = schema.chat.api.fields.attachment_purposes;
  const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) =>
    new Response(JSON.stringify({ messageId: 'm', sessionId: 's' }), { status: 202 }));
  const client = createChatBrowserClient(schema, { fetcher });
  // Two pictures both marked as the input used to be refused before sending; now they simply go out as pictures.
  await client.startTurn('s', 'edit', { imageURLs: ['/a', '/b'], attachmentPurposes: [
    { imageSlot: 1, purpose: 'input' }, { imageSlot: 2, purpose: 'input' },
  ] });
  const body = JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body));
  expect(field).not.toBe('');
  expect(body).not.toHaveProperty(field);
  expect(body[schema.chat.api.fields.image_urls]).toEqual(['/a', '/b']);
});

it('shows the Schema detail of an attachment-purpose rejection', async () => {
  const schema = catalog.application as unknown as ProductSchema;
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ code: 'attachment_purpose_invalid', detail: 'Choose only one input image' }), { status: 400 }));
  const client = createChatBrowserClient(schema, { fetcher });
  await expect(client.startTurn('s', 'edit', { attachmentPurposes: [{ imageSlot: 1, purpose: 'input' }] }))
    .rejects.toMatchObject({ message: 'Choose only one input image', status: 400 });
});
