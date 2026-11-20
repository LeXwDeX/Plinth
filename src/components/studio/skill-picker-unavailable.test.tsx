// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { SkillPicker } from './skill-picker';

vi.mock('@/components/product-schema-provider', async () => {
  const catalog = await import('../../../internal/catalog/default_catalog.json');
  return { useProductSchema: () => ({ schema: catalog.default.application }) };
});
vi.mock('@/components/workspace/skill-creator', () => ({ SkillCreator: () => null }));

describe('unavailable Skill', () => {
  it('shows the Schema reason on the row and the footer without making the row selectable', async () => {
    const onSelect = vi.fn();
    const skill = { id: 'personal', displayName: 'Personal', prompt: 'p', recommendedSize: '' };
    render(<SkillPicker sessionId="s" catalog={{ revision: 'r', skills: [skill] } as never}
      effective={{ skills: [] } as never} selectedId="personal" unavailable loading={false} failed={false}
      disabled={false} onSelect={onSelect} onCreated={() => undefined} supportingModels={{ personal: ['Model A'] }} />);
    // #405: there is no trigger to carry a "!" and the reason any more; the gear says it (composer-settings-405.test.tsx),
    // and the tab keeps the reason on the row and under the table.
    expect(screen.queryByTestId('workspace-skill-picker')).toBeNull();
    const reason = defaultCatalog.application.content.messages['ui.workspace.skills.execution_unavailable'].replace('{models}', 'Model A');
    const row = await screen.findByRole('menuitemradio', { name: `Personal: ${reason}` });
    expect(row.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(row);
    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.getByTestId('skill-picker-unavailable').textContent).toContain(defaultCatalog.application.content.messages['ui.workspace.skills.unavailable']);
    expect(screen.getAllByText(reason).some((element) => element.tagName === 'P')).toBe(true);
  });

  it('declares the execution template in both Schema locales', () => {
    expect(defaultCatalog.application.content.messages['ui.workspace.skills.execution_unavailable']).toContain('{models}');
    expect(defaultCatalog.application.localization.bundles.en.messages['ui.workspace.skills.execution_unavailable']).toContain('{models}');
  });
});
