import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  new URL('../../app/admin/user-assets/page.tsx', import.meta.url),
  'utf8',
);

describe('administration user assets frontend Schema boundary', () => {
  it('delegates users and overview HTTP behavior to Schema browser clients', () => {
    expect(source).toContain('createAdministrationUsersBrowserClient');
    expect(source).toContain('createAdministrationUserAssetsBrowserClient');
    expect(source).toContain(
      'schema.runtime.http.legacy_routes.admin_user_assets',
    );
    expect(source).toContain('schema.runtime.http.legacy_routes.asset_item');
    expect(source).not.toMatch(/['"`]\/api\//);
    expect(source).not.toMatch(/\bfetch\s*\(/);
  });

  it('renders copy, states, capabilities and display rules from Schema', () => {
    expect(source).toContain('schema.administration.user_assets');
    expect(source).toContain('schema.task_states');
    expect(source).toContain('schema.capabilities');
    expect(source).not.toContain('@/lib/constants');
    expect(source).not.toContain('TASK_3D_TYPES');
    expect(source).not.toMatch(/[\u3400-\u9fff]/u);
  });
});
