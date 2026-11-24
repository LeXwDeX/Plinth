// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { AgentConfigurationSnapshot } from '@/lib/agent-configuration';
import AgentConfigurationPage from './page';

// jsdom has no PointerEvent; Base UI controls read it.
class TestPointerEvent extends MouseEvent {
  readonly pointerId: number;

  readonly pointerType: string;

  readonly isPrimary: boolean;

  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init);
    this.pointerId = init.pointerId ?? 1;
    this.pointerType = init.pointerType ?? 'mouse';
    this.isPrimary = init.isPrimary ?? true;
  }
}

beforeAll(() => {
  Object.defineProperty(window, 'PointerEvent', { configurable: true, writable: true, value: TestPointerEvent });
});

const schema = defaultCatalog.application as unknown as ProductSchema;
const ui = schema.agent_configuration.ui;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => schema.content.messages[key] ?? '' }),
}));

const configuration = {
  system_prompt: 'SCHEMA SYSTEM PROMPT',
  domain_context: { image: 'SCHEMA 2D CONTEXT', 'model-3d': 'SCHEMA 3D CONTEXT' },
  skills: [],
  enabled_tool_names: ['get_task', 'inspect_image'],
  enabled_mcp_preset_ids: [],
};
const snapshot: AgentConfigurationSnapshot = {
  revision: '7',
  configuration,
  prompts_readonly: true,
  presets: configuration,
  tools: [{ name: 'get_task', description: 'read a task' }, { name: 'inspect_image', description: 'look at an image' }],
  mcp_presets: [],
  history: [{ revision: '7', source: 'publish' }],
};

let calls: Array<{ route: string; body: unknown }> = [];

beforeEach(() => {
  calls = [];
  vi.stubGlobal('fetch', vi.fn(async (route: string, init?: RequestInit) => {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ route, body });
    const payload = route === schema.agent_configuration.routes.validate ? { valid: true, effective_prompts: {} } : snapshot;
    return { ok: true, status: 200, json: async () => payload } as Response;
  }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('agent configuration page (#554)', () => {
  it('shows the prompts read-only, without an editor or a restore-default action', async () => {
    const { container } = render(<AgentConfigurationPage />);
    await screen.findByText(ui.prompts_readonly);
    const readonly = screen.getByTestId('agent-prompts-readonly');
    expect(readonly.tagName).toBe('DETAILS');
    expect(readonly.textContent).toContain(ui.prompts_view);
    for (const text of ['SCHEMA SYSTEM PROMPT', 'SCHEMA 2D CONTEXT', 'SCHEMA 3D CONTEXT']) {
      expect(readonly.textContent).toContain(text);
    }
    expect(container.querySelectorAll('textarea#agent-system-prompt, textarea[id^="agent-domain-"]')).toHaveLength(0);
    expect(Array.from(container.querySelectorAll('textarea')).some(area => area.value.startsWith('SCHEMA'))).toBe(false);
    expect(screen.queryByTestId('agent-prompt-follow')).toBeNull();
  });

  it('saves a tool choice without submitting any prompt', async () => {
    const { container } = render(<AgentConfigurationPage />);
    await screen.findByText(ui.prompts_readonly);
    const tool = container.querySelector<HTMLInputElement>('#agent-tool-inspect_image');
    expect(tool).not.toBeNull();
    fireEvent.click(tool!);
    fireEvent.click(screen.getByRole('button', { name: ui.preview }));
    await waitFor(() => expect(calls.some(call => call.route === schema.agent_configuration.routes.validate)).toBe(true));
    const publish = screen.getByRole('button', { name: ui.publish });
    await waitFor(() => expect((publish as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(publish);
    await waitFor(() => expect(calls.some(call => call.route === schema.agent_configuration.routes.publish)).toBe(true));
    const published = calls.find(call => call.route === schema.agent_configuration.routes.publish)!.body as { expected_revision: string; configuration: Record<string, unknown> };
    expect(published.expected_revision).toBe('7');
    expect(published.configuration).not.toHaveProperty('system_prompt');
    expect(published.configuration).not.toHaveProperty('domain_context');
    expect(published.configuration.enabled_tool_names).toEqual(['get_task']);
  });
});
