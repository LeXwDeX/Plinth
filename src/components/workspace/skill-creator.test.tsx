// @vitest-environment jsdom
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SkillCreator } from './skill-creator';

vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: {
  chat: { skills: { creation: { route: '/skills', item_route: '/skills/{skillID}', max_name_characters: 100, recommended_size: '1536x1024' } } },
  agent_configuration: { limits: { max_skill_prompt_characters: 20000 } },
} }) }));
vi.mock('@/lib/product-schema', () => ({ schemaText: (_schema: unknown, key: string) => key.split('.').pop() }));
vi.mock('@/lib/workspace/session-scope', () => ({ captureSessionScope: () => ({}), isCurrentSessionScope: () => true }));
vi.mock('sonner', () => ({ toast: { success: vi.fn() } }));
vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({ open, children }: { open: boolean; children: React.ReactNode }) => open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
  DialogDescription: ({ children }: { children: React.ReactNode }) => <p>{children}</p>,
}));

const skill = { id: 'personal-a', name: 'Original', displayName: 'Original', prompt: 'old prompt', category: 'anime', recommendedSize: '1536x1024', thumbnailPath: '', rawImageURL: '' };
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function setup() {
  const onCreated = vi.fn();
  const onUpdated = vi.fn();
  const onGone = vi.fn();
  const onOpenChange = vi.fn();
  render(<SkillCreator sessionId="session" disabled={false} editing={skill} onCreated={onCreated} onUpdated={onUpdated} onGone={onGone} control={{ open: true, onOpenChange }} />);
  return { onCreated, onUpdated, onGone, onOpenChange };
}

describe('editing a personal Skill', () => {
  it('loads values and updates without selecting the Skill', async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ skill: { id: skill.id } }) });
    vi.stubGlobal('fetch', fetcher);
    const callbacks = setup();
    expect(screen.getByLabelText('name')).toHaveProperty('value', 'Original');
    expect(screen.getByLabelText('prompt')).toHaveProperty('value', 'old prompt');
    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'Changed' } });
    fireEvent.click(screen.getByRole('button', { name: 'edit_save' }));
    await waitFor(() => expect(callbacks.onUpdated).toHaveBeenCalledOnce());
    expect(callbacks.onCreated).not.toHaveBeenCalled();
    expect(fetcher).toHaveBeenCalledWith('/skills/personal-a', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ name: 'Changed', prompt: 'old prompt', category: 'anime', recommended_size: '1536x1024' }) }));
  });

  it.each([500, 404])('keeps input on HTTP %s and refreshes only on 404', async (status) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status }));
    const callbacks = setup();
    fireEvent.change(screen.getByLabelText('prompt'), { target: { value: 'unsaved' } });
    fireEvent.click(screen.getByRole('button', { name: 'edit_save' }));
    expect((await screen.findByRole('alert')).textContent).toBe(status === 404 ? 'gone' : 'save_failed');
    expect(screen.getByLabelText('prompt')).toHaveProperty('value', 'unsaved');
    expect(callbacks.onGone).toHaveBeenCalledTimes(status === 404 ? 1 : 0);
    expect(callbacks.onOpenChange).not.toHaveBeenCalled();
  });

  it('confirms before discarding changed input', () => {
    const confirm = vi.fn().mockReturnValueOnce(false).mockReturnValueOnce(true);
    vi.stubGlobal('confirm', confirm);
    const callbacks = setup();
    fireEvent.change(screen.getByLabelText('name'), { target: { value: 'Changed' } });
    fireEvent.click(screen.getByRole('button', { name: 'cancel' }));
    expect(callbacks.onOpenChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'cancel' }));
    expect(confirm).toHaveBeenCalledWith('discard_confirm');
    expect(callbacks.onOpenChange).toHaveBeenCalledWith(false);
  });
});
