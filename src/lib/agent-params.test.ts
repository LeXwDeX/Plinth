import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  AgentHarnessError,
  consentAwaitingDecision,
  consentDecision,
  createAgentHarnessClient,
  mergeAgentParams,
  projectConsentState,
} from './agent-params';

const schema = structuredClone(defaultCatalog.application) as ProductSchema;
const routes = {
  params: schema.runtime.http.routes.agent_params,
  bind: schema.runtime.http.routes.agent_bind,
  consent: schema.runtime.http.routes.agent_consent,
};

function jsonResponse(payload: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => payload,
  } as unknown as Response;
}

function recordingFetcher(response: Response) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return response;
  });
  return { calls, fetcher: fetcher as unknown as typeof fetch };
}

describe('createAgentHarnessClient', () => {
  it('GETs params scoped to the session and unwraps the params envelope', async () => {
    const { calls, fetcher } = recordingFetcher(
      jsonResponse({ params: { size: '1024x1024', n: 2 } }),
    );
    const client = createAgentHarnessClient({ fetcher, routes });
    await expect(client.getParams('session-1')).resolves.toEqual({
      size: '1024x1024',
      n: 2,
    });
    expect(calls[0].url).toBe(
      `${routes.params}?scope=${encodeURIComponent('session-1')}`,
    );
    expect(calls[0].init?.method ?? 'GET').toBe('GET');
  });

  it('PUTs params and returns the authoritative merged params from the server', async () => {
    const { calls, fetcher } = recordingFetcher(
      jsonResponse({ params: { size: '2048x2048', quality: 'high' } }),
    );
    const client = createAgentHarnessClient({ fetcher, routes });
    const merged = await client.putParams('session-1', { size: '2048x2048' });
    expect(merged).toEqual({ size: '2048x2048', quality: 'high' });
    expect(calls[0].url).toBe(`${routes.params}?scope=session-1`);
    expect(calls[0].init?.method).toBe('PUT');
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      params: { size: '2048x2048' },
    });
  });

  it('isolates identically named values by exact Offering ID in GET and PUT', async () => {
    const { calls, fetcher } = recordingFetcher(jsonResponse({ params: { size: '2K' } }));
    const client = createAgentHarnessClient({ fetcher, routes });
    await client.getParams('session 1', undefined, 'image/model/text');
    await client.putParams('session 1', { size: 300 }, undefined, '3d/model/image');
    expect(calls[0].url).toBe(`${routes.params}?scope=session%201&offering_id=image%2Fmodel%2Ftext`);
    expect(calls[1].url).toBe(`${routes.params}?scope=session%201&offering_id=3d%2Fmodel%2Fimage`);
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ params: { size: 300 } });
  });

  it('surfaces error detail vocabulary keys as AgentHarnessError', async () => {
    const { fetcher } = recordingFetcher(
      jsonResponse(
        { error: 'invalid_scope', detail: 'error.agent_scope_required' },
        false,
        400,
      ),
    );
    const client = createAgentHarnessClient({ fetcher, routes });
    const failure = client.getParams('').catch((error: unknown) => error);
    await expect(failure).resolves.toBeInstanceOf(AgentHarnessError);
    await expect(failure).resolves.toMatchObject({
      status: 400,
      detail: 'error.agent_scope_required',
    });
  });

  it('binds an agent session to a task', async () => {
    const { calls, fetcher } = recordingFetcher(
      jsonResponse({ session: 'session-1', task: 'task-9' }),
    );
    const client = createAgentHarnessClient({ fetcher, routes });
    await client.bindTask('session-1', 'task-9');
    expect(calls[0].url).toBe(routes.bind);
    expect(calls[0].init?.method).toBe('PUT');
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      session: 'session-1',
      task: 'task-9',
    });
  });

  it('reads consent state and submits approval decisions', async () => {
    const { calls, fetcher } = recordingFetcher(
      jsonResponse({ consent: { consent_pending: { action: 'generate' } } }),
    );
    const client = createAgentHarnessClient({ fetcher, routes });
    await expect(client.getConsent('session-1')).resolves.toEqual({
      consent_pending: { action: 'generate' },
    });
    expect(calls[0].url).toBe(
      `${routes.consent}?scope=session-1`,
    );

    await client.putConsent('session-1', 'request-a', true);
    expect(calls[1].url).toBe(
      `${routes.consent}?scope=session-1`,
    );
    expect(calls[1].init?.method).toBe('PUT');
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({
      approved: true,
      request_id: 'request-a',
    });
  });

  it('uses every route provided by the Product Schema fixture', async () => {
    const mutatedSchema = structuredClone(defaultCatalog.application) as ProductSchema;
    mutatedSchema.runtime.http.routes.agent_params = '/schema/agent-params';
    mutatedSchema.runtime.http.routes.agent_bind = '/schema/agent-bind';
    mutatedSchema.runtime.http.routes.agent_consent = '/schema/agent-consent';
    const customRoutes = {
      params: mutatedSchema.runtime.http.routes.agent_params,
      bind: mutatedSchema.runtime.http.routes.agent_bind,
      consent: mutatedSchema.runtime.http.routes.agent_consent,
    };
    const { calls, fetcher } = recordingFetcher(jsonResponse({ params: {}, consent: {} }));
    const client = createAgentHarnessClient({ fetcher, routes: customRoutes });

    await client.getParams('session-x');
    await client.putParams('session-x', { quality: 'high' });
    await client.bindTask('session-x', 'task-x');
    await client.getConsent('session-x');
    await client.putConsent('session-x', 'request-x', false);

    expect(calls.map(({ url }) => url)).toEqual([
      '/schema/agent-params?scope=session-x',
      '/schema/agent-params?scope=session-x',
      '/schema/agent-bind',
      '/schema/agent-consent?scope=session-x',
      '/schema/agent-consent?scope=session-x',
    ]);
  });
});

describe('mergeAgentParams', () => {
  it('overlays local edits onto server params without dropping untouched keys', () => {
    expect(
      mergeAgentParams(
        { size: '1024x1024', quality: 'high', n: 1 },
        { quality: 'low' },
      ),
    ).toEqual({ size: '1024x1024', quality: 'low', n: 1 });
  });

  it('drops undefined local values so cleared fields fall back to schema defaults', () => {
    expect(
      mergeAgentParams({ size: '1024x1024' }, { size: undefined, n: 2 }),
    ).toEqual({ n: 2 });
  });
});

describe('projectConsentState', () => {
  it('projects a pending consent request', () => {
    expect(
      projectConsentState({ consent_pending: { tool: 'generate' }, consent_request_id: 'request-a' }),
    ).toEqual({
      pending: { tool: 'generate' },
      approved: false,
      taskId: null,
      requestId: 'request-a',
    });
  });

  it('projects approval and bound task state', () => {
    expect(
      projectConsentState({
        consent_approved: true,
        task_id: 'task-9',
      }),
    ).toEqual({ pending: null, approved: true, taskId: 'task-9', requestId: null });
  });

  it('projects an empty state as no consent interaction', () => {
    expect(projectConsentState({})).toEqual({
      pending: null,
      approved: false,
      taskId: null,
      requestId: null,
    });
  });

  it('tolerates null and non-object state', () => {
    expect(projectConsentState(null)).toEqual({
      pending: null,
      approved: false,
      taskId: null,
      requestId: null,
    });
  });
});

describe('consentDecision / consentAwaitingDecision', () => {
  it('treats a pending request without decision as awaiting approval', () => {
    const state = { consent_pending: { reason: 'extra credits' } };
    expect(consentDecision(state)).toBe('pending');
    expect(consentAwaitingDecision(state)).toBe(true);
  });

  it('reads the explicit decision marker written by the consent PUT', () => {
    const pending = { consent_pending: { reason: 'extra credits' } };
    expect(
      consentDecision({ ...pending, consent_decision: 'approved' }),
    ).toBe('approved');
    expect(
      consentAwaitingDecision({ ...pending, consent_decision: 'approved' }),
    ).toBe(false);
    expect(
      consentDecision({ ...pending, consent_decision: 'denied' }),
    ).toBe('denied');
    expect(
      consentAwaitingDecision({ ...pending, consent_decision: 'denied' }),
    ).toBe(false);
    expect(
      consentDecision({ ...pending, consent_decision: 'pending' }),
    ).toBe('pending');
  });

  it('falls back to the approved bit for legacy states', () => {
    const pending = { consent_pending: { reason: 'x' } };
    expect(
      consentDecision({ ...pending, consent_approved: true }),
    ).toBe('approved');
    expect(
      consentAwaitingDecision({ ...pending, consent_approved: true }),
    ).toBe(false);
  });

  it('reports none when no consent was ever requested', () => {
    expect(consentDecision({})).toBe('none');
    expect(consentDecision(null)).toBe('none');
    expect(consentAwaitingDecision({ consent_approved: true })).toBe(false);
  });
});

it('reads effective parameter sources separately from overrides and writes only explicit null/value patches', async () => {
  const payload = { params: { quality: 'detailed' }, parameter_states: { quality: { value: 'detailed', source: 'agent', reason: 'Keep the requested surface detail' }, count: { value: 1, source: 'default' } } };
  const fetcher = vi.fn(async (_url: string | URL | Request, _init?: RequestInit) => new Response(JSON.stringify(payload), { status: 200 }));
  const client = createAgentHarnessClient({ routes: { params: '/params', consent: '/consent', bind: '/bind' }, fetcher });
  const state = await client.getParamsWithState('session', undefined, 'offering');
  expect(state.params).toEqual({ quality: 'detailed' });
  expect(state.parameterStates.quality).toEqual(payload.parameter_states.quality);
  await client.putParamsWithState('session', { count: null }, undefined, 'offering');
  expect(JSON.parse(String(fetcher.mock.calls[1][1]?.body))).toEqual({ params: { count: null } });
});
