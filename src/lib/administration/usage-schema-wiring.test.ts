import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  new URL('../../app/admin/page.tsx', import.meta.url),
  'utf8',
);

describe('administration usage frontend Schema boundary', () => {
  it('delegates routes, query names and wire fields to the Schema client', () => {
    expect(source).toContain('useProductSchema');
    expect(source).toContain('createAdministrationUsageBrowserClient');
    expect(source).toContain('schema.runtime.http.legacy_routes.admin_usage');
    expect(source).toContain(
      'schema.runtime.http.legacy_routes.admin_balance_summary',
    );
    expect(source).not.toMatch(/['"`]\/api\//);
    expect(source).not.toMatch(/\bfetch\s*\(/);
  });

  it('renders every public label and display option from Product Schema', () => {
    expect(source).toContain('schema.administration.usage');
    expect(source).toContain('presentation.quick_ranges');
    expect(source).toContain('presentation.date_time_options');
    expect(source).not.toMatch(/[\u3400-\u9fff]/u);
  });
});
