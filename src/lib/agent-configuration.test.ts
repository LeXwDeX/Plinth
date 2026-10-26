import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from './product-schema';
import { AgentConfigurationError, createAgentConfigurationClient, resolveSessionSkill, type AgentConfiguration, type AgentEffectiveConfiguration } from './agent-configuration';
import { AgentConfigurationResource, sessionSkillId } from './agent-configuration-resource';
import type { ChatSkill } from './chat/browser-client';

const schema = structuredClone(defaultCatalog.application) as unknown as ProductSchema;
const configuration: AgentConfiguration = { system_prompt: 'edited prompt', domain_context: { image: '2D' }, skills: [], enabled_tool_names: ['observe_task'], enabled_mcp_preset_ids: [] };
function response(body: unknown, status = 200) { return { ok: status < 400, status, json: async () => body } as Response; }
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { resolve, reject, promise }; }
const flush = async () => { await Promise.resolve(); await Promise.resolve(); };

describe('agent configuration browser contracts', () => {
  it('uses every Schema route and preserves exact revision gates for publish, restore and reset', async () => {
    const calls: Array<{ route: string; init?: RequestInit }> = [];
    const fetcher = vi.fn(async (route, init) => { calls.push({ route: String(route), init }); return response({ valid: true, revision: 'next' }); }) as typeof fetch;
    const client = createAgentConfigurationClient(schema, fetcher);
    await client.get(); await client.validate(configuration); await client.publish('base', configuration); await client.restore('base', 'past'); await client.reset('base');
    expect(calls.map(call => call.route)).toEqual(['admin', 'validate', 'publish', 'restore', 'reset'].map(key => schema.agent_configuration.routes[key as keyof typeof schema.agent_configuration.routes]));
    expect(JSON.parse(String(calls[2].init?.body))).toEqual({ expected_revision: 'base', configuration });
    expect(JSON.parse(String(calls[3].init?.body))).toEqual({ expected_revision: 'base', target_revision: 'past' });
    expect(JSON.parse(String(calls[4].init?.body))).toEqual({ expected_revision: 'base' });
    expect(calls[0].init?.method).toBe('GET');
  });
  it('scopes effective capabilities by domain, model and session and carries abort cleanup', async () => {
    const fetcher = vi.fn(async () => response({})); const controller = new AbortController();
    await createAgentConfigurationClient(schema, fetcher).effective('model-3d', 'model & one', 'session/a', controller.signal);
    const args = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    const url = new URL(args[0], 'http://localhost');
    expect(url.pathname).toBe(schema.agent_configuration.routes.effective);
    expect(Object.fromEntries(url.searchParams)).toEqual({ creative_domain: 'model-3d', model_id: 'model & one', session_id: 'session/a' });
    expect(args[1].signal).toBe(controller.signal);
  });
  it('rejects off-origin configured routes before fetching', async () => {
    const changed = structuredClone(schema); changed.agent_configuration.routes.admin = 'https://external.invalid/admin';
    const fetcher = vi.fn();
    await expect(createAgentConfigurationClient(changed, fetcher).get()).rejects.toBeInstanceOf(AgentConfigurationError);
    expect(fetcher).not.toHaveBeenCalled();
  });
  it('exposes only a safe error key and conflict status to the page', async () => {
    const client = createAgentConfigurationClient(schema, vi.fn(async () => response({ detail: 'error.agent_configuration_conflict', raw: 'private-data' }, 409)));
    await expect(client.publish('stale', configuration)).rejects.toMatchObject({ detail: 'error.agent_configuration_conflict', status: 409 });
    await expect(client.get()).rejects.toBeInstanceOf(AgentConfigurationError);
  });
});

describe('session Skill and asynchronous capability isolation', () => {
  const skill = { id: 'skill-a', name: 'a', displayName: 'a' } as ChatSkill;
  const effective = { skills: [{ id: 'skill-a' }] } as AgentEffectiveConfiguration;
  it('retains distinct selections and clears only the selected session', () => {
    const selections = { a: 'skill-a', b: 'skill-b' };
    expect(sessionSkillId(selections, 'b')).toBe('skill-b');
    expect(sessionSkillId({ ...selections, a: null }, 'b')).toBe('skill-b');
    expect(sessionSkillId(selections, null)).toBeNull();
  });
  it('keeps stale or incompatible selection explicit, without substituting a different Skill', () => {
    expect(resolveSessionSkill('skill-a', [skill], effective)).toEqual({ skill, unavailable: false });
    expect(resolveSessionSkill('missing', [skill], effective)).toEqual({ skill: null, unavailable: true });
    expect(resolveSessionSkill('skill-a', [skill], { skills: [] } as unknown as AgentEffectiveConfiguration)).toEqual({ skill, unavailable: true });
    expect(resolveSessionSkill(null, null, null)).toEqual({ skill: null, unavailable: false });
  });
  it('a new session renders empty and late prior-session capabilities cannot replace it', async () => {
    const oldRequest = deferred<string>(), newRequest = deferred<string>(); let oldSignal: AbortSignal | null = null;
    const old = new AgentConfigurationResource(signal => { oldSignal = signal; return oldRequest.promise; });
    const next = new AgentConfigurationResource(() => newRequest.promise);
    old.start(); old.stop(); next.start();
    expect(next.getSnapshot().data).toBeNull();
    oldRequest.resolve('A'); await flush();
    expect(oldSignal!.aborted).toBe(true); expect(old.getSnapshot().data).toBeNull(); expect(next.getSnapshot().data).toBeNull();
    newRequest.resolve('B'); await flush(); expect(next.getSnapshot().data).toBe('B'); next.stop();
  });
  it('ignores a late request from an aborted lifetime during StrictMode restart', async () => {
    const first = deferred<string>(), second = deferred<string>(); let count = 0;
    const resource = new AgentConfigurationResource(() => ++count === 1 ? first.promise : second.promise);
    resource.start(); resource.stop(); resource.start(); second.resolve('new'); await flush(); first.resolve('old'); await flush();
    expect(resource.getSnapshot()).toEqual({ data: 'new', failed: false, loading: false }); resource.stop();
  });
  it('shows current load failures but ignores aborted errors', async () => {
    const request = deferred<string>(); const resource = new AgentConfigurationResource(() => request.promise); resource.start();
    request.reject(new Error('private transport error')); await flush();
    expect(resource.getSnapshot()).toEqual({ data: null, failed: true, loading: false }); resource.stop();
  });
});
