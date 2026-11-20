import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ModelPicker, useDomainModelPick } from '@/components/studio/model-picker';
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

/** A session fixture; `extra` stands in for members older servers still send (the retired agent scope). */
function withSession(extra: Record<string, unknown> = {}) {
  useChatStore.setState({
    activeSessionId: 's1',
    sessions: [{ id: 's1', title: 't', createdAt: 1, updatedAt: 1, messages: [], ...extra } as never],
  });
}

afterEach(() => useChatStore.setState({ activeSessionId: null, sessions: [] }));

/** The models tab as the settings dialog builds it: both domains' picks (held by the always-mounted dialog), then the content. */
function Models() {
  const image = useDomainModelPick('image');
  const model = useDomainModelPick('model-3d');
  return <ModelPicker image={image} model={model} />;
}

/** Prints what each domain's table says about being switched off. */
function SwitchedOff() {
  const image = useDomainModelPick('image').section;
  const model = useDomainModelPick('model-3d').section;
  return <i>{`${image.disabledReason ?? ''}|${model.disabledReason ?? ''}`}</i>;
}

describe('the model picker without an Agent scope picker (#103)', () => {
  it('keeps both domain tables usable for every session shape, including older scoped ones', () => {
    for (const extra of [{}, { agentScope: '2d' }, { agentScope: '3d' }, { agentScope: 'auto' }, { agentScope: 'retired' }]) {
      withSession(extra);
      expect(renderToStaticMarkup(<SwitchedOff />).replace(/<\/?i>/g, '')).toBe('|');
    }
  });

  it('is one content for both domains, a table each, that stays usable whatever the older session stored (#405)', () => {
    for (const extra of [{}, { agentScope: '2d' }, { agentScope: '3d' }]) {
      withSession(extra);
      const html = renderToStaticMarkup(<Models />);
      expect(html.match(/data-testid="workspace-model-picker-panel"/g)?.length).toBe(1);
      expect(html.match(/data-testid="workspace-model-picker-image"/g)?.length).toBe(1);
      expect(html.match(/data-testid="workspace-model-picker-model"/g)?.length).toBe(1);
      // The old entry button is gone with the pickers under the box: nothing to open, no table switched off.
      expect(html).not.toContain('data-testid="workspace-model-picker"');
      expect(html).not.toContain('<button');
      expect(html).not.toContain('disabled=""');
    }
  });

  it('writes a 2D or 3D pick through the serial lock writer and rolls back with a toast on failure', () => {
    const source = readFileSync(new URL('./model-picker.tsx', import.meta.url), 'utf8');
    expect(source).toContain('pickExplicitModel3D');
    expect(source).toContain('pickExplicitModelImage');
    expect(source).toContain('setModel(domain, previous)');
    expect(source).toContain("toast.error(schemaText(schema, 'ui.studio.picker.lock_failed'))");
    // The old fire-and-forget call swallowed every failure.
    expect(source).not.toContain('.catch(() => undefined)');
  });
});
