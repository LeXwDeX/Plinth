import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync(new URL('./main-layout.tsx', import.meta.url), 'utf8');

describe('admin/account shell scroll containment', () => {
  it('keeps the shell at viewport height with its own overflow hidden', () => {
    expect(source).toMatch(/className="[^"]*\bh-dvh\b[^"]*\boverflow-hidden\b[^"]*"/);
  });

  it('makes the scrolling <main> the containing block of absolutely positioned descendants', () => {
    // sr-only text (position: absolute) inside an unpositioned scroll container escapes to the
    // initial containing block, stretches the document and lets the whole shell scroll away,
    // exposing the body background below it.
    const main = source.match(/<main className="([^"]*)"/);
    expect(main).not.toBeNull();
    const classes = main![1].split(/\s+/);
    expect(classes).toContain('overflow-y-auto');
    expect(classes).toContain('relative');
  });
});
