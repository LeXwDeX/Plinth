import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AttachmentThumb } from '@/components/studio/attachment-thumb';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

describe('attachment thumbnail', () => {
  it('shows the small picture at rest and no preview until it is pointed at', () => {
    const html = renderToStaticMarkup(<AttachmentThumb url="/a.png" alt="附件 1" />);
    expect(html).toContain('data-testid="attachment-thumb"');
    expect(html).toContain('alt="附件 1"');
    expect(html).not.toContain('attachment-hover-preview');
  });

  it('shows a platform picture through its preview and a picture not yet uploaded as it is (#292)', () => {
    const asset = renderToStaticMarkup(<AttachmentThumb url="/api/v1/assets/asset_1" alt="附件 1" />);
    expect(asset).toContain('src="/api/v1/assets/asset_1?size=preview"');
    const local = renderToStaticMarkup(<AttachmentThumb url="data:image/png;base64,UE5HIQ==" alt="附件 1" />);
    expect(local).toContain('src="data:image/png;base64,UE5HIQ=="');
    // Anything else is never put into an image address.
    const script = renderToStaticMarkup(<AttachmentThumb url="data:image/svg+xml;base64,PHN2Zz4=" alt="附件 1" />);
    expect(script).not.toContain('<img');
  });

  it('is the picture of every composer attachment chip, with its size text declared in the Schema', () => {
    const composer = readFileSync(new URL('./composer.tsx', import.meta.url), 'utf8');
    expect(composer).toContain('<AttachmentThumb url={item.url}');
    expect(messages['ui.studio.composer.attachment.pixels']).toContain('{width}');
    expect(messages['ui.studio.composer.attachment.pixels']).toContain('{height}');
  });
});
