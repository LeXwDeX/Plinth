import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ChatBrowserError } from '@/lib/chat/browser-client';
import type { ProductSchema } from '@/lib/product-schema';
import { advanceWithQueue, threeViewQueueLimitMs } from '@/lib/workspace/three-view-queue';

const full = (seconds?: number) => new ChatBrowserError('进行中的任务数量已达上限', 429, '', seconds);

describe('advanceWithQueue', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it('429 加 Retry-After 时标记排队，等待后用同样的调用重提，成功后返回', async () => {
    const call = vi.fn().mockRejectedValueOnce(full(30)).mockResolvedValueOnce('group');
    const onQueued = vi.fn();
    const result = advanceWithQueue({ call, onQueued, signal: new AbortController().signal, limitMs: 1_800_000 });
    await vi.advanceTimersByTimeAsync(0);
    expect(onQueued).toHaveBeenCalledOnce();
    expect(call).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(29_999);
    expect(call).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await expect(result).resolves.toBe('group');
    expect(call).toHaveBeenCalledTimes(2);
    expect(onQueued).toHaveBeenCalledOnce();
  });

  it('没有 Retry-After 的 429 和其它错误照旧抛出，不排队', async () => {
    const onQueued = vi.fn();
    await expect(advanceWithQueue({ call: () => Promise.reject(full()), onQueued, signal: new AbortController().signal, limitMs: 1_800_000 })).rejects.toThrow('进行中的任务数量已达上限');
    await expect(advanceWithQueue({ call: () => Promise.reject(new ChatBrowserError('boom', 500, '', 5)), onQueued, signal: new AbortController().signal, limitMs: 1_800_000 })).rejects.toThrow('boom');
    expect(onQueued).not.toHaveBeenCalled();
  });

  it('超过总时长后抛出最后一次的错误', async () => {
    const call = vi.fn().mockRejectedValue(full(30));
    const result = advanceWithQueue({ call, onQueued: vi.fn(), signal: new AbortController().signal, limitMs: 60_000 });
    const assertion = expect(result).rejects.toThrow('进行中的任务数量已达上限');
    await vi.advanceTimersByTimeAsync(120_000);
    await assertion;
    expect(call).toHaveBeenCalledTimes(3);
  });

  it('中止后停止等待，不再重提', async () => {
    const call = vi.fn().mockRejectedValue(full(30));
    const controller = new AbortController();
    const result = advanceWithQueue({ call, onQueued: vi.fn(), signal: controller.signal, limitMs: 1_800_000 });
    await vi.advanceTimersByTimeAsync(10_000);
    controller.abort();
    await expect(result).resolves.toBeUndefined();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(call).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('threeViewQueueLimitMs', () => {
  const withLimits = (limits: unknown) => ({ chat: { three_view_limits: limits } }) as unknown as ProductSchema;

  it('取 Schema 声明的秒数换算成毫秒', () => {
    expect(threeViewQueueLimitMs(withLimits({ queue_limit_seconds: 1800 }))).toBe(1_800_000);
  });

  it('字段缺失或不是正整数时返回 null，不给兜底时长', () => {
    expect(threeViewQueueLimitMs({ chat: {} } as unknown as ProductSchema)).toBeNull();
    for (const value of [0, -5, 1.5, '1800', null]) {
      expect(threeViewQueueLimitMs(withLimits({ queue_limit_seconds: value }))).toBeNull();
    }
  });
});
