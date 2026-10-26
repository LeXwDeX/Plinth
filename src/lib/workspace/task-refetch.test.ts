import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { parseRefetchEligibility, refetchRoute, refetchUnavailableText, startRefetch } from './task-refetch';

const schema = defaultCatalog.application as unknown as ProductSchema;

afterEach(() => vi.unstubAllGlobals());

describe('task refetch client', () => {
  it('builds the owner route from the Schema', () => {
    expect(refetchRoute(schema, 'task/1')).toBe('/api/v1/tasks/task%2F1/refetch');
  });

  it('parses only the known server shape', () => {
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: true, refetch_task_id: 'r', refetch_state: 'QUEUED' }, 't'))
      .toEqual({ sourceTaskId: 't', eligible: true, refetchTaskId: 'r', refetchState: 'QUEUED' });
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: false, reason: 'expired' }, 't'))
      .toEqual({ sourceTaskId: 't', eligible: false, reason: 'expired' });
    expect(parseRefetchEligibility({ source_task_id: 'other', eligible: true }, 't')).toBeNull();
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: false }, 't')).toBeNull();
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: false, reason: 'unknown' }, 't')).toBeNull();
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: false, reason: 'repeated_failure', failure_class: 'integrity_failure' }, 't'))
      .toEqual({ sourceTaskId: 't', eligible: false, reason: 'repeated_failure', failureClass: 'integrity_failure' });
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: false, reason: 'repeated_failure', failure_class: '<b>' }, 't'))
      .toEqual({ sourceTaskId: 't', eligible: false, reason: 'repeated_failure' });
  });

  it('names the cause of a repeated failure with the Schema text of its class', () => {
    const text = refetchUnavailableText(schema, { sourceTaskId: 't', eligible: false, reason: 'repeated_failure', failureClass: 'integrity_failure' });
    expect(text).toContain(schema.content.messages['failure.integrity_failure']);
    expect(text).not.toContain('{cause}');
    expect(refetchUnavailableText(schema, { sourceTaskId: 't', eligible: false, reason: 'expired' }))
      .toBe(schema.content.messages['ui.task.refetch.unavailable.expired']);
  });

  it('starts a refetch with an empty POST and reads a refusal as its reason', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ refetch: { source_task_id: 't', eligible: true, refetch_task_id: 'r', refetch_state: 'QUEUED' }, task_id: 'r', replayed: false }), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ refetch: { source_task_id: 't', eligible: false, reason: 'not_supported' }, replayed: false }), { status: 409 }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(startRefetch(schema, 't')).resolves.toMatchObject({ taskId: 'r', replayed: false });
    expect(fetchMock.mock.calls[0][1]).toEqual({ method: 'POST' });
    await expect(startRefetch(schema, 't')).resolves.toEqual({ eligibility: { sourceTaskId: 't', eligible: false, reason: 'not_supported' }, replayed: false });
  });
});
