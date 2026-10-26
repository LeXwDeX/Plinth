import { describe, expect, it, vi } from 'vitest';
import type { AdministrationProductSchema } from '@/lib/product-schema';
import {
  createAdministrationGenerationsBrowserClient,
} from './generations-browser-client';

const schema = {
  api: {
    fields: {
      data: 'items',
      error: 'issue',
      total: 'itemCount',
      page: 'current',
      page_size: 'count',
      id: 'recordId',
      user_id: 'ownerId',
      user_name: 'ownerName',
      user_email: 'ownerEmail',
      task_type: 'kind',
      task_id: 'jobId',
      estimated_credits: 'credits',
      task_status: 'phase',
      params: 'arguments',
      result_urls: 'outputs',
      thumbnail_url: 'preview',
      created_at: 'created',
      finished_at: 'finished',
      ledger: 'job',
      task_cache: 'cache',
      status: 'cachePhase',
      provider_id: 'provider',
      cache_data: 'payload',
      cached_at: 'cached',
      updated_at: 'updated',
    },
    query: {
      page: 'current',
      page_size: 'count',
      user_id: 'owner',
      task_type: 'kind',
      status: 'phase',
      from: 'startedAfter',
      to: 'startedBefore',
    },
    path_parameters: {
      task_id: 'jobID',
    },
  },
  messages: {
    internal_error: 'configured internal error',
  },
} as unknown as AdministrationProductSchema['generations'];

const routes = {
  collection: '/configured/admin/jobs',
  item: '/configured/admin/jobs/{jobID}',
  download: '/configured/admin/jobs/{jobID}/download',
};

describe('administration generations browser client', () => {
  it('从 Schema 生成查询并隐藏列表线协议', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(JSON.stringify({
      items: [{
        recordId: 'ledger-1',
        ownerId: 'user-1',
        ownerName: 'Alice',
        ownerEmail: 'alice@example.test',
        kind: 'text-to-image',
        jobId: 'task/one',
        credits: 7,
        phase: 'SUCCEEDED',
        arguments: '{"prompt":"cat"}',
        outputs: '["/image.png"]',
        preview: '/thumb.png',
        created: '2026-07-24T12:00:00Z',
        finished: null,
      }],
      itemCount: 1,
      current: 2,
      count: 9,
    })));
    const client = createAdministrationGenerationsBrowserClient({
      schema,
      routes,
      fetcher,
    });

    const page = await client.listGenerations({
      page: 2,
      pageSize: 9,
      userId: 'user-1',
      taskType: 'text-to-image',
      status: 'SUCCEEDED',
      from: '2026-07-20T00:00:00Z',
      to: '2026-07-24T23:59:59Z',
    });

    const requested = new URL(
      String(fetcher.mock.calls[0]?.[0]),
      'https://art-ai.test',
    );
    expect(requested.pathname).toBe('/configured/admin/jobs');
    expect(Object.fromEntries(requested.searchParams)).toEqual({
      current: '2',
      count: '9',
      owner: 'user-1',
      kind: 'text-to-image',
      phase: 'SUCCEEDED',
      startedAfter: '2026-07-20T00:00:00Z',
      startedBefore: '2026-07-24T23:59:59Z',
    });
    expect(page).toEqual({
      records: [{
        id: 'ledger-1',
        userId: 'user-1',
        userName: 'Alice',
        userEmail: 'alice@example.test',
        taskType: 'text-to-image',
        taskId: 'task/one',
        estimatedCredits: 7,
        taskStatus: 'SUCCEEDED',
        params: '{"prompt":"cat"}',
        resultUrls: '["/image.png"]',
        thumbnailUrl: '/thumb.png',
        createdAt: '2026-07-24T12:00:00Z',
        finishedAt: null,
      }],
      total: 1,
      page: 2,
      pageSize: 9,
    });
  });

  it('从 Schema 生成详情与下载地址并隐藏嵌套线协议', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(JSON.stringify({
      job: {
        recordId: 'task/one',
        ownerId: 'user-1',
        ownerName: 'Alice',
        ownerEmail: '',
        kind: 'text-to-image',
        jobId: 'task/one',
        credits: 7,
        phase: 'SUCCEEDED',
        arguments: { prompt: 'cat' },
        outputs: ['/image.png'],
        preview: '/thumb.png',
        created: '2026-07-24T12:00:00Z',
        finished: '2026-07-24T12:01:00Z',
      },
      cache: {
        jobId: 'task/one',
        kind: 'text-to-image',
        cachePhase: 'SUCCEEDED',
        provider: 'openai',
        payload: { remote: true },
        cached: '2026-07-24T12:00:00Z',
        updated: '2026-07-24T12:01:00Z',
      },
    })));
    const client = createAdministrationGenerationsBrowserClient({
      schema,
      routes,
      fetcher,
    });

    const detail = await client.getGeneration('task/one');

    expect(fetcher).toHaveBeenCalledWith(
      '/configured/admin/jobs/task%2Fone',
      undefined,
    );
    expect(detail.ledger.params).toEqual({ prompt: 'cat' });
    expect(detail.ledger.resultUrls).toEqual(['/image.png']);
    expect(detail.taskCache).toEqual({
      taskId: 'task/one',
      taskType: 'text-to-image',
      status: 'SUCCEEDED',
      providerId: 'openai',
      data: { remote: true },
      cachedAt: '2026-07-24T12:00:00Z',
      updatedAt: '2026-07-24T12:01:00Z',
    });
    expect(client.generationDownloadURL('task/one')).toBe(
      '/configured/admin/jobs/task%2Fone/download',
    );
  });
});
