import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AgentScopePicker } from '@/components/studio/agent-scope-picker';
import { ModelPicker, useDomainModelPick } from '@/components/studio/model-picker';
import { formatSchemaText } from '@/lib/product-schema';
import type { WorkspaceDomain } from '@/store/workspace-store';
import { useChatStore, type AgentScope } from '@/store/chat-store';

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

const SESSION = 's1';

function inScope(agentScope: AgentScope | undefined) {
  useChatStore.setState({
    activeSessionId: SESSION,
    sessions: [{ id: SESSION, title: 't', createdAt: 1, updatedAt: 1, messages: [], agentScope } as never],
  });
}

function trigger(html: string, testId: string): string {
  return html.match(new RegExp(`<button[^>]*data-testid="${testId}"[^>]*>`))?.[0] ?? '';
}

afterEach(() => useChatStore.setState({ activeSessionId: null, sessions: [] }));

describe('the Agent scope picker', () => {
  it('shows the label of the session scope, and the default (auto) when the session has none', () => {
    inScope('2d');
    expect(renderToStaticMarkup(<AgentScopePicker />)).toContain('2D 模式');
    inScope('3d');
    expect(renderToStaticMarkup(<AgentScopePicker />)).toContain('3D 模式');
    inScope(undefined);
    expect(renderToStaticMarkup(<AgentScopePicker />)).toContain('自动模式');
  });

  it('is usable with a session and off without one', () => {
    inScope('auto');
    expect(trigger(renderToStaticMarkup(<AgentScopePicker />), 'workspace-agent-scope-picker')).not.toContain('disabled=""');
    useChatStore.setState({ activeSessionId: null, sessions: [] });
    expect(trigger(renderToStaticMarkup(<AgentScopePicker />), 'workspace-agent-scope-picker')).toContain('disabled=""');
  });
});

/** Prints the reason a domain's table is switched off, or nothing when it is usable. */
function Reason({ domain }: { domain: WorkspaceDomain }) {
  return <i>{useDomainModelPick(domain).section.disabledReason ?? ''}</i>;
}

describe('the model picker follows the Agent scope', () => {
  const states = () => ({
    image: renderToStaticMarkup(<Reason domain="image" />).replace(/<\/?i>/g, ''),
    model: renderToStaticMarkup(<Reason domain="model-3d" />).replace(/<\/?i>/g, ''),
  });

  it('greys the 3D table with the reason in 2D mode and leaves the 2D table usable', () => {
    inScope('2d');
    const { image, model } = states();
    expect(image).toBe('');
    expect(model).toContain('2D 模式');
  });

  it('greys the 2D table with the reason in 3D mode and leaves the 3D table usable', () => {
    inScope('3d');
    const { image, model } = states();
    expect(model).toBe('');
    expect(image).toContain('3D 模式');
  });

  it('greys neither in auto mode', () => {
    inScope('auto');
    const { image, model } = states();
    expect(image).toBe('');
    expect(model).toBe('');
  });

  it('is one trigger for both domains that stays usable whatever the scope', () => {
    inScope('2d');
    const html = renderToStaticMarkup(<ModelPicker />);
    expect(html.match(/data-testid="workspace-model-picker"/g)?.length).toBe(1);
    expect(trigger(html, 'workspace-model-picker')).not.toContain('disabled=""');
  });
});

describe('the composer', () => {
  const composer = readFileSync(new URL('./composer.tsx', import.meta.url), 'utf8');

  it('puts the scope picker where the key hint used to be and moves the hint into the placeholder', () => {
    expect(composer).toContain('<AgentScopePicker />');
    // A text box carries no hover hint: the keys are part of its placeholder, and the box has no native title.
    expect(composer).toContain("schemaText(schema, 'ui.studio.composer.placeholder_with_keys')");
    expect(composer).toContain("keys: schemaText(schema, 'ui.studio.composer.hint')");
    expect(composer).not.toMatch(/<textarea[^>]*\btitle=/);
    expect(composer).not.toMatch(/<span[^>]*>\s*\{schemaText\(schema, 'ui\.studio\.composer\.hint'\)\}/);
    const placeholder = formatSchemaText(messages['ui.studio.composer.placeholder_with_keys'], {
      placeholder: messages['ui.workspace.agent.placeholder'],
      keys: messages['ui.studio.composer.hint'],
    });
    expect(placeholder).toBe('告诉 Agent 你想创作什么…（Enter 发送，Shift+Enter 换行）');
  });
});
