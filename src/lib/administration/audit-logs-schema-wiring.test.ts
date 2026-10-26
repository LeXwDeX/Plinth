import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const source = readFileSync(
  new URL('../../components/admin/audit-log-panel.tsx', import.meta.url),
  'utf8',
);

describe('admin audit log Product Schema boundary', () => {
  it('consumes schema clients without owning routes, wire fields, text, or action styles', () => {
    expect(source).toContain('useProductSchema');
    expect(source).toContain('createAdministrationAuditLogsBrowserClient');
    expect(source).toContain('createAdministrationUsersBrowserClient');
    expect(source).toContain('schema.administration.audit_logs');
    expect(source).toContain('schema.audit_actions');
    expect(source).toContain('presentation.action_badge_classes');
    expect(source).not.toMatch(/['"`]\/api\//);
    expect(source).not.toMatch(/\bfetch\s*\(/);
    expect(source).not.toContain('@/lib/constants');
    expect(source).not.toMatch(/[\u3400-\u9fff]/u);
  });
});
