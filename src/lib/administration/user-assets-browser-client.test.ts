import { describe, expect, it, vi } from 'vitest';
import type { AdministrationProductSchema } from '@/lib/product-schema';
import {
  AdministrationUserAssetsBrowserError,
  createAdministrationUserAssetsBrowserClient,
} from './user-assets-browser-client';

const schema = {
  api: {
    envelope_fields: {
      error: 'issue',
      user: 'owner',
      summary: 'metrics',
      uploads: 'uploadPage',
      generations: 'generationPage',
      downloads: 'downloadPage',
    },
    page_fields: {
      data: 'rows',
      total: 'itemCount',
      page: 'cursor',
      page_size: 'limitValue',
    },
    user_fields: {
      id: 'accountId',
      username: 'login',
      nickname: 'alias',
      email: 'mail',
      name: 'displayName',
      is_admin: 'privileged',
      is_active: 'enabled',
      created_at: 'registered',
      last_login: 'lastSeen',
    },
    summary_fields: {
      total_uploads: 'uploadCount',
      total_generations: 'generationCount',
      total_downloads: 'downloadCount',
      total_storage_bytes: 'storedBytes',
    },
    upload_fields: {
      id: 'uploadId',
      asset_type: 'assetKind',
      original_filename: 'sourceName',
      stored_filename: 'storedName',
      mime_type: 'contentType',
      file_size: 'byteSize',
      storage_path: 'storageKey',
      created_at: 'uploaded',
    },
    generation_fields: {
      id: 'recordId',
      task_type: 'capability',
      task_id: 'task',
      task_status: 'phase',
      result_urls: 'outputs',
      params: 'inputs',
      estimated_credits: 'credits',
      thumbnail_url: 'preview',
      created_at: 'created',
      finished_at: 'completed',
    },
    download_fields: {
      id: 'eventId',
      action: 'eventType',
      resource_type: 'subjectType',
      resource_id: 'subjectId',
      detail: 'metadata',
      created_at: 'observed',
    },
    query: {
      page_size: 'limit',
      uploads_page: 'uploadCursor',
      generations_page: 'generationCursor',
      downloads_page: 'downloadCursor',
      from: 'begin',
      to: 'finish',
    },
    path_parameters: { user_id: 'account' },
  },
  messages: {
    user_not_found: 'configured missing',
    internal_error: 'configured internal',
  },
} as unknown as AdministrationProductSchema['user_assets'];

const routes = {
  overview: '/configured/accounts/{account}/assets',
  asset: '/configured/uploads/{upload}',
};

describe('administration user assets browser client', () => {
  it('从 Schema 生成请求并隐藏所有嵌套线协议', async () => {
    const fetcher = vi.fn(async (
      _input: RequestInfo | URL,
      _init?: RequestInit,
    ) => new Response(JSON.stringify({
      owner: {
        accountId: 'user/one',
        login: 'alice',
        alias: 'Alice Alias',
        mail: 'alice@example.test',
        displayName: 'Alice',
        privileged: true,
        enabled: false,
        registered: '2026-07-26T00:00:00Z',
        lastSeen: null,
      },
      metrics: {
        uploadCount: 1,
        generationCount: 1,
        downloadCount: 1,
        storedBytes: 2048,
      },
      uploadPage: {
        rows: [{
          uploadId: 'asset/one',
          assetKind: 'image',
          sourceName: 'cat.png',
          storedName: 'stored.png',
          contentType: 'image/png',
          byteSize: 2048,
          storageKey: 'uploads/stored.png',
          uploaded: '2026-07-26T01:00:00Z',
        }],
        itemCount: 1,
        cursor: 2,
        limitValue: 7,
      },
      generationPage: {
        rows: [{
          recordId: 'ledger-1',
          capability: 'text-to-image',
          task: 'task-1',
          phase: 'SUCCEEDED',
          outputs: '["/result.png"]',
          inputs: '{"prompt":"cat"}',
          credits: 7,
          preview: null,
          created: '2026-07-26T02:00:00Z',
          completed: null,
        }],
        itemCount: 1,
        cursor: 3,
        limitValue: 7,
      },
      downloadPage: {
        rows: [{
          eventId: 'event-1',
          eventType: 'download_asset',
          subjectType: 'task_asset',
          subjectId: 'task-1',
          metadata: '{"source":"test"}',
          observed: '2026-07-26T03:00:00Z',
        }],
        itemCount: 1,
        cursor: 4,
        limitValue: 7,
      },
    })));
    const client = createAdministrationUserAssetsBrowserClient({
      schema,
      routes,
      fetcher,
    });

    const overview = await client.getUserAssets('user/one', {
      pageSize: 7,
      uploadsPage: 2,
      generationsPage: 3,
      downloadsPage: 4,
      from: '2026-07-20T00:00:00Z',
      to: '2026-07-26T23:59:59Z',
    });

    const requested = new URL(
      String(fetcher.mock.calls[0]?.[0]),
      'https://art-ai.test',
    );
    expect(requested.pathname).toBe('/configured/accounts/user%2Fone/assets');
    expect(Object.fromEntries(requested.searchParams)).toEqual({
      limit: '7',
      uploadCursor: '2',
      generationCursor: '3',
      downloadCursor: '4',
      begin: '2026-07-20T00:00:00Z',
      finish: '2026-07-26T23:59:59Z',
    });
    expect(overview.user).toEqual({
      id: 'user/one',
      username: 'alice',
      nickname: 'Alice Alias',
      email: 'alice@example.test',
      name: 'Alice',
      isAdmin: true,
      isActive: false,
      createdAt: '2026-07-26T00:00:00Z',
      lastLogin: null,
    });
    expect(overview.summary.totalStorageBytes).toBe(2048);
    expect(overview.uploads).toEqual({
      data: [{
        id: 'asset/one',
        assetType: 'image',
        originalFilename: 'cat.png',
        storedFilename: 'stored.png',
        mimeType: 'image/png',
        fileSize: 2048,
        storagePath: 'uploads/stored.png',
        createdAt: '2026-07-26T01:00:00Z',
      }],
      total: 1,
      page: 2,
      pageSize: 7,
    });
    expect(overview.generations.data[0]?.resultUrls).toBe(
      '["/result.png"]',
    );
    expect(overview.downloads.data[0]?.resourceId).toBe('task-1');
    expect(client.assetURL('asset/one')).toBe(
      '/configured/uploads/asset%2Fone',
    );
  });

  it('使用 Schema 错误字段并保留 HTTP 状态', async () => {
    const client = createAdministrationUserAssetsBrowserClient({
      schema,
      routes,
      fetcher: vi.fn(async () => new Response(JSON.stringify({
        issue: 'configured failure',
      }), { status: 404 })),
    });

    await expect(client.getUserAssets('missing', {})).rejects.toEqual(
      new AdministrationUserAssetsBrowserError(404, 'configured failure'),
    );
  });
});
