import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

/**
 * #699: a transparent picture shown on a dark surface looked black, so a transparent result read as "the model
 * changed the background". Every place that shows a result picture puts the same checkerboard under it; an opaque
 * picture covers it entirely.
 */
describe('transparent pictures show a checkerboard under them (#699)', () => {
  it('defines one checkerboard utility from the design tokens', () => {
    const css = read('../../app/globals.css');
    expect(css).toMatch(/@utility bg-checkerboard \{[^}]*repeating-conic-gradient\(var\(--muted\) 0% 25%, var\(--panel\) 0% 50%\)/);
  });

  it.each([
    ['./version-strip.tsx', 2],
    ['./object-thumb.tsx', 1],
    ['./all-outputs.tsx', 1],
    ['./stage-image-view.tsx', 1],
    ['./lightbox.tsx', 1],
    ['../result/result-detail.tsx', 1],
    ['../chat/result-card.tsx', 3],
  ])('%s puts it under its pictures', (path, count) => {
    const source = read(path);
    const pictures = [...source.matchAll(/<img\b[\s\S]*?\/>/g)].map((match) => match[0]);
    expect(pictures.filter((tag) => /[\s"']bg-checkerboard[\s"']/.test(tag)).length).toBeGreaterThanOrEqual(count);
  });
});
