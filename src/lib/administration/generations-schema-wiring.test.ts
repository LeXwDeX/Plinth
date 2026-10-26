import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  new URL('../../components/admin/generations-panel.tsx', import.meta.url),
  'utf8',
);

describe('Administration generations frontend Schema boundary', () => {
  it('delegates routes and wire translation to Schema browser clients', () => {
    expect(source).toContain('useProductSchema');
    expect(source).toContain(
      'createAdministrationGenerationsBrowserClient',
    );
    expect(source).toContain('createAdministrationUsersBrowserClient');
    expect(source).not.toMatch(/['"`]\/api\//);
    expect(source).not.toMatch(/\bfetch\s*\(/);
  });

  it('renders public copy, capabilities and states from Product Schema', () => {
    expect(source).toContain('schema.administration.generations');
    expect(source).toContain('schema.capabilities');
    expect(source).toContain('schema.task_states');
    expect(source).not.toContain('@/lib/constants');
    expect(source).not.toContain('@/lib/providers/provider-config');
    expect(source).not.toMatch(/[\u3400-\u9fff]/u);
  });
});
