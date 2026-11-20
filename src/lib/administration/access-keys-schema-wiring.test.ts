import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const pageSource = readFileSync(
  new URL('../../app/admin/access-keys/page.tsx', import.meta.url),
  'utf8',
);
const routerSource = readFileSync(
  new URL('../../../web/src/router.tsx', import.meta.url),
  'utf8',
);
const layoutSource = readFileSync(
  new URL('../../app/admin/layout.tsx', import.meta.url),
  'utf8',
);

describe('Administration access keys frontend Schema boundary', () => {
  it('delegates public HTTP behavior to the Browser Client', () => {
    expect(pageSource).toContain('createAdministrationAccessKeysBrowserClient');
    expect(pageSource).toContain('useProductSchema');
    expect(pageSource).toContain('schema.administration.access_keys');
    expect(pageSource).not.toMatch(/['"`]\/api\//);
    expect(pageSource).not.toMatch(/\bfetch\s*\(/);
  });

  it('renders visible copy, limits and routes from Product Schema', () => {
    expect(pageSource).toContain('accessKeysSchema.presentation');
    expect(pageSource).toContain('search_max_length');
    expect(pageSource).toContain('admin_access_keys_switch');
    expect(pageSource).toContain('withErrorReference');
    expect(pageSource).not.toMatch(/[\u3400-\u9fff]/u);
  });

  it('exposes list, revoke and the overall switch through the Browser Client', () => {
    expect(pageSource).toContain('client.list(');
    expect(pageSource).toContain('client.revoke(');
    expect(pageSource).toContain('client.setEnabled(');
  });

  it('mounts the page on the administration route the Catalog navigates to', () => {
    expect(routerSource).toContain(
      `'access-keys': lazy(() => import('@/app/admin/access-keys/page'))`,
    );
    expect(layoutSource).toContain('KeyRound');
  });
});