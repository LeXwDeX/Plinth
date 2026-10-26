import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const pageSource = readFileSync(
  new URL('../../app/admin/users/page.tsx', import.meta.url),
  'utf8',
);

describe('Administration users frontend Schema boundary', () => {
  it('delegates public HTTP behavior to the Browser Client', () => {
    expect(pageSource).toContain('createAdministrationUsersBrowserClient');
    expect(pageSource).toContain('useProductSchema');
    expect(pageSource).not.toMatch(/['"`]\/api\//);
    expect(pageSource).not.toMatch(/\bfetch\s*\(/);
  });

  it('renders visible copy and limits from Product Schema', () => {
    expect(pageSource).toContain('schema.administration.users.presentation');
    expect(pageSource).toContain('password_minimum_characters');
    expect(pageSource).not.toMatch(/[\u3400-\u9fff]/u);
  });
});
