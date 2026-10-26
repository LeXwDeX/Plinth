import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { TrackedTask } from '@/store/workspace-store';
import { fetchSourceRetries, regenerateBlockedBy, sourceExpiredSteps, sourceRetryText } from './source-retry';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

const tracked = (taskId: string, failureClass: string | null): TrackedTask => ({
  taskId, capability: 'retexture', prompt: '', state: failureClass ? 'FAILED' : 'SUCCEEDED', progress: 0, progressMaximum: 100,
  terminal: true, failure: failureClass ? { failureClass, detail: '', retryable: false, ambiguous: false } : null, liveError: '',
  assets: [], downloadables: [], resources: [], resourcesPending: false, notFound: false, updatedAt: 0,
});

// #326: a chat step that failed because its source's original task is gone is judged by the server with the rule
// plans use; the card offers a paid regenerate only when running the step again can succeed.
describe('a failed chat step judged for regenerate', () => {
  it('asks only about the paid steps that failed as source_expired', () => {
    const tasks = [tracked('expired', 'source_expired'), tracked('rejected', 'rejected'), tracked('done', null)];
    expect(sourceExpiredSteps(['expired', 'rejected', 'done', 'unknown'], tasks)).toEqual(['expired']);
  });

  it('reads each judgment from the owner task detail', async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => ({
      ok: true,
      json: async () => ({ id: 'x', state: 'FAILED', failure: { class: 'source_expired', retryable: false, detail: '',
        ...(String(input).includes('saved') ? { source_retry: 'saved_file' } : String(input).includes('gone') ? { source_retry: 'source_expired' } : {}) } }),
    }) as Response);
    const judged = await fetchSourceRetries(schema, ['saved', 'gone', 'plain'], fetcher as unknown as typeof fetch);
    expect(judged).toEqual({ saved: 'saved_file', gone: 'source_expired', plain: '' });
    expect(fetcher).toHaveBeenCalledWith(schema.runtime.http.routes.task_item.replace('{taskID}', 'saved'));
  });

  it('says nothing about a step it could not read', async () => {
    const fetcher = vi.fn(async () => ({ ok: false, json: async () => ({}) }) as Response);
    expect(await fetchSourceRetries(schema, ['t'], fetcher as unknown as typeof fetch)).toEqual({ t: '' });
  });

  it('blocks a paid regenerate only when a step is certain to fail', () => {
    expect(regenerateBlockedBy({ a: 'source_expired' })).toBe(true);
    expect(regenerateBlockedBy({ a: 'source_incompatible', b: 'saved_file' })).toBe(true);
    expect(regenerateBlockedBy({ a: 'saved_file' })).toBe(false);
    expect(regenerateBlockedBy({ a: '' })).toBe(false);
    expect(regenerateBlockedBy(undefined)).toBe(false);
  });

  it('names the reason, or the saved file the regenerate will use, in Schema words', () => {
    expect(sourceRetryText(schema, 'saved_file')).toBe(messages['ui.studio.result.source_retry.saved_file']);
    expect(sourceRetryText(schema, 'source_expired')).toBe(messages['ui.studio.result.source_retry.source_expired']);
    expect(sourceRetryText(schema, 'source_incompatible')).toBe(messages['ui.studio.result.source_retry.source_incompatible']);
    expect(sourceRetryText(schema, '')).toBe('');
    expect(sourceRetryText(schema, 'something_new')).toBe('');
  });
});
