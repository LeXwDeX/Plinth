// @vitest-environment jsdom

import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import type { ChatAttachment, ChatMessage as ChatMessageType } from '@/store/chat-store';

const schema = defaultCatalog.application as { content: { messages: Record<string, string> } } & Record<string, unknown>;
const messages = schema.content.messages;
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));

afterEach(cleanup);

const assistant = (images: Array<{ url: string; taskId: string }>, attachments: ChatAttachment[] = []): ChatMessageType => ({
  id: 'm1', role: 'assistant', content: 'done', images, attachments, createdAt: 1,
});
const image = (n: number) => ({ url: `/api/v1/tasks/t${n}/assets/a${n}`, taskId: `t${n}` });
const generated = (n: number): ChatAttachment => ({ handle: `@img${n}`, kind: 'image', role: 'generated', taskId: `t${n}`, assetId: `a${n}`, imageSlot: n });

describe('rejecting a 2D candidate', () => {
  it('offers 不要这个 beside every generated candidate and reports the already rejected one', () => {
    const html = renderToStaticMarkup(
      <ChatMessage message={assistant([image(1), image(2)], [generated(1), generated(2)])} sessionId="s"
        rejectedAssets={new Set(['a2'])} onReject={() => {}} />,
    );
    expect(html.match(/data-testid="candidate-reject"/g)).toHaveLength(2);
    expect(html).toContain(`>${messages['ui.chat.image.reject']}<`);
    expect(html).toContain(`>${messages['ui.chat.image.rejected']}<`);
    expect(html).toContain('data-rejected="true"');
  });

  it('offers no rejection without a handler or without the declared preference routes', () => {
    const plain = renderToStaticMarkup(
      <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" />,
    );
    expect(plain).not.toContain('candidate-reject');
  });

  it('opens the reason dialog and sends the claim with the chosen preset and note', async () => {
    const onReject = vi.fn(async (_claim: ChatAttachmentClaim, _preset: string, _text: string) => true);
    render(
      <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" onReject={onReject} />,
    );
    fireEvent.click(screen.getByTestId('candidate-reject'));
    expect(screen.getByText(messages['ui.chat.image.reject_dialog_title'])).toBeTruthy();
    // The presets come from the public Schema's ids; their labels are localized UI messages.
    fireEvent.click(screen.getByRole('radio', { name: messages['ui.workspace.preferences.reason.style'] }));
    fireEvent.change(screen.getByLabelText(messages['ui.chat.image.reject_text_label']), {
      target: { value: '  too realistic  ' },
    });
    fireEvent.click(screen.getByTestId('reject-candidate-confirm'));
    await waitFor(() => expect(onReject).toHaveBeenCalledOnce());
    expect(onReject.mock.calls[0][0]).toMatchObject({ kind: 'image', taskId: 't1', assetId: 'a1' });
    expect(onReject.mock.calls[0][1]).toBe('style');
    expect(onReject.mock.calls[0][2]).toBe('too realistic');
  });

  it('keeps the dialog open and shows the failure when the server refuses', async () => {
    const onReject = vi.fn(async (_claim: ChatAttachmentClaim, _preset: string, _text: string) => false);
    render(
      <ChatMessage message={assistant([image(1)], [generated(1)])} sessionId="s" onReject={onReject} />,
    );
    fireEvent.click(screen.getByTestId('candidate-reject'));
    fireEvent.click(screen.getByTestId('reject-candidate-confirm'));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(messages['ui.chat.image.reject_failed']));
    expect(screen.getByText(messages['ui.chat.image.reject_dialog_title'])).toBeTruthy();
  });
});