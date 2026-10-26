import { afterEach, describe, expect, it, vi } from 'vitest';
import { clearPendingDirectTask, contentFingerprint, inpaintSourceIdentity, prepareDirectTask, readPendingDirectTask, savePendingDirectTask, submitDirectTask } from './canvas-image-actions';

const routes = {
  task_prepare: '/api/v1/tasks/prepare',
  task_grants: '/api/v1/tasks/grants',
  task_grant_decision: '/api/v1/tasks/grants/{requestID}',
  tasks: '/api/v1/tasks',
};

afterEach(() => vi.unstubAllGlobals());

describe('canvas paid submission', () => {
  it('recovers the same frozen request after a dialog is reopened', () => {
    const values = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    });
    const pending = { preparedInputId: 'prepared-a', requestId: 'request-a', key: 'key-a',
      sessionId: 'session-a', offeringId: 'model-a', prompt: 'blue robot' };
    savePendingDirectTask('session-a', 'inpaint:task-a:node-a', pending);
    expect(readPendingDirectTask('session-a', 'inpaint:task-a:node-a')).toEqual(pending);
    expect(readPendingDirectTask('session-b', 'inpaint:task-a:node-a')).toBeNull();
    expect(readPendingDirectTask('session-a', 'inpaint:task-b:node-a')).toBeNull();
    clearPendingDirectTask('session-a', 'inpaint:task-a:node-a');
    expect(readPendingDirectTask('session-a', 'inpaint:task-a:node-a')).toBeNull();
  });

  it('does not restore an old inpaint operation after the mask changes on the same node', async () => {
    const values = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    });
    const oldMask = await contentFingerprint(new Blob([new Uint8Array([1, 2, 3])]));
    const newMask = await contentFingerprint(new Blob([new Uint8Array([1, 2, 4])]));
    expect(newMask).not.toBe(oldMask);
    const sourceIdentity = (digest: string) => inpaintSourceIdentity({
      offeringId: 'model-a', sourceFingerprint: 'source-a', maskFingerprint: digest,
      frame: { x: 10, y: 20, width: 30, height: 40 }, size: { width: 30, height: 40 }, target: '30x40',
    });
    const pending = { preparedInputId: 'prepared-old', requestId: 'request-old', key: 'key-old',
      sessionId: 'session-a', offeringId: 'model-a', prompt: 'blue robot' };
    savePendingDirectTask('session-a', sourceIdentity(oldMask), pending);
    expect(readPendingDirectTask('session-a', sourceIdentity(oldMask))).toEqual(pending);
    expect(readPendingDirectTask('session-a', sourceIdentity(newMask))).toBeNull();
  });

  it('keeps the submission identity when the same source bytes return under a fresh upload URL', async () => {
    const sourceBytes = new Uint8Array([137, 80, 78, 71, 1, 2, 3]);
    const first = await contentFingerprint(new Blob([sourceBytes]));
    const second = await contentFingerprint(new Blob([sourceBytes]));
    const request = { offeringId: 'model-a', maskFingerprint: 'mask-a',
      frame: { x: 10, y: 20, width: 30, height: 40 }, size: { width: 30, height: 40 }, target: '30x40' };
    // The board uploads again on every reopen; only the exported bytes may select the frozen request.
    expect(inpaintSourceIdentity({ ...request, sourceFingerprint: first }))
      .toBe(inpaintSourceIdentity({ ...request, sourceFingerprint: second }));
    expect(inpaintSourceIdentity({ ...request, sourceFingerprint: first })).not.toContain('/api/v1/assets');
  });

  it('does not reuse a request when the source bytes, mask, frame, size, target or offering changed', async () => {
    const base = {
      offeringId: 'model-a', sourceFingerprint: 'source-a', maskFingerprint: 'mask-a',
      frame: { x: 10, y: 20, width: 30, height: 40 }, size: { width: 30, height: 40 }, target: '30x40',
    };
    const identity = inpaintSourceIdentity(base);
    expect(inpaintSourceIdentity({ ...base, sourceFingerprint: 'source-b' })).not.toBe(identity);
    expect(inpaintSourceIdentity({ ...base, maskFingerprint: 'mask-b' })).not.toBe(identity);
    expect(inpaintSourceIdentity({ ...base, frame: { ...base.frame, x: 11 } })).not.toBe(identity);
    expect(inpaintSourceIdentity({ ...base, size: { width: 30, height: 41 } })).not.toBe(identity);
    expect(inpaintSourceIdentity({ ...base, target: '60x80' })).not.toBe(identity);
    expect(inpaintSourceIdentity({ ...base, offeringId: 'model-b' })).not.toBe(identity);
  });

  it('prepares an exact input and waits for a separate human confirmation before deciding', async () => {
    const requests: Array<{ url: string; init: RequestInit }> = [];
    const responses = [
      { prepared_input_id: 'prepared-a' },
      { request_id: 'request-a' },
      { request_id: 'request-a', status: 'approved' },
      { task_id: 'task-a' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      requests.push({ url, init });
      return { ok: true, json: async () => responses[requests.length - 1] };
    }));
    const body = { session_id: 'session-a', capability: 'image-to-image', offering_id: 'model-a',
      input: { prompt: 'blue robot', reference_assets: ['asset-a'] } };
    const pending = await prepareDirectTask(routes, body, 'session-a', 'model-a', 'blue robot');
    expect(requests.map(({ url }) => url)).toEqual([routes.task_prepare, routes.task_grants]);
    expect(JSON.parse(String(requests[0].init.body))).toEqual(body);
    expect(JSON.parse(String(requests[1].init.body))).toEqual({ prepared_input_id: 'prepared-a' });

    expect(await submitDirectTask(routes, pending)).toBe('task-a');
    expect(requests.map(({ url }) => url)).toEqual([
      routes.task_prepare, routes.task_grants, '/api/v1/tasks/grants/request-a', routes.tasks,
    ]);
    expect(JSON.parse(String(requests[2].init.body))).toEqual({ decision: 'approve' });
    expect(JSON.parse(String(requests[3].init.body))).toEqual({
      prepared_input_id: 'prepared-a', authorization_request_id: 'request-a',
    });
    expect((requests[1].init.headers as Record<string, string>)['Idempotency-Key'])
      .toBe((requests[3].init.headers as Record<string, string>)['Idempotency-Key']);
  });
  it('skips the human decision when the session approval mode already approved the step', async () => {
    const requests: Array<{ url: string; init: RequestInit }> = [];
    const responses = [
      { prepared_input_id: 'prepared-a' },
      { request_id: 'request-a', status: 'approved', approved_by_policy: true },
      { task_id: 'task-a' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      requests.push({ url, init });
      return { ok: true, json: async () => responses[requests.length - 1] };
    }));
    const body = { session_id: 'session-a', capability: 'image-to-image', offering_id: 'model-a',
      input: { prompt: 'blue robot', reference_assets: ['asset-a'] } };
    const pending = await prepareDirectTask(routes, body, 'session-a', 'model-a', 'blue robot');
    expect(pending.approvedByPolicy).toBe(true);

    expect(await submitDirectTask(routes, pending)).toBe('task-a');
    expect(requests.map(({ url }) => url)).toEqual([routes.task_prepare, routes.task_grants, routes.tasks]);
    expect(requests.some(({ init }) => init.method === 'PUT')).toBe(false);
    expect(JSON.parse(String(requests[2].init.body))).toEqual({
      prepared_input_id: 'prepared-a', authorization_request_id: 'request-a',
    });
  });

  it('submits an already-approved human grant without issuing a second approval', async () => {
    const requests: Array<{ url: string; init: RequestInit }> = [];
    const responses = [
      { prepared_input_id: 'prepared-a' },
      { request_id: 'request-a', status: 'approved', approved_by_policy: false },
      { task_id: 'task-a' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      requests.push({ url, init });
      return { ok: true, json: async () => responses[requests.length - 1] };
    }));
    const pending = await prepareDirectTask(routes, { session_id: 'session-a' }, 'session-a', 'model-a');
    expect(pending.grantApproved).toBe(true);
    expect(await submitDirectTask(routes, pending)).toBe('task-a');
    expect(requests.map(({ url }) => url)).toEqual([routes.task_prepare, routes.task_grants, routes.tasks]);
  });

  it('still asks for a person when the grant is pending', async () => {
    const requests: Array<{ url: string; init: RequestInit }> = [];
    const responses = [
      { prepared_input_id: 'prepared-a' },
      { request_id: 'request-a', status: 'pending', approved_by_policy: false },
    ];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      requests.push({ url, init });
      return { ok: true, json: async () => responses[requests.length - 1] };
    }));
    const pending = await prepareDirectTask(routes, { session_id: 'session-a' }, 'session-a', 'model-a');
    expect(pending.approvedByPolicy).toBeUndefined();
  });
});


describe('optional preparation authority', () => {
  it('does not even prepare after the dialog departs', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(prepareDirectTask(routes, { session_id: 's' }, 's', 'o', 'p', undefined, () => false))
      .rejects.toThrow('inactive');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('does not create a grant after the dialog departs during preparation', async () => {
    const fetch = vi.fn(async (url: string) => ({
      ok: true,
      json: async () => url === routes.task_prepare ? { prepared_input_id: 'p' } : { request_id: 'r' },
    }));
    vi.stubGlobal('fetch', fetch);
    let checks = 0;
    await expect(prepareDirectTask(routes, { session_id: 's' }, 's', 'o', 'p', undefined, () => checks++ === 0))
      .rejects.toThrow('inactive');
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe(routes.task_prepare);
  });
  it('still prepares and grants for an active dialog', async () => {
    const fetch = vi.fn(async (url: string) => ({
      ok: true,
      json: async () => url === routes.task_prepare ? { prepared_input_id: 'p' } : { request_id: 'r' },
    }));
    vi.stubGlobal('fetch', fetch);
    await expect(prepareDirectTask(routes, { session_id: 's' }, 's', 'o', 'p')).resolves.toMatchObject({ preparedInputId: 'p', requestId: 'r' });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

describe('optional submission authority', () => {
  const pending = { preparedInputId: 'p', requestId: 'r', key: 'k', sessionId: 's', offeringId: 'o' };
  it('does not decide or submit after departure, including after an in-flight approval', async () => {
    let active = false;
    const fetch = vi.fn(async (_url: string) => {
      active = false;
      return { ok: true, json: async () => ({ status: 'approved' }) };
    });
    vi.stubGlobal('fetch', fetch);
    await expect(submitDirectTask(routes, pending, () => active)).rejects.toThrow('inactive');
    expect(fetch).not.toHaveBeenCalled();
    active = true;
    await expect(submitDirectTask(routes, pending, () => active)).rejects.toThrow('inactive');
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/tasks/grants/r');
  });
  it('allows an already-sent authorized task to finish after departure', async () => {
    let active = true;
    const fetch = vi.fn(async (_url: string) => {
      active = false;
      return { ok: true, json: async () => ({ task_id: 'accepted-task' }) };
    });
    vi.stubGlobal('fetch', fetch);
    await expect(submitDirectTask(routes, { ...pending, grantApproved: true }, () => active)).resolves.toBe('accepted-task');
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
