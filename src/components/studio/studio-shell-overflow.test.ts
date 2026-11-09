import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync(new URL('./studio-shell.tsx', import.meta.url), 'utf8');

describe('studio shell overflow (#210)', () => {
  it('clips instead of hiding, so focus or scrollIntoView cannot slide the workspace up', () => {
    // An absolutely positioned descendant that escaped a scroll container made the hidden shell
    // 1402px tall in a 900px window; the browser then scrolled the shell and the top bar vanished.
    const shell = source.match(/className="([^"]*)" data-testid="studio-shell"/);
    expect(shell).not.toBeNull();
    const classes = shell![1].split(/\s+/);
    expect(classes).toContain('h-dvh');
    expect(classes).toContain('overflow-clip');
    expect(classes).not.toContain('overflow-hidden');
  });
});
