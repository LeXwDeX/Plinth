import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const providerTypes = readFileSync(new URL('./types.ts', import.meta.url), 'utf8');
const adminUtils = readFileSync(new URL('../admin-utils.ts', import.meta.url), 'utf8');
const processingPanel = readFileSync(
  new URL('../../hooks/use-model-processing-capabilities.ts', import.meta.url),
  'utf8',
);

describe('capability domain frontend schema boundary', () => {
  it('keeps capability domain truth in the public schema projection only', () => {
    expect(providerTypes).not.toContain('CAPABILITY_DOMAIN_MAP');
    expect(providerTypes).not.toContain('CapabilityDomain');
    expect(providerTypes).not.toContain('CapabilityType');
    expect(adminUtils).not.toContain('TASK_3D_TYPES');
    expect(processingPanel).toContain('schema.capabilities');
    expect(processingPanel).toContain("capability.domain === 'processing'");
  });
});
