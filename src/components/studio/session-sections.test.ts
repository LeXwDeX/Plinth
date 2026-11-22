import { describe, expect, it } from 'vitest';

import { sessionSections } from './session-menu';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatSession } from '@/store/chat-store';

const schema = (groups?: { source: string; label: string }[]) =>
  ({
    chat: { api: { session_groups: groups } },
    content: {
      messages: {
        'ui.studio.session.group.chat': 'Sessions',
        'ui.studio.session.group.mcp': 'From MCP',
      },
    },
  }) as unknown as ProductSchema;

const session = (id: string, source?: string): ChatSession => ({
  id,
  title: id,
  createdAt: '',
  updatedAt: '',
  ...(source ? { source } : {}),
});

describe('session list sections', () => {
  it('keeps one flat section when the Schema declares no grouping', () => {
    expect(sessionSections(schema(), [session('a'), session('b', 'mcp')])).toEqual([
      { key: '', label: '', sessions: [session('a'), session('b', 'mcp')] },
    ]);
  });

  it('lists the declared groups in order, named by the message each label points at', () => {
    const sections = sessionSections(
      schema([
        { source: 'mcp', label: 'ui.studio.session.group.mcp' },
        { source: 'chat', label: 'ui.studio.session.group.chat' },
      ]),
      [session('a', 'chat'), session('b', 'mcp')],
    );
    expect(sections.map((section) => section.label)).toEqual(['From MCP', 'Sessions']);
    expect(sections.map((section) => section.sessions.map((item) => item.id))).toEqual([['b'], ['a']]);
  });

  it('falls back to the ordinary sessions for a source no group names', () => {
    const sections = sessionSections(
      schema([
        { source: 'chat', label: 'ui.studio.session.group.chat' },
        { source: 'mcp', label: 'ui.studio.session.group.mcp' },
      ]),
      [session('a'), session('b', 'later')],
    );
    expect(sections.map((section) => section.sessions.map((item) => item.id))).toEqual([['a', 'b'], []]);
  });

  it('names no group when every group has a heading the reader can see', () => {
    const sections = sessionSections(
      schema([{ source: 'mcp', label: 'ui.studio.session.group.mcp' }]),
      [session('a', 'mcp')],
    );
    expect(sections.map((section) => section.label)).toEqual(['From MCP']);
  });
});