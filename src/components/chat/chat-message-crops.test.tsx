import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { ChatAttachment, ChatMessage as ChatMessageType } from '@/store/chat-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));

// #552: the crops crop_image_regions cuts in a conversation reach the reply as
// input-asset pictures with crop attachments. They use the ordinary result card:
// every crop is a thumbnail with its handle tag, and a click opens it large in
// the result detail, which offers the download of a picture without a task.
it('shows conversation crops as reply pictures with their handles', () => {
  const crops = [1, 2, 3, 4].map((n) => ({ url: `/api/v1/assets/crop_${n}`, taskId: '' }));
  const attachments: ChatAttachment[] = crops.map((_, index) => ({
    handle: `@img${index + 2}`, kind: 'image', role: 'crop', taskId: '', assetId: `crop_${index + 1}`, imageSlot: index + 1,
  }));
  const message: ChatMessageType = {
    id: 'm1', role: 'assistant', content: '四张裁图是 @img2 @img3 @img4 @img5。', images: crops, attachments, createdAt: 1,
  };
  const html = renderToStaticMarkup(<ChatMessage message={message} sessionId="s" />);

  expect(html).toContain('data-testid="chat-image-result"');
  expect(html).toContain('data-count="4"');
  expect(html.match(/<button[^>]*data-testid="chat-result-picture"/g)).toHaveLength(4);
  for (const crop of crops) expect(html).toContain(`data-chat-picture="${crop.url}"`);
  for (const attachment of attachments) expect(html).toContain(`>${attachment.handle}<`);
  // A crop is never a 3D cover.
  expect(html).not.toContain('chat-model-results');
});
