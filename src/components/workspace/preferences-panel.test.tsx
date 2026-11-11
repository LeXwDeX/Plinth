// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PreferencesPanel } from './preferences-panel';
import { useSessionPreferences } from '@/hooks/use-session-preferences';
import type { ChatPreference } from '@/lib/chat/preferences';

vi.mock('@/components/product-schema-provider', () => {
  const schema = defaultCatalog.application;
  return { useProductSchema: () => ({ schema, text: (key: string) => schema.content.messages[key as keyof typeof schema.content.messages] ?? '' }) };
});
vi.mock('@/lib/chat/browser-client', () => ({ createChatBrowserClient: () => ({}) }));
vi.mock('@/hooks/use-session-preferences', () => ({ useSessionPreferences: vi.fn() }));

const schema = defaultCatalog.application;
const messages = schema.content.messages;
const preferencesMock = vi.mocked(useSessionPreferences);

const preference = (overrides: Partial<ChatPreference> = {}): ChatPreference => ({
  id: 'p1', kind: 'rejected', source: 'reject', direction: 'a photorealistic face',
  reasonPreset: 'style', reasonText: 'too realistic', handle: '@img2', taskId: 't1', assetId: 'a2',
  alternatives: ['@img1'], explicit: true, active: true, userEdited: false, createdAt: '2026-07-26T10:00:00Z',
  ...overrides,
});

function mockPreferences(overrides: Record<string, unknown> = {}) {
  preferencesMock.mockReturnValue({
    available: true,
    preferences: [preference()],
    loaded: true,
    failed: false,
    reject: vi.fn(async () => true),
    signal: vi.fn(async () => undefined),
    update: vi.fn(async () => true),
    remove: vi.fn(async () => true),
    ...overrides,
  } as unknown as ReturnType<typeof useSessionPreferences>);
}

beforeEach(() => {
  preferencesMock.mockReset();
  mockPreferences();
});
afterEach(cleanup);

function openPanel() {
  render(<PreferencesPanel sessionId="session-1" />);
  fireEvent.click(screen.getByRole('button', { name: new RegExp(messages['ui.workspace.preferences.title']) }));
}

describe('preference summary panel', () => {
  it('shows the recorded direction, handle and reason', () => {
    openPanel();
    expect(screen.getByTestId('preference-item').getAttribute('data-kind')).toBe('rejected');
    expect(screen.getByText('a photorealistic face')).toBeTruthy();
    expect(screen.getByText('@img2')).toBeTruthy();
    expect(screen.getByTestId('preference-reason').textContent).toContain(messages['ui.workspace.preferences.reason.style']);
    expect(screen.getByTestId('preference-reason').textContent).toContain('too realistic');
  });

  it('shows the empty state and the read failure', () => {
    mockPreferences({ preferences: [], loaded: true });
    openPanel();
    expect(screen.getByTestId('preferences-empty').textContent).toBe(messages['ui.workspace.preferences.empty']);
    cleanup();
    mockPreferences({ preferences: [], loaded: true, failed: true });
    openPanel();
    expect(screen.getByRole('alert').textContent).toBe(messages['ui.workspace.preferences.load_failed']);
  });

  it('edits a direction and reason and deletes the record', async () => {
    const update = vi.fn(async () => true);
    const remove = vi.fn(async () => true);
    mockPreferences({ update, remove });
    openPanel();
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.preferences.edit'] }));
    const direction = screen.getByLabelText(messages['ui.workspace.preferences.direction_label']) as HTMLTextAreaElement;
    fireEvent.change(direction, { target: { value: 'an ink-wash fox' } });
    const reason = screen.getByLabelText(messages['ui.workspace.preferences.reason_label']) as HTMLTextAreaElement;
    fireEvent.change(reason, { target: { value: 'washed out' } });
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.preferences.save'] }));
    await waitFor(() => expect(update).toHaveBeenCalledWith('p1', {
      direction: 'an ink-wash fox', reasonPreset: 'style', reasonText: 'washed out',
    }));
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.preferences.delete'] }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith('p1'));
  });

  it('refuses an over-long direction before calling the server', async () => {
    const update = vi.fn(async () => true);
    mockPreferences({ update });
    openPanel();
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.preferences.edit'] }));
    fireEvent.change(screen.getByLabelText(messages['ui.workspace.preferences.direction_label']), {
      target: { value: '设'.repeat(401) },
    });
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.preferences.save'] }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('400'));
    expect(update).not.toHaveBeenCalled();
  });

  it('draws nothing without a session or without the feature in the Schema', () => {
    const { container } = render(<PreferencesPanel sessionId={null} />);
    expect(container.firstChild).toBeNull();
    cleanup();
    mockPreferences({ available: false });
    const disabled = render(<PreferencesPanel sessionId="session-1" />);
    expect(disabled.container.firstChild).toBeNull();
  });
});