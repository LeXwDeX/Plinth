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
// The snapshot the server returns; a test with shared Skills swaps it (#601).
let served: AgentConfigurationSnapshot = snapshot;
const maxSkillCount = schema.agent_configuration.limits.max_skill_count;

beforeEach(() => {
  calls = [];
  served = snapshot;
  vi.stubGlobal('fetch', vi.fn(async (route: string, init?: RequestInit) => {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ route, body });
    const payload = route === schema.agent_configuration.routes.validate ? { valid: true, effective_prompts: {} } : served;
    return { ok: true, status: 200, json: async () => payload } as Response;
  }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  schema.agent_configuration.limits.max_skill_count = maxSkillCount;
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

// #670: a history version shows its time in the admin date format, not the stored ISO string.
describe('agent configuration history time (#670)', () => {
  it('formats the version time instead of showing the ISO string', async () => {
    const iso = '2026-11-20T08:30:00Z';
    served = { ...snapshot, history: [{ revision: '7', source: 'publish', created_at: iso }] };
    render(<AgentConfigurationPage />);
    const presentation = schema.administration.audit_logs.presentation;
    const formatted = new Intl.DateTimeFormat(presentation.date_locale, presentation.date_time_options as Intl.DateTimeFormatOptions).format(new Date(iso));
    expect(await screen.findByText(`7 · ${formatted} · publish`)).toBeTruthy();
    expect(screen.queryByText(new RegExp(iso))).toBeNull();
  });
});

describe('agent configuration shared Skills (#601)', () => {
  const field = (container: HTMLElement, name: string) => container.querySelector<HTMLInputElement | HTMLTextAreaElement>(`[id^="agent-skill-skill_"][id$="-${name}"]`);
  const addButton = () => screen.getByRole('button', { name: ui.add_skill }) as HTMLButtonElement;

  it('says there are no shared Skills and offers to add one, without an empty search or picker', async () => {
    const { container } = render(<AgentConfigurationPage />);
    expect(await screen.findByText(ui.skills_empty)).toBeTruthy();
    expect(JSON.stringify(defaultCatalog)).toContain('"agent_configuration.ui.skills_empty"');
    expect(addButton().disabled).toBe(false);
    expect(container.querySelector('#agent-skill-search')).toBeNull();
    expect(container.querySelector('#agent-skill-edit')).toBeNull();
    expect(screen.queryByRole('button', { name: ui.remove_skill })).toBeNull();
  });

  it('adds a blank shared Skill that opens for editing and is submitted for checking', async () => {
    const { container } = render(<AgentConfigurationPage />);
    await screen.findByText(ui.skills_empty);
    fireEvent.click(addButton());
    expect(screen.queryByText(ui.skills_empty)).toBeNull();
    for (const [name, value] of [['name', 'Ink wash'], ['category', 'style'], ['prompt', 'ink wash style']] as const) {
      const input = field(container, name);
      expect(input).not.toBeNull();
      expect(input!.value).toBe('');
      fireEvent.change(input!, { target: { value } });
      expect(field(container, name)!.value).toBe(value);
    }
    expect(container.querySelector('#agent-skill-search')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: ui.preview }));
    await waitFor(() => expect(calls.some(call => call.route === schema.agent_configuration.routes.validate)).toBe(true));
    const checked = calls.find(call => call.route === schema.agent_configuration.routes.validate)!.body as { configuration: { skills: Array<Record<string, unknown>> } };
    expect(checked.configuration.skills).toHaveLength(1);
    expect(checked.configuration.skills[0]).toMatchObject({ name: 'Ink wash', category: 'style', prompt: 'ink wash style', recommended_size: schema.chat.skills.creation.recommended_size, thumbnail_path: '', raw_image_url: '' });
    expect(String(checked.configuration.skills[0].id)).toMatch(/^skill_[0-9a-f]{32}$/);
  });

  it('disables adding at the Skill limit and says why', async () => {
    schema.agent_configuration.limits.max_skill_count = 1;
    render(<AgentConfigurationPage />);
    await screen.findByText(ui.skills_empty);
    fireEvent.click(addButton());
    const add = addButton();
    expect(add.disabled).toBe(true);
    fireEvent.focus(add.parentElement!);
    fireEvent.pointerEnter(add.parentElement!);
    fireEvent.mouseEnter(add.parentElement!);
    expect((await screen.findByTestId('hint-reason', {}, { timeout: 2000 })).textContent).toContain(schema.content.messages['ui.unavailable.reason.admin.agent_skill_limit']);
  });

  it('disables the empty-state add when no Skill is allowed at all', async () => {
    schema.agent_configuration.limits.max_skill_count = 0;
    render(<AgentConfigurationPage />);
    await screen.findByText(ui.skills_empty);
    expect(addButton().disabled).toBe(true);
  });

  it('returns to the empty state after the last Skill is removed', async () => {
    render(<AgentConfigurationPage />);
    await screen.findByText(ui.skills_empty);
    fireEvent.click(addButton());
    fireEvent.click(screen.getByRole('button', { name: ui.remove_skill }));
    expect(screen.getByText(ui.skills_empty)).toBeTruthy();
    expect(addButton().disabled).toBe(false);
  });

  it('keeps search, choice, editing, adding and removing when shared Skills exist', async () => {
    const skill = (id: string, name: string) => ({ id, name, category: 'style', recommended_size: schema.chat.skills.creation.recommended_size, thumbnail_path: '', raw_image_url: '', prompt: `${name} prompt` });
    served = { ...snapshot, configuration: { ...configuration, skills: [skill('skill-a', 'Alpha'), skill('skill-b', 'Beta')] } };
    const { container } = render(<AgentConfigurationPage />);
    await screen.findByText(ui.prompts_readonly);
    expect(screen.queryByText(ui.skills_empty)).toBeNull();
    expect(container.querySelector('#agent-skill-search')).not.toBeNull();
    expect(container.querySelector<HTMLInputElement>('#agent-skill-skill-a-name')!.value).toBe('Alpha');
    fireEvent.change(container.querySelector('#agent-skill-skill-a-name')!, { target: { value: 'Alpha 2' } });
    expect(container.querySelector<HTMLInputElement>('#agent-skill-skill-a-name')!.value).toBe('Alpha 2');
    fireEvent.click(addButton());
    const copy = field(container, 'name');
    expect(copy).not.toBeNull();
    expect(copy!.value).toBe('Alpha 2');
    fireEvent.click(screen.getByRole('button', { name: ui.remove_skill }));
    expect(field(container, 'name')).toBeNull();
    expect(container.querySelector<HTMLInputElement>('#agent-skill-skill-a-name')!.value).toBe('Alpha 2');
    fireEvent.click(screen.getByRole('button', { name: ui.preview }));
    await waitFor(() => expect(calls.some(call => call.route === schema.agent_configuration.routes.validate)).toBe(true));
    const checked = calls.find(call => call.route === schema.agent_configuration.routes.validate)!.body as { configuration: { skills: Array<{ id: string }> } };
    expect(checked.configuration.skills.map(item => item.id)).toEqual(['skill-a', 'skill-b']);
  });
});
