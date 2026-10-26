import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from './product-schema';
import { createLLMSettingsClient, LLMSettingsError, toggleLevel, type LLMConnectionView, type LLMSettings } from './llm-settings';

const schema = structuredClone(defaultCatalog.application) as unknown as ProductSchema;
function response(body: unknown, status = 200) { return { ok: status < 400, status, json: async () => body } as Response; }
const settings: LLMSettings = { default_model: 'LLM', models: { LLM: { enabled: true, effort_levels: ['low'], default_effort: 'low' } } };

describe('LLM settings browser contract (#322)', () => {
  it('uses every Schema route and sends the revision each change is based on', async () => {
    const calls: Array<{ route: string; init?: RequestInit }> = [];
    const fetcher = vi.fn(async (route, init) => { calls.push({ route: String(route), init }); return response({ revision: '1' }); }) as typeof fetch;
    const client = createLLMSettingsClient(schema, fetcher);
    await client.get(); await client.publish('3', settings); await client.restore('4', '2'); await client.reset('5'); await client.test('GLM');
    const routes = schema.llm_settings!.routes;
    expect(calls.map((call) => call.route)).toEqual([routes.admin, routes.publish, routes.restore, routes.reset, routes.test]);
    expect(calls[0].init?.method).toBe('GET');
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ expected_revision: '3', settings });
    expect(JSON.parse(String(calls[2].init?.body))).toEqual({ expected_revision: '4', target_revision: '2' });
    expect(JSON.parse(String(calls[3].init?.body))).toEqual({ expected_revision: '5' });
    expect(JSON.parse(String(calls[4].init?.body))).toEqual({ connection_id: 'GLM' });
  });

  it('carries the server detail and status of a refusal, and never fetches an off-origin route', async () => {
    const refused = vi.fn(async () => response({ detail: 'conflict text' }, 409)) as typeof fetch;
    await expect(createLLMSettingsClient(schema, refused).publish('0', settings)).rejects.toMatchObject({ detail: 'conflict text', status: 409 });
    const changed = structuredClone(schema); changed.llm_settings!.routes.admin = 'https://external.invalid/admin';
    const fetcher = vi.fn();
    await expect(createLLMSettingsClient(changed, fetcher).get()).rejects.toBeInstanceOf(LLMSettingsError);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('keeps the profile order and a valid default level when a level is toggled', () => {
    const connection = { profile_levels: ['low', 'high', 'max'], profile_default: 'low' } as LLMConnectionView;
    const base = { enabled: true, effort_levels: ['low', 'max'], default_effort: 'low' };
    expect(toggleLevel(connection, base, 'high', true)).toEqual({ enabled: true, effort_levels: ['low', 'high', 'max'], default_effort: 'low' });
    expect(toggleLevel(connection, base, 'low', false)).toEqual({ enabled: true, effort_levels: ['max'], default_effort: 'max' });
  });
});
