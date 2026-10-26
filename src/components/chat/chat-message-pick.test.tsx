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

const pickButton = (html: string) => html.match(/<button[^>]*data-testid="candidate-pick"[^>]*>/)?.[0] ?? '';

const assistant = (images: Array<{ url: string; taskId: string }>, attachments: ChatAttachment[] = []): ChatMessageType => ({
  id: 'm1', role: 'assistant', content: 'done', images, attachments, createdAt: 1,
});
const image = (n: number) => ({ url: `/api/v1/tasks/t${n}/assets/a${n}`, taskId: `t${n}` });
const generated = (n: number): ChatAttachment => ({ handle: `@img${n}`, kind: 'image', role: 'generated', taskId: `t${n}`, assetId: `a${n}`, imageSlot: 1 });

it('offers 选用 on a generated candidate and calls back with a claim for it', () => {
  const claims: unknown[] = [];
  const html = renderToStaticMarkup(
    <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" pick={null} onPick={(claim) => { claims.push(claim); }} />,
  );
  expect(html).toContain('data-testid="candidate-pick"');
  expect(html).toContain('data-picked="false"');
  expect(html).toContain('>选用<');
  expect(pickButton(html)).not.toContain('disabled=""');
});

it('marks the picked candidate and shows the picked state instead of the action', () => {
  const pick: ChatAttachment = { handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' };
  const html = renderToStaticMarkup(
    <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" pick={pick} onPick={() => {}} />,
  );
  expect(html).toContain('data-picked="true"');
  expect(html).toContain('已选用');
  expect(pickButton(html)).toContain('disabled=""');
});

it('does not offer the pick on an uploaded picture or a user message, or without a pick handler', () => {
  const upload = renderToStaticMarkup(
    <ChatMessage message={assistant([{ url: 'data:image/png;base64,QQ==', taskId: '' }])} sessionId="s" pick={null} onPick={() => {}} />,
  );
  expect(pickButton(upload)).toContain('disabled=""');
  const user = renderToStaticMarkup(
    <ChatMessage message={{ ...assistant([image(1)]), role: 'user' }} sessionId="s" pick={null} onPick={() => {}} />,
  );
  expect(user).not.toContain('candidate-pick');
  const plain = renderToStaticMarkup(<ChatMessage message={assistant([image(1)])} sessionId="s" />);
  expect(plain).not.toContain('candidate-pick');
});

it('keeps an accepted pick that has no handle shown as picked', () => {
  const pick: ChatAttachment = { handle: '', kind: 'image', taskId: 't1', assetId: 'a1' };
  const html = renderToStaticMarkup(
    <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" pick={pick} onPick={() => {}} />,
  );
  expect(html).toContain('data-picked="true"');
  expect(pickButton(html)).toContain('disabled=""');
});

it('offers only the 2D pictures of a message for picking, never the picture of a 3D task', () => {
  // slot 1 is the model's render (@model1), slot 2 its pre-model picture, slot 3 a real 2D result
  const message = assistant(
    [
      { url: '/api/v1/tasks/t1/assets/render', taskId: 't1' },
      { url: '/api/v1/tasks/t1/assets/pre', taskId: 't1' },
      { url: '/api/v1/tasks/t2/assets/a2', taskId: 't2' },
    ],
    [
      { handle: '@model1', kind: 'model', taskId: 't1', imageSlot: 1 },
      { handle: '@img1', kind: 'image', taskId: 't1', assetId: 'pre', imageSlot: 2 },
      { handle: '@img2', kind: 'image', role: 'generated', taskId: 't2', assetId: 'a2', imageSlot: 3 },
    ],
  );
  const html = renderToStaticMarkup(<ChatMessage message={message} sessionId="s" pick={null} onPick={() => {}} />);
  expect(html.match(/data-testid="candidate-pick"/g)).toHaveLength(1);
  expect(html).toContain('data-count="1"');
  // The candidate keeps its place in the message: it is picture 3 and carries its own handle.
  expect(html).toContain(`aria-label="${messages['ui.chat.message.generated_image']} 3"`);
  expect(html).toContain('data-handle="@img2"');
  expect(html).not.toContain('data-handle="@img1"');
  // The model's render is the cover of its own card (the tracker has no model for it here), the pre-model picture is not shown.
  expect(html).toContain('data-testid="chat-model-results"');
  expect(html).toContain('/api/v1/tasks/t1/assets/render');
  expect(html).not.toContain('/api/v1/tasks/t1/assets/pre');
});

it('makes the picture click open the stage and keeps the detail behind a small icon', () => {
  const html = renderToStaticMarkup(
    <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" pick={null} onPick={() => {}} />,
  );
  const detail = html.match(/<button[^>]*data-testid="chat-result-detail"[^>]*>/)?.[0] ?? '';
  expect(detail).toContain(`aria-label="${messages['ui.result.card.detail']}"`);
  // The hover hint is a HintTooltip around the icon, not a native title.
  expect(detail).not.toContain('title=');
  expect(html).toContain('data-testid="chat-result-picture"');
  expect(html).toContain('data-testid="chat-result-to3d"');
  expect(html).toContain('data-testid="chat-result-download"');
  expect(html).toContain('data-testid="chat-result-active-handle"');
  // No second way in to the stage, no enlarge icon, and no credits in the card (the paid-step note states them).
  expect(html).not.toContain('data-testid="chat-result-open"');
  expect(html).not.toContain('data-testid="chat-result-credits"');
  expect(html).not.toContain('lucide-expand');
});
