import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  agentScopeOf,
  agentScopeOptions,
  defaultAgentScope,
  pickerDisabledReason,
  scopeAllowsDomain,
} from './agent-scope';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as unknown as ProductSchema;
}

describe('agent scope options', () => {
  it('offers the three modes in the Schema order with Schema text', () => {
    const options = agentScopeOptions(schemaFixture());
    expect(options.map((option) => option.scope)).toEqual(['2d', '3d', 'auto']);
    expect(options.map((option) => option.label)).toEqual(['2D 模式', '3D 模式', '自动模式']);
    for (const option of options) expect(option.description).not.toBe('');
  });

  it('offers only what the Schema declares and ignores modes it does not know', () => {
    const schema = schemaFixture();
    schema.chat.agent_scope.modes = [
      { id: 'auto', workspace_domains: ['image', 'model-3d'] },
      { id: '4d', workspace_domains: [] },
    ];
    expect(agentScopeOptions(schema).map((option) => option.scope)).toEqual(['auto']);
  });

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

describe('which model picker a mode leaves usable', () => {
  it('greys the 3D picker in 2D mode and the 2D picker in 3D mode, never in auto', () => {
    const schema = schemaFixture();
    expect(scopeAllowsDomain(schema, '2d', 'image')).toBe(true);
    expect(scopeAllowsDomain(schema, '2d', 'model-3d')).toBe(false);
    expect(scopeAllowsDomain(schema, '3d', 'image')).toBe(false);
    expect(scopeAllowsDomain(schema, '3d', 'model-3d')).toBe(true);
    expect(scopeAllowsDomain(schema, 'auto', 'image')).toBe(true);
    expect(scopeAllowsDomain(schema, 'auto', 'model-3d')).toBe(true);
  });

  it('restricts nothing for a Schema that declares no scopes', () => {
    const schema = schemaFixture();
    schema.chat.agent_scope = { default_mode: 'auto', modes: [] };
    expect(scopeAllowsDomain(schema, '2d', 'model-3d')).toBe(true);
  });

  it('says why in the tooltip, naming the current mode', () => {
    const schema = schemaFixture();
    expect(pickerDisabledReason(schema, '2d')).toContain('2D 模式');
    expect(pickerDisabledReason(schema, '3d')).toContain('3D 模式');
    expect(pickerDisabledReason(schema, '2d')).not.toContain('{mode}');
  });
});

describe('the Schema ships a label for every mode it offers', () => {
  it('has the ui keys the picker reads', () => {
    const messages = (defaultCatalog.application.content.messages as Record<string, string>);
    const schema = schemaFixture();
    for (const mode of schema.chat.agent_scope.modes) {
      for (const prefix of ['mode_', 'desc_']) expect(messages[`ui.studio.agent_scope.${prefix}${mode.id}`], `${prefix}${mode.id}`).toBeTruthy();
    }
    for (const key of ['label', 'col.mode', 'col.description', 'note', 'change_failed', 'picker_disabled']) {
      expect(messages[`ui.studio.agent_scope.${key}`], key).toBeTruthy();
    }
  });
});
