import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';
import { ContextCompactedDivider } from '@/components/studio/context-compacted-divider';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog }) }));

describe('the compaction divider (#322)', () => {
  it('says the context was compacted by hand, with the rounds, and marks the trigger', () => {
    const html = renderToStaticMarkup(<ContextCompactedDivider compaction={{ id: 'c1', createdAt: '2026-10-22T01:00:00Z', trigger: 'manual', rounds: 6, throughMessageId: 'm1' }} />);
    expect(html).toContain('data-testid="chat-context-compacted"');
    expect(html).toContain('data-trigger="manual"');
    expect(html).toContain(formatSchemaText(messages['ui.chat.context_compacted'], { rounds: 6 }));
  });

  it('says an automatic compaction was automatic', () => {
    const html = renderToStaticMarkup(<ContextCompactedDivider compaction={{ id: 'c2', createdAt: '2026-10-22T01:00:00Z', trigger: 'auto', rounds: 3, throughMessageId: 'm1' }} />);
    expect(html).toContain('data-trigger="auto"');
    expect(html).toContain(formatSchemaText(messages['ui.chat.context_compacted_auto'], { rounds: 3 }));
  });

  it('is placed in the conversation by the compaction records, outside the messages', () => {
    const chat = readFileSync(new URL('./chat-column.tsx', import.meta.url), 'utf8');
    expect(chat).toContain('compactionPlacement(');
    expect(chat).toContain('<ContextCompactedDivider');
    // The divider is never a chat message: it is not added to the messages that are shown, copied or sent.
    const display = chat.slice(chat.indexOf('const displayMessages = useMemo'), chat.indexOf('const processEvent'));
    expect(display).not.toContain('compaction');
  });
});
