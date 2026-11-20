import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { approvalModeOptions, defaultApprovalMode } from '@/lib/chat/approval-modes';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

// #406: a new session starts in the automatic approval mode. The default is the
// Product Schema's `chat.approval_policy.default_mode`; the page never holds a
// value of its own, and a session that already exists shows its stored mode.
describe('default approval mode (#406)', () => {
  it('is the Schema default, which the shipped Catalog sets to auto', () => {
    const schema = schemaFixture();
    expect(schema.chat.approval_policy.default_mode).toBe('auto');
    expect(defaultApprovalMode(schema)).toBe('auto');
    // The top bar can show it: it is one of the modes the menu offers.
    expect(approvalModeOptions(schema).map((option) => option.mode)).toContain(defaultApprovalMode(schema));
  });

  it('follows the Schema instead of a value written in the page', () => {
    const schema = schemaFixture();
    for (const mode of ['budget', 'manual', 'auto'] as const) {
      schema.chat.approval_policy.default_mode = mode;
      expect(defaultApprovalMode(schema)).toBe(mode);
    }
  });

  it('reads a default the page does not know as the strict mode', () => {
    const schema = schemaFixture();
    schema.chat.approval_policy.default_mode = 'yolo';
    expect(defaultApprovalMode(schema)).toBe('manual');
  });

  it('keeps the mode a stored session carries, and never fills a gap with the default', async () => {
    const schema = schemaFixture();
    expect(defaultApprovalMode(schema)).toBe('auto');
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json([
        { [fields.id]: 'budget', [fields.title]: 'B', [fields.approval_mode]: 'budget' },
        { [fields.id]: 'manual', [fields.title]: 'M', [fields.approval_mode]: 'manual' },
        { [fields.id]: 'auto', [fields.title]: 'A', [fields.approval_mode]: 'auto' },
        { [fields.id]: 'bare', [fields.title]: 'N' },
      ]),
    });
    const sessions = await client.listSessions();
    expect(sessions.map((session) => [session.id, session.approvalMode])).toEqual([
      ['budget', 'budget'],
      ['manual', 'manual'],
      ['auto', 'auto'],
      ['bare', 'manual'],
    ]);
  });
});
