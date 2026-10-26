import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { SentImages } from '@/components/chat/result-card';
import type { ChatAttachment } from '@/store/chat-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

const render = (urls: string[], attachments: ChatAttachment[] = []) => renderToStaticMarkup(
  <SentImages images={urls.map((url) => ({ url, taskId: '' }))} attachments={attachments} labelPrefix="上传图" onPreview={() => {}} />,
);

describe('pictures the user sent (#292)', () => {
  it('shows an uploaded picture through its preview and marks the original for copying', () => {
    const html = render(['/api/v1/assets/asset_1'], [{ handle: '@img1', kind: 'image', role: 'upload', assetId: 'asset_1', imageSlot: 1 }]);
    expect(html).toContain('src="/api/v1/assets/asset_1?size=preview"');
    expect(html).toContain('data-chat-picture="/api/v1/assets/asset_1"');
    expect(html).toContain('draggable="true"');
    expect(html).toContain('@img1');
  });

  it('shows a picture an older message stored as a raster data address, without copying it into attributes', () => {
    const picture = 'data:image/png;base64,UE5HIQ==';
    const html = render([picture]);
    expect(html).toContain(`src="${picture}"`);
    // Once in the image address, nowhere else: no copy marker, no drag payload.
    expect(html.split(picture)).toHaveLength(2);
    expect(html).not.toContain('data-chat-picture=');
    expect(html).not.toContain('draggable');
  });

  it('keeps the placeholder for any other data address', () => {
    for (const picture of ['data:image/svg+xml;base64,PHN2Zz4=', 'data:text/html;base64,PGI+', 'data:image/png,raw']) {
      const html = render([picture]);
      expect(html).not.toContain('<img');
      expect(html).not.toContain(picture);
    }
  });
});
