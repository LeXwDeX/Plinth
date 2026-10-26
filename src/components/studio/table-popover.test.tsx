import { describe, expect, it } from 'vitest';

import { pickerRowClass } from '@/components/studio/table-popover';

// #236: hovering the selected row must not repaint it like an unselected one.
describe('pickerRowClass', () => {
  it('keeps the selected tint and accent while the row is hovered or focused', () => {
    const classes = pickerRowClass(true).split(/\s+/);
    expect(classes).toContain('bg-primary/15');
    expect(classes).toContain('hover:bg-primary/20');
    expect(classes).toContain('focus-visible:bg-primary/20');
    expect(classes).toContain('ring-1');
    expect(classes).not.toContain('hover:bg-panel-raised');
    expect(classes).not.toContain('focus-visible:bg-panel-raised');
  });

  it('gives unselected rows a neutral hover that differs from the selected tint', () => {
    const classes = pickerRowClass(false).split(/\s+/);
    expect(classes).toContain('hover:bg-panel-raised');
    expect(classes.some((name) => name.includes('primary'))).toBe(false);
  });

  it('finishes its color transition within 150ms', () => {
    expect(pickerRowClass(true)).toMatch(/\bduration-(75|100|150)\b/);
  });
});
