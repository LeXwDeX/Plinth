import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';

const schema = defaultCatalog.application as unknown as ProductSchema;
const advance = (headers: Record<string, string>) => createChatBrowserClient(schema, {
  fetcher: async () => Response.json({ detail: '进行中的任务数量已达上限' }, { status: 429, headers }),
}).advanceThreeView('s1', 'run1', 'front', 'start', 1);

describe('advanceThreeView 429', () => {
  it('把 Retry-After 秒数放进 ChatBrowserError', async () => {
    const error = await advance({ 'Retry-After': '30' }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ChatBrowserError);
    expect((error as ChatBrowserError).status).toBe(429);
    expect((error as ChatBrowserError).retryAfterSeconds).toBe(30);
  });

  it.each<Record<string, string>>([{}, { 'Retry-After': 'soon' }, { 'Retry-After': '0' }, { 'Retry-After': '-5' }])('缺失、非法或不大于 0（%j）时不带等待秒数', async (headers) => {
    const error = await advance(headers).catch((e: unknown) => e) as ChatBrowserError;
    expect(error.status).toBe(429);
    expect(error.retryAfterSeconds).toBeUndefined();
  });
});
