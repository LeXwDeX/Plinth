// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TablePopover } from './table-popover';

describe('unavailable picker rows', () => {
  it('lets keyboard users inspect the reason but not select the row', async () => {
    const pick = vi.fn();
    render(<TablePopover label="Skills" value="none" sections={[{
      title: 'Skills', head: ['Name'], columns: '1fr', selectedKey: '', onPick: pick,
      rows: [
        { key: 'disabled', cells: [{ text: 'Disabled' }], disabled: true, disabledReason: 'Schema reason', focusableDisabled: true },
        { key: '', cells: [{ text: 'None' }] },
      ],
    }]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Skills: none' }));
    const row = await screen.findByRole('menuitemradio', { name: 'Disabled: Schema reason' });
    expect(row.getAttribute('aria-disabled')).toBe('true');
    row.focus();
    expect(document.activeElement).toBe(row);
    fireEvent.click(row);
    expect(pick).not.toHaveBeenCalled();
    fireEvent.keyDown(row, { key: 'ArrowDown' });
    await waitFor(() => expect(document.activeElement?.textContent).toContain('None'));
  });
});
