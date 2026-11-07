// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PersonalSkillNotFound } from '@/lib/personal-skills';
import { SkillCreator } from './skill-creator';

vi.mock('@/components/product-schema-provider', async () => {
  const catalog = await import('../../../internal/catalog/default_catalog.json');
  return { useProductSchema: () => ({ schema: catalog.default.application }) };
});
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
const updatePersonalSkill = vi.fn();
const createPersonalSkill = vi.fn();
vi.mock('@/lib/personal-skills', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/personal-skills')>();
  return {
    ...actual,
    createPersonalSkill: (...args: unknown[]) => createPersonalSkill(...args),
    updatePersonalSkill: (...args: unknown[]) => updatePersonalSkill(...args),
  };
});

const messages = defaultCatalog.application.content.messages;

function renderEditor(editing = { id: 'personal-a', name: 'Old name', prompt: 'old prompt', recommendedSize: '1536x1024' }) {
  const onOpenChange = vi.fn();
  const onUpdated = vi.fn();
  const onMissing = vi.fn();
  render(<SkillCreator
    sessionId="session-1"
    disabled={false}
    editing={editing}
    onUpdated={onUpdated}
    onMissing={onMissing}
    control={{ open: true, onOpenChange }}
  />);
  return { onOpenChange, onUpdated, onMissing };
}

describe('SkillCreator edit mode (#152)', () => {
  afterEach(() => { vi.clearAllMocks(); });

  it('prefills the current name and prompt and updates through PUT without creating', async () => {
    updatePersonalSkill.mockResolvedValue({ id: 'personal-a', name: 'New name', prompt: 'new prompt' });
    const { onOpenChange, onUpdated } = renderEditor();
    const name = screen.getByLabelText(messages['ui.workspace.skills.name']);
    const prompt = screen.getByLabelText(messages['ui.workspace.skills.prompt']);
    expect((name as HTMLInputElement).value).toBe('Old name');
    expect((prompt as HTMLTextAreaElement).value).toBe('old prompt');
    fireEvent.change(name, { target: { value: 'New name' } });
    fireEvent.change(prompt, { target: { value: 'new prompt' } });
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.save_changes'] }));
    await waitFor(() => expect(onUpdated).toHaveBeenCalledWith({ id: 'personal-a', name: 'New name', prompt: 'new prompt' }));
    expect(updatePersonalSkill.mock.calls[0][1]).toBe('personal-a');
    expect(updatePersonalSkill.mock.calls[0][2]).toMatchObject({ name: 'New name', prompt: 'new prompt', recommended_size: '1536x1024' });
    expect(createPersonalSkill).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('keeps the input and shows save_failed when the update fails', async () => {
    updatePersonalSkill.mockRejectedValue(new Error('HTTP 500'));
    const { onOpenChange } = renderEditor();
    const name = screen.getByLabelText(messages['ui.workspace.skills.name']);
    fireEvent.change(name, { target: { value: 'Kept input' } });
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.save_changes'] }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(messages['ui.workspace.skills.save_failed']));
    expect((name as HTMLInputElement).value).toBe('Kept input');
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('shows the generic not-found message and refreshes when the Skill vanished', async () => {
    updatePersonalSkill.mockRejectedValue(new PersonalSkillNotFound('HTTP 404'));
    const { onMissing } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.save_changes'] }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(messages['ui.workspace.skills.not_found']));
    expect(onMissing).toHaveBeenCalled();
  });

  it('asks before discarding unsaved changes and can stay in the form', async () => {
    const { onOpenChange } = renderEditor();
    fireEvent.change(screen.getByLabelText(messages['ui.workspace.skills.name']), { target: { value: 'Changed' } });
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.cancel'] }));
    await waitFor(() => expect(screen.getByText(messages['ui.workspace.skills.unsaved'])).toBeTruthy());
    expect(onOpenChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.keep_editing'] }));
    expect(screen.queryByText(messages['ui.workspace.skills.unsaved'])).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.cancel'] }));
    fireEvent.click(await screen.findByTestId('skill-unsaved-discard'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('closes without a prompt when nothing changed', () => {
    const { onOpenChange } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: messages['ui.workspace.skills.cancel'] }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(screen.queryByText(messages['ui.workspace.skills.unsaved'])).toBeNull();
  });
});