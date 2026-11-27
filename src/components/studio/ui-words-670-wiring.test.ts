import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// #670: the studio's words come from the Schema. These spots are checked in
// their source because each sits deep inside a component that needs a WebGL
// viewer, a canvas or a whole chat column to render.
function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

describe('studio words wiring (#670)', () => {
  it('groups the picture pixel limit by the locale, never a bare large number', () => {
    const column = source('./chat-column.tsx');
    expect(column).toContain('pixels: groupedCount(schema.runtime.frontend.image_upload.maximum_pixels, activeLocale(schema)),');
    expect(column).not.toContain('pixels: schema.runtime.frontend.image_upload.maximum_pixels,');
  });

  it('shows the plain loading text while the canvas loads, not the parameter panel words', () => {
    const stage = source('./stage.tsx');
    expect(stage).not.toContain("'ui.workspace.harness.loading'");
    expect(stage).toContain("{schemaText(schema, 'ui.loading')}</div>}>");
  });

  it('writes a size as the Schema width × height text, never as `WxH`', () => {
    // object-details still keys its aspect-ratio lookup by `WxH`; only the shown text changes.
    expect(source('./object-details.tsx')).not.toContain('{`${size.width}x${size.height}${ratio');
    expect(source('./stage-3d.tsx')).not.toContain('`${textureSize.width}x${textureSize.height}`');
    for (const file of ['./object-details.tsx', './stage-3d.tsx']) {
      expect(source(file), file).toContain("'ui.studio.composer.attachment.pixels'");
    }
  });

  it('writes the lightbox zoom with the Schema percentage suffix', () => {
    const lightbox = source('./lightbox.tsx');
    expect(lightbox).not.toContain('{percent}%');
    expect(lightbox).toContain('{percent}{schema.runtime.frontend.percentage_suffix}');
  });

  it('joins a label and its value with the Schema words, never an ASCII colon', () => {
    for (const file of ['./approval-menu.tsx', './context-meter.tsx', './params-popover.tsx', './approval-card.tsx']) {
      const text = source(file);
      // `${label}: ${value}` in a template, or `{label}: {value}` in JSX.
      expect(text, file).not.toMatch(/\$\{[^}]+\}: \$\{/);
      expect(text, file).not.toMatch(/\}: \{(?:effective|formatNativePrice)/);
      expect(text, file).toContain("'ui.common.label_value'");
    }
  });
});
