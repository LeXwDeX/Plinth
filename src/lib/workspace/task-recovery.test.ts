import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { fetchRecoveryEligibility, parseRecoveryEligibility, prepareRecovery, type RecoveryEligibility } from './task-recovery';

const schema = defaultCatalog.application as unknown as ProductSchema;
const eligibility: RecoveryEligibility = {
  sourceTaskId: 'source-1', eligible: true, receiptDigest: 'digest-1', sessionId: 'session-1',
  capability: 'asset-recovery', idempotencyKey: 'recover-key',
};
const wire = {
  source_task_id: 'source-1', eligible: true, receipt_digest: 'digest-1', session_id: 'session-1',
  capability: 'asset-recovery', idempotency_key: 'recover-key', slots: [{ role: 'model' }],
};

afterEach(() => vi.unstubAllGlobals());

describe('task recovery API projection', () => {
  it('accepts only owner-scoped, eligible responses with the frozen receipt and stable key', () => {
    expect(parseRecoveryEligibility(wire, 'source-1')).toEqual(eligibility);
    expect(parseRecoveryEligibility({ ...wire, source_task_id: 'other' }, 'source-1')).toBeNull();
    expect(parseRecoveryEligibility({ ...wire, idempotency_key: '' }, 'source-1')).toBeNull();
    expect(parseRecoveryEligibility({ source_task_id: 'source-1', eligible: false }, 'source-1'))
      .toEqual({ sourceTaskId: 'source-1', eligible: false });
  });

  it('queries the Schema route and treats a foreign or missing task as an eligibility failure', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(wire), { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    await expect(fetchRecoveryEligibility(schema, 'source-1')).resolves.toEqual(eligibility);
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/tasks/source-1/recovery');
    fetch.mockResolvedValueOnce(new Response(JSON.stringify({ detail: 'not found' }), { status: 404 }));
    await expect(fetchRecoveryEligibility(schema, 'foreign')).rejects.toThrow('not found');
  });

  it('sends only the receipt digest, then uses the server idempotency key and reads Grant status', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        recovery: wire, prepared_input_id: 'prepared-1', input_hash: 'hash-1', session_id: 'session-1',
        capability: 'asset-recovery', offering_id: 'local/recovery', offering_revision: 'r1',
      }), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ request_id: 'grant-1', status: 'pending', approved_by_policy: false }), { status: 201 }));
    vi.stubGlobal('fetch', fetch);
    const result = await prepareRecovery(schema, 'source-1', eligibility, 'saved prompt');
    expect(result.kind).toBe('prepared');
    if (result.kind !== 'prepared') return;
    expect(result.grant).toMatchObject({ requestId: 'grant-1', grantApproved: false, approvedByPolicy: false, prompt: 'saved prompt' });
    const prepareRequest = fetch.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(prepareRequest.body))).toEqual({ receipt_digest: 'digest-1' });
    const grantRequest = fetch.mock.calls[1][1] as RequestInit;
    expect(grantRequest.headers).toMatchObject({ 'Idempotency-Key': 'recover-key' });
    expect(JSON.parse(String(grantRequest.body))).toEqual({ prepared_input_id: 'prepared-1' });
  });

  it('submits a policy-approved grant directly and opens an already-created task on replay', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ recovery: wire, prepared_input_id: 'prepared-1', session_id: 'session-1', capability: 'asset-recovery', offering_id: 'local/recovery' }), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ request_id: 'grant-1', status: 'approved', approved_by_policy: true }), { status: 201 }));
    vi.stubGlobal('fetch', fetch);
    const approved = await prepareRecovery(schema, 'source-1', eligibility);
    expect(approved.kind === 'prepared' && approved.grant).toMatchObject({ grantApproved: true, approvedByPolicy: true });

    fetch.mockResolvedValueOnce(new Response(JSON.stringify({ recovery: { ...wire, recovery_task_id: 'recovery-1' } }), { status: 200 }));
    await expect(prepareRecovery(schema, 'source-1', eligibility)).resolves.toEqual({ kind: 'existing_task', taskId: 'recovery-1' });
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('refuses a changed receipt instead of submitting a task', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ recovery: { ...wire, receipt_digest: 'different' } }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    await expect(prepareRecovery(schema, 'source-1', eligibility)).rejects.toThrow('recovery receipt changed');
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('does not resubmit a denied execution grant', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ recovery: wire, prepared_input_id: 'prepared-1', session_id: 'session-1', capability: 'asset-recovery', offering_id: 'local/recovery' }), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ request_id: 'grant-1', status: 'denied', approved_by_policy: false }), { status: 201 }));
    vi.stubGlobal('fetch', fetch);
    await expect(prepareRecovery(schema, 'source-1', eligibility)).resolves.toEqual({ kind: 'denied' });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});


describe('recovery mount authority', () => {
  it('does not prepare or request a Grant after its authority ends', async () => {
    let active = false;
    const fetch = vi.fn(async () => {
      active = false;
      return new Response(JSON.stringify({ recovery: wire, prepared_input_id: 'prepared-1', session_id: 'session-1', capability: 'asset-recovery', offering_id: 'local/recovery' }), { status: 201 });
    });
    vi.stubGlobal('fetch', fetch);
    await expect(prepareRecovery(schema, 'source-1', eligibility, '', () => active)).rejects.toThrow('inactive');
    expect(fetch).not.toHaveBeenCalled();
    active = true;
    await expect(prepareRecovery(schema, 'source-1', eligibility, '', () => active)).rejects.toThrow('inactive');
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
