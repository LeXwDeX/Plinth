import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { agentScopeOf, defaultAgentScope } from './agent-scope';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as unknown as ProductSchema;
}

describe('agent scope normalization', () => {
  it('defaults to auto, and a value the Schema does not offer is the default', () => {
    const schema = schemaFixture();
    expect(defaultAgentScope(schema)).toBe('auto');
    expect(agentScopeOf(schema, '3d')).toBe('3d');
    expect(agentScopeOf(schema, 'bogus')).toBe('auto');
    expect(agentScopeOf(schema, undefined)).toBe('auto');
    schema.chat.agent_scope.modes = schema.chat.agent_scope.modes.filter((mode) => mode.id !== '2d');
    expect(agentScopeOf(schema, '2d')).toBe('auto');
  });
});
