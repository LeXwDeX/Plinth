import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ModelPicker, useDomainModelPick } from '@/components/studio/model-picker';
import type { AgentScope } from '@/store/chat-store';
import { useChatStore } from '@/store/chat-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

// Server rendering reads a zustand store's initial snapshot, so the selectors read the live state instead.
vi.mock('@/store/chat-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat-store')>();
  const useChatStore = Object.assign(
    (selector: (state: ReturnType<typeof actual.useChatStore.getState>) => unknown) => selector(actual.useChatStore.getState()),
    actual.useChatStore,
  );
  return { ...actual, useChatStore };
});

function inScope(agentScope: AgentScope | undefined) {
  useChatStore.setState({
    activeSessionId: 's1',
    sessions: [{ id: 's1', title: 't', createdAt: 1, updatedAt: 1, messages: [], agentScope } as never],
  });
}

function trigger(html: string, testId: string): string {
  return html.match(new RegExp(`<button[^>]*data-testid="${testId}"[^>]*>`))?.[0] ?? '';
}

afterEach(() => useChatStore.setState({ activeSessionId: null, sessions: [] }));

/** Prints what each domain's table says about being switched off. */
function SwitchedOff() {
  const image = useDomainModelPick('image').section;
  const model = useDomainModelPick('model-3d').section;
  return <i>{`${image.disabledReason ?? ''}|${model.disabledReason ?? ''}`}</i>;
}

describe('the model picker without an Agent scope picker (#103)', () => {
  it('keeps both domain tables usable even when an older session carries a scope', () => {
    for (const scope of ['2d', '3d', 'auto'] as const) {
      inScope(scope);
      expect(renderToStaticMarkup(<SwitchedOff />).replace(/<\/?i>/g, '')).toBe('|');
    }
  });

  it('is one trigger for both domains that stays usable whatever the stored scope', () => {
    inScope('2d');
    const html = renderToStaticMarkup(<ModelPicker />);
    expect(html.match(/data-testid="workspace-model-picker"/g)?.length).toBe(1);
    expect(trigger(html, 'workspace-model-picker')).not.toContain('disabled=""');
  });
});
