// @vitest-environment jsdom
// #152: personal Skill rows offer a separate edit action that opens the editor. #405: the picker is the Skills tab of the
// settings dialog, so there is no popover to close first: the editor is a dialog above it, and the tab stays where it is.
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SkillPicker } from './skill-picker';

vi.mock('@/components/product-schema-provider', async () => {
  const catalog = await import('../../../internal/catalog/default_catalog.json');
  return { useProductSchema: () => ({ schema: catalog.default.application }) };
});
vi.mock('@/components/workspace/skill-creator', () => ({
  SkillCreator: (props: { editing?: { id: string } | null; control?: { open: boolean } }) => (
    <div data-testid="creator" data-editing={props.editing?.id ?? ''} data-open={String(props.control?.open)} />
  ),
}));

const catalog = {
  revision: 'r',
  skills: [
    { id: 'three-view', displayName: '三视图', name: '三视图', prompt: 'views', recommendedSize: '1024' },
    { id: 'personal-a', displayName: '我的水彩', name: 'My watercolor', prompt: 'soft', recommendedSize: '1536x1024' },
  ],
} as never;
const effective = { skills: [{ id: 'three-view' }, { id: 'personal-a' }] } as never;

describe('personal Skill edit action (#152)', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('shows edit only on the personal row and prefills the editor, leaving the Skills tab in place', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ skills: [{ id: 'personal-a' }] }), { status: 200 }))));
    render(<SkillPicker sessionId="s" catalog={catalog} effective={effective} selectedId={null}
      unavailable={false} loading={false} failed={false} disabled={false}
      onSelect={() => undefined} onCreated={() => undefined} onUpdated={() => undefined} onDeleted={() => undefined} />);
    await waitFor(() => expect(screen.getByTestId('skill-edit')).toBeTruthy());
    expect(screen.getAllByTestId('skill-edit')).toHaveLength(1);
    fireEvent.click(screen.getByTestId('skill-edit'));
    // Nothing is closed: the tab is still there under the editor.
    expect(screen.getByTestId('workspace-skill-picker-panel')).toBeTruthy();
    const editors = screen.getAllByTestId('creator');
    expect(editors).toHaveLength(2);
    expect(editors[1].getAttribute('data-editing')).toBe('personal-a');
    expect(editors[1].getAttribute('data-open')).toBe('true');
  });

  it('keeps edit available when the current model cannot use the Skill', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ skills: [{ id: 'personal-a' }] }), { status: 200 }))));
    render(<SkillPicker sessionId="s" catalog={catalog} effective={{ skills: [] } as never} selectedId={null}
      unavailable={false} loading={false} failed={false} disabled={false}
      onSelect={() => undefined} onCreated={() => undefined} onUpdated={() => undefined} onDeleted={() => undefined} />);
    const row = await screen.findByRole('menuitemradio', { name: /我的水彩/ });
    expect(row.getAttribute('aria-disabled')).toBe('true');
    const editButton = await screen.findByTestId('skill-edit');
    fireEvent.click(editButton);
    await waitFor(() => expect(screen.getAllByTestId('creator')[1].getAttribute('data-open')).toBe('true'));
  });
});