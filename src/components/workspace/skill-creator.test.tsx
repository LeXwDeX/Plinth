// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PersonalSkillInvalid, PersonalSkillNotFound, SkillDraftQuotaExhausted } from '@/lib/personal-skills';
import { SkillCreator, type SkillEditTarget } from './skill-creator';

vi.mock('@/components/product-schema-provider', async () => {
  const catalog = await import('../../../internal/catalog/default_catalog.json');
  return { useProductSchema: () => ({ schema: catalog.default.application }) };
});
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
const patchPersonalSkill = vi.fn();
const createPersonalSkill = vi.fn();
const draftPersonalSkill = vi.fn();
vi.mock('@/lib/personal-skills', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/personal-skills')>();
  return {
    ...actual,
    createPersonalSkill: (...args: unknown[]) => createPersonalSkill(...args),
    patchPersonalSkill: (...args: unknown[]) => patchPersonalSkill(...args),
    draftPersonalSkill: (...args: unknown[]) => draftPersonalSkill(...args),
  };
});

const messages = defaultCatalog.application.content.messages;
const label = (key: string) => messages[`ui.workspace.skills.${key}` as keyof typeof messages];

const target: SkillEditTarget = {
  id: 'personal-a', name: 'Old name', prompt: 'old prompt', recommendedSize: '1536x1024',
  slug: 'old-slug', description: '派生的说明', descriptionDerived: true,
};

function renderEditor(editing: SkillEditTarget = target) {
  const onOpenChange = vi.fn();
  const onUpdated = vi.fn();
  const onMissing = vi.fn();
  render(<SkillCreator sessionId="session-1" disabled={false} editing={editing}
    onUpdated={onUpdated} onMissing={onMissing} control={{ open: true, onOpenChange }} />);
  return { onOpenChange, onUpdated, onMissing };
}

function renderCreator() {
  const onOpenChange = vi.fn();
  const onCreated = vi.fn();
  render(<SkillCreator sessionId="session-1" disabled={false} onCreated={onCreated} control={{ open: true, onOpenChange }} />);
  return { onOpenChange, onCreated };
}

describe('SkillCreator edit mode (#548: each field saved on its own)', () => {
  afterEach(() => { vi.clearAllMocks(); });

  it('prefills every field, marks a derived description and saves one field through PATCH', async () => {
    patchPersonalSkill.mockResolvedValue({ id: 'personal-a', name: 'Old name', prompt: 'old prompt', slug: 'new-slug', description: '派生的说明' });
    const { onOpenChange, onUpdated } = renderEditor();
    expect((screen.getByLabelText(label('name')) as HTMLInputElement).value).toBe('Old name');
    expect((screen.getByLabelText(label('body')) as HTMLTextAreaElement).value).toBe('old prompt');
    expect((screen.getByLabelText(label('description')) as HTMLTextAreaElement).value).toBe('派生的说明');
    expect(screen.getByText(new RegExp(label('description_derived')))).toBeTruthy();
    fireEvent.change(screen.getByLabelText(label('slug')), { target: { value: 'new-slug' } });
    fireEvent.click(screen.getByTestId('skill-field-save-slug'));
    await waitFor(() => expect(onUpdated).toHaveBeenCalled());
    expect(patchPersonalSkill.mock.calls[0][1]).toBe('personal-a');
    expect(patchPersonalSkill.mock.calls[0][2]).toEqual({ slug: 'new-slug' });
    expect(screen.getByTestId('skill-field-saved-slug')).toBeTruthy();
    expect(createPersonalSkill).not.toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('shows a refused field next to that field and keeps the input', async () => {
    patchPersonalSkill.mockRejectedValue(new PersonalSkillInvalid('HTTP 400', 'description'));
    renderEditor();
    const description = screen.getByLabelText(label('description'));
    fireEvent.change(description, { target: { value: 'two\nlines' } });
    fireEvent.click(screen.getByTestId('skill-field-save-description'));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(label('description_invalid')));
    expect((description as HTMLTextAreaElement).value).toBe('two\nlines');
  });

  it('shows the generic not-found message and refreshes when the Skill vanished', async () => {
    patchPersonalSkill.mockRejectedValue(new PersonalSkillNotFound('HTTP 404'));
    const { onMissing } = renderEditor();
    fireEvent.change(screen.getByLabelText(label('name')), { target: { value: 'New' } });
    fireEvent.click(screen.getByTestId('skill-field-save-name'));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(label('not_found')));
    expect(onMissing).toHaveBeenCalled();
  });

  it('asks before discarding unsaved changes and can stay in the form', async () => {
    const { onOpenChange } = renderEditor();
    fireEvent.change(screen.getByLabelText(label('name')), { target: { value: 'Changed' } });
    fireEvent.click(screen.getByRole('button', { name: label('close') }));
    await waitFor(() => expect(screen.getByText(label('unsaved'))).toBeTruthy());
    expect(onOpenChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: label('keep_editing') }));
    expect(screen.queryByText(label('unsaved'))).toBeNull();
    // 按钮常驻：真实点击时目标不能被自己的 click 事件卸载，否则对话框会被外部点击检测关掉。
    expect(screen.getByTestId('skill-unsaved-keep-editing')).toBeTruthy();
    expect(screen.getByTestId('skill-edit-actions').className).not.toContain('hidden');
    fireEvent.click(screen.getByRole('button', { name: label('close') }));
    fireEvent.click(await screen.findByTestId('skill-unsaved-discard'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('closes without a prompt when nothing changed', () => {
    const { onOpenChange } = renderEditor();
    fireEvent.click(screen.getByRole('button', { name: label('close') }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(screen.queryByText(label('unsaved'))).toBeNull();
  });
});

describe('SkillCreator create mode (#548: name and requirement, then review)', () => {
  afterEach(() => { vi.clearAllMocks(); });

  it('drafts from the name and requirement, lets every field change, and saves only on save', async () => {
    draftPersonalSkill.mockResolvedValue({ slug: 'soft-watercolor', description: '画角色时使用。', prompt: '柔和水彩。', apply: 'generation-prompt' });
    createPersonalSkill.mockResolvedValue({ id: 'personal-new' });
    const { onCreated, onOpenChange } = renderCreator();
    fireEvent.change(screen.getByLabelText(label('name')), { target: { value: '水彩角色' } });
    fireEvent.change(screen.getByLabelText(label('requirement')), { target: { value: '角色设定图用柔和水彩' } });
    fireEvent.click(screen.getByTestId('skill-generate'));
    await waitFor(() => expect((screen.getByLabelText(label('slug')) as HTMLInputElement).value).toBe('soft-watercolor'));
    expect(draftPersonalSkill.mock.calls[0][1]).toEqual({ name: '水彩角色', requirement: '角色设定图用柔和水彩', session_id: 'session-1' });
    expect(createPersonalSkill).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText(label('description')), { target: { value: '画角色设定图时使用。' } });
    fireEvent.change(screen.getByLabelText(label('apply')), { target: { value: 'instruction' } });
    fireEvent.click(screen.getByRole('button', { name: label('save') }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('personal-new'));
    expect(createPersonalSkill.mock.calls[0][1]).toMatchObject({
      name: '水彩角色', slug: 'soft-watercolor', description: '画角色设定图时使用。', prompt: '柔和水彩。', apply: 'instruction',
    });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('says the chat model quota is used up instead of a bare failure (#619)', async () => {
    draftPersonalSkill.mockRejectedValue(new SkillDraftQuotaExhausted('HTTP 503'));
    renderCreator();
    fireEvent.change(screen.getByLabelText(label('name')), { target: { value: '水墨' } });
    fireEvent.change(screen.getByLabelText(label('requirement')), { target: { value: '用水墨' } });
    fireEvent.click(screen.getByTestId('skill-generate'));
    expect(label('draft_quota_exhausted')).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(label('draft_quota_exhausted')));
  });

  it('says once that drafting failed and keeps the write-it-myself way', async () => {
    draftPersonalSkill.mockRejectedValue(new Error('HTTP 502'));
    renderCreator();
    fireEvent.change(screen.getByLabelText(label('name')), { target: { value: '水彩' } });
    fireEvent.change(screen.getByLabelText(label('requirement')), { target: { value: '用水彩' } });
    fireEvent.click(screen.getByTestId('skill-generate'));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(label('draft_failed')));
    fireEvent.click(screen.getByTestId('skill-write-myself'));
    expect((screen.getByLabelText(label('name')) as HTMLInputElement).value).toBe('水彩');
    expect((screen.getByLabelText(label('body')) as HTMLTextAreaElement).value).toBe('');
    // Nothing is saved without a description.
    fireEvent.change(screen.getByLabelText(label('body')), { target: { value: '柔和水彩。' } });
    fireEvent.click(screen.getByRole('button', { name: label('save') }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain(label('description_invalid')));
    expect(createPersonalSkill).not.toHaveBeenCalled();
  });
});
