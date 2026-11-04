// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { BriefPanel } from '@/components/studio/brief-panel';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const text = (key: string) => schemaText(schema, key);
const schemaFixture = vi.hoisted(() => ({ current: null as unknown as ProductSchema }));

const swr = vi.hoisted(() => ({
  data: undefined as unknown, error: undefined as unknown, isLoading: false, mutate: vi.fn(),
}));
const client = vi.hoisted(() => {
  class ChatBrowserError extends Error {
    readonly status = 500;
    readonly requestId = '';
    constructor(message: string) { super(message); this.name = 'ChatBrowserError'; }
  }
  return {
    getBrief: vi.fn(async () => ({})),
    setBrief: vi.fn(async () => ({})),
    mutate: vi.fn(async () => undefined),
    ChatBrowserError,
  };
});

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: schemaFixture.current }) }));
vi.mock('swr', () => ({
  default: () => ({ data: swr.data, error: swr.error, isLoading: swr.isLoading, mutate: swr.mutate }),
}));
vi.mock('@/lib/chat/browser-client', () => ({
  createChatBrowserClient: () => ({ getBrief: client.getBrief, setBrief: client.setBrief }),
  ChatBrowserError: client.ChatBrowserError,
}));

const session = { id: 's1', title: '会话', createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z' };

beforeEach(() => {
  schemaFixture.current = schema;
  swr.data = undefined;
  swr.error = undefined;
  swr.isLoading = false;
  swr.mutate = client.mutate;
  client.getBrief.mockReset();
  client.getBrief.mockResolvedValue({});
  client.setBrief.mockReset();
  client.setBrief.mockResolvedValue({});
  client.mutate.mockReset();
  client.mutate.mockResolvedValue(undefined);
  useChatStore.setState({ sessions: [session], activeSessionId: 's1' });
});
afterEach(cleanup);

describe('the production brief panel (#103)', () => {
  it('invites the person to start 3D work when no brief is stored', () => {
    const view = render(<BriefPanel sessionId="s1" />);
    expect(view.getByTestId('studio-brief-title').textContent).toBe(text('ui.studio.brief.title'));
    expect(view.getByTestId('studio-brief-empty').textContent).toBe(text('ui.studio.brief.empty'));
    expect(view.getByTestId('studio-brief-edit').textContent).toContain(text('ui.studio.brief.edit'));
    expect(view.queryByTestId('studio-brief-values')).toBeNull();
  });

  it('shows the stored brief with the declared labels and the reader’s words for a profile and booleans', () => {
    swr.data = {
      purpose: 'game_prop', style: '低模', target_platform: '手机', needs_parts: false, needs_rig: true,
      triangle_budget: 12000, notes: '不要圆角',
    };
    const view = render(<BriefPanel sessionId="s1" />);
    expect(view.getByTestId('studio-brief-value-purpose').textContent).toBe('游戏/实时·道具与场景件');
    expect(view.getByTestId('studio-brief-value-style').textContent).toBe('低模');
    expect(view.getByTestId('studio-brief-value-target_platform').textContent).toBe('手机');
    expect(view.getByTestId('studio-brief-value-needs_parts').textContent).toBe(text('ui.studio.brief.no'));
    expect(view.getByTestId('studio-brief-value-needs_rig').textContent).toBe(text('ui.studio.brief.yes'));
    expect(view.getByTestId('studio-brief-value-triangle_budget').textContent).toBe('12000');
    expect(view.getByTestId('studio-brief-value-notes').textContent).toBe('不要圆角');
    // A boolean that was never answered is not a value of the brief.
    expect(view.queryByTestId('studio-brief-value-needs_animation')).toBeNull();
  });

  it('edits every declared field with its type and limits', () => {
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    const form = view.getByTestId('studio-brief-form');
    expect(form.textContent).toContain('用途');
    expect(form.textContent).toContain('三角面预算');
    const purpose = view.getByTestId('studio-brief-field-purpose') as HTMLSelectElement;
    expect(purpose.tagName).toBe('SELECT');
    // The empty option plus the five declared usage profiles.
    expect(purpose.options).toHaveLength(6);
    expect(purpose.options[0].textContent).toBe(text('ui.studio.brief.none'));
    expect([...purpose.options].map((option) => option.value)).toContain('game_prop');
    const styleField = schema.chat.model3d_usage!.brief!.fields!.find((field) => field.name === 'style')!;
    const style = view.getByTestId('studio-brief-field-style') as HTMLInputElement | HTMLTextAreaElement;
    expect(style.tagName).toBe(styleField.multiline === true ? 'TEXTAREA' : 'INPUT');
    if (styleField.multiline !== true) {
      expect((style as HTMLInputElement).type).toBe('text');
    }
    expect(style.maxLength).toBe(200);
    expect(style.placeholder).toBe(styleField.placeholder ?? '');
    const target = view.getByTestId('studio-brief-field-target_platform') as HTMLInputElement;
    expect(target.maxLength).toBe(120);
    for (const name of ['needs_parts', 'needs_rig', 'needs_animation']) {
      expect((view.getByTestId(`studio-brief-field-${name}`) as HTMLInputElement).type).toBe('checkbox');
    }
    const budget = view.getByTestId('studio-brief-field-triangle_budget') as HTMLInputElement;
    expect(budget.type).toBe('number');
    expect(budget.min).toBe('100');
    expect(budget.max).toBe('2000000');
    const notesField = schema.chat.model3d_usage!.brief!.fields!.find((field) => field.name === 'notes')!;
    const notes = view.getByTestId('studio-brief-field-notes') as HTMLTextAreaElement;
    expect(notes.tagName).toBe(notesField.multiline === true ? 'TEXTAREA' : 'INPUT');
    expect(notes.maxLength).toBe(1000);
    expect(notes.placeholder).toBe(notesField.placeholder ?? '');
  });

  it('renders a textarea and the hint only where the Schema declares them', () => {
    const custom = structuredClone(defaultCatalog.application) as unknown as ProductSchema;
    const fields = custom.chat.model3d_usage!.brief!.fields!;
    const style = fields.find((field) => field.name === 'style')!;
    style.multiline = true;
    style.placeholder = '写清风格';
    const notes = fields.find((field) => field.name === 'notes')!;
    notes.multiline = false;
    notes.placeholder = '写清限制';
    schemaFixture.current = custom;
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    const styleInput = view.getByTestId('studio-brief-field-style') as HTMLTextAreaElement;
    expect(styleInput.tagName).toBe('TEXTAREA');
    expect(styleInput.placeholder).toBe('写清风格');
    const notesInput = view.getByTestId('studio-brief-field-notes') as HTMLInputElement;
    expect(notesInput.tagName).toBe('INPUT');
    expect(notesInput.type).toBe('text');
    expect(notesInput.placeholder).toBe('写清限制');
  });

  it('falls back to a single line and no hint when the Schema declares neither', () => {
    const custom = structuredClone(defaultCatalog.application) as unknown as ProductSchema;
    for (const field of custom.chat.model3d_usage!.brief!.fields!) {
      delete field.multiline;
      delete field.placeholder;
    }
    schemaFixture.current = custom;
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    const notes = view.getByTestId('studio-brief-field-notes') as HTMLInputElement;
    expect(notes.tagName).toBe('INPUT');
    expect(notes.placeholder).toBe('');
    const style = view.getByTestId('studio-brief-field-style') as HTMLInputElement;
    expect(style.tagName).toBe('INPUT');
    expect(style.placeholder).toBe('');
  });

  it('saves the whole declared object and shows the stored answer', async () => {
    client.setBrief.mockResolvedValueOnce({ purpose: 'game_prop', style: '低模' });
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    fireEvent.change(view.getByTestId('studio-brief-field-purpose'), { target: { value: 'game_prop' } });
    fireEvent.change(view.getByTestId('studio-brief-field-style'), { target: { value: '  低模  ' } });
    fireEvent.click(view.getByTestId('studio-brief-field-needs_rig'));
    fireEvent.click(view.getByTestId('studio-brief-save'));
    await waitFor(() => expect(view.queryByTestId('studio-brief-form')).toBeNull());
    expect(client.setBrief).toHaveBeenCalledWith('s1', {
      purpose: 'game_prop', style: '低模', needs_rig: true,
    });
    expect(client.mutate).toHaveBeenCalledWith({ purpose: 'game_prop', style: '低模' }, { revalidate: false });
    expect(useChatStore.getState().sessions[0].brief).toEqual({ purpose: 'game_prop', style: '低模' });
    expect(view.getByTestId('studio-brief-saved').textContent).toBe(text('ui.studio.brief.saved'));
    expect(view.getByTestId('studio-brief-value-style').textContent).toBe('低模');
  });

  it('clears the form and sends an empty brief as a cleared one', async () => {
    swr.data = { style: '低模' };
    client.setBrief.mockResolvedValueOnce({});
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    expect((view.getByTestId('studio-brief-field-style') as HTMLInputElement).value).toBe('低模');
    fireEvent.click(view.getByTestId('studio-brief-clear'));
    expect((view.getByTestId('studio-brief-field-style') as HTMLInputElement).value).toBe('');
    fireEvent.click(view.getByTestId('studio-brief-save'));
    await waitFor(() => expect(view.queryByTestId('studio-brief-form')).toBeNull());
    expect(client.setBrief).toHaveBeenCalledWith('s1', {});
  });

  it('cancels an edit without saving or changing the stored brief', () => {
    swr.data = { style: '低模' };
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    fireEvent.change(view.getByTestId('studio-brief-field-style'), { target: { value: '别的风格' } });
    fireEvent.click(view.getByTestId('studio-brief-cancel'));
    expect(view.queryByTestId('studio-brief-form')).toBeNull();
    expect(client.setBrief).not.toHaveBeenCalled();
    expect(view.getByTestId('studio-brief-value-style').textContent).toBe('低模');
  });

  it('says why a save failed and keeps the form open to try again', async () => {
    client.setBrief.mockRejectedValueOnce(new client.ChatBrowserError('ui.studio.brief.load_failed'));
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    fireEvent.change(view.getByTestId('studio-brief-field-style'), { target: { value: '低模' } });
    fireEvent.click(view.getByTestId('studio-brief-save'));
    const error = await view.findByTestId('studio-brief-error');
    expect(error.textContent).toBe(formatSchemaText(text('ui.studio.brief.save_failed'), {
      reason: text('ui.studio.brief.load_failed'),
    }));
    expect(view.getByTestId('studio-brief-form')).toBeTruthy();
    expect((view.getByTestId('studio-brief-field-style') as HTMLInputElement).value).toBe('低模');
  });

  it('words an unknown save failure with the Schema’s own sentence', async () => {
    client.setBrief.mockRejectedValueOnce(new Error('boom'));
    const view = render(<BriefPanel sessionId="s1" />);
    fireEvent.click(view.getByTestId('studio-brief-edit'));
    fireEvent.click(view.getByTestId('studio-brief-save'));
    const error = await view.findByTestId('studio-brief-error');
    expect(error.textContent).toBe(formatSchemaText(text('ui.studio.brief.save_failed'), {
      reason: schema.chat.presentation.messages.unknown_error,
    }));
  });

  it('says it is reading the brief, and says so when the read failed', () => {
    swr.isLoading = true;
    const view = render(<BriefPanel sessionId="s1" />);
    expect(view.getByTestId('studio-brief-loading').textContent).toBe(text('ui.studio.brief.loading'));
    cleanup();
    swr.isLoading = false;
    swr.error = new Error('nope');
    const failed = render(<BriefPanel sessionId="s1" />);
    expect(failed.getByTestId('studio-brief-load-failed').textContent).toBe(text('ui.studio.brief.load_failed'));
  });

  it('is absent without a session', () => {
    const view = render(<BriefPanel sessionId={null} />);
    expect(view.container.innerHTML).toBe('');
  });
});
