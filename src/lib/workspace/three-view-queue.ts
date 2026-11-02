import { ChatBrowserError } from '@/lib/chat/browser-client';

/**
 * 排队等待的总时长上限。前端 Schema 没有暴露可用的请求超时，这里取 30 分钟：
 * 足够覆盖进行中任务排空的常见时间，又不会让标签页无限期挂着重试。
 */
export const THREE_VIEW_QUEUE_LIMIT_MS = 30 * 60 * 1000;

export interface QueuedAdvanceOptions<T> {
  /** 一次提交。进行中任务已满时应抛出带 `retryAfterSeconds` 的 429 `ChatBrowserError`。 */
  call: () => Promise<T>;
  /** 第一次被排队时调用一次（之后的重试不重复调用）。 */
  onQueued: () => void;
  /** 组件卸载或会话切换时中止等待。 */
  signal: AbortSignal;
  limitMs?: number;
}

/**
 * 页面发起的三视图是单次请求、没有后台 worker：429 + Retry-After 表示这次一定没有提交，
 * 同一 action/attempt 再次提交是幂等的（同一幂等键不会重复扣积分），所以这里按服务端给的
 * 等待时间自动重提。成功、其它错误、超过总时长或被中止都会结束循环；中止时返回 undefined。
 */
export async function advanceWithQueue<T>({ call, onQueued, signal, limitMs = THREE_VIEW_QUEUE_LIMIT_MS }: QueuedAdvanceOptions<T>): Promise<T | undefined> {
  let waited = 0;
  let queued = false;
  for (;;) {
    if (signal.aborted) return undefined;
    try {
      return await call();
    } catch (error) {
      const seconds = error instanceof ChatBrowserError && error.status === 429 ? error.retryAfterSeconds : undefined;
      if (seconds === undefined || waited + seconds * 1000 > limitMs) throw error;
      if (!queued) {
        queued = true;
        onQueued();
      }
      waited += seconds * 1000;
      if (!await sleep(seconds * 1000, signal)) return undefined;
    }
  }
}

/** 等待指定毫秒；被中止返回 false，等满返回 true。 */
function sleep(ms: number, signal: AbortSignal): Promise<boolean> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve(false);
    const done = (value: boolean) => {
      clearTimeout(timer);
      signal.removeEventListener('abort', onAbort);
      resolve(value);
    };
    const onAbort = () => done(false);
    const timer = setTimeout(() => done(true), ms);
    signal.addEventListener('abort', onAbort);
  });
}
