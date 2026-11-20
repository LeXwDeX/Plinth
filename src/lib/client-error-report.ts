import { problemRequestId } from '@/lib/problem-reference';

/**
 * #233：把浏览器里的错误（错误边界、window error、未处理的 Promise 拒绝）送到服务端日志。
 * 只在已配置路由后发送；同一错误 30 秒内只报一次，每次页面加载最多报 20 条；发送失败静默，
 * 不能让上报本身再引发错误。
 *
 * #387：错误边界与全局监听不知道出错前最后一个请求是哪个，所以由 `installRequestIdTracking` 记住最近一次响应的
 * `X-Request-ID`，上报时作为 `request_id` 带上（服务端日志里是 `related_request_id`），可据此对上服务端日志。
 */
const DEDUPE_MILLISECONDS = 30_000;
const PAGE_LIMIT = 20;
const MESSAGE_LIMIT = 1000;
const STACK_LIMIT = 4000;

let route: string | null = null;
let revision = '';
let sent = 0;
const recent = new Map<string, number>();
let lastRequestId = '';
// The real fetch while tracking is installed: the report itself goes out through it, so the report's own
// response (which carries an ID too) never displaces the request being reported on.
let untrackedFetch: typeof fetch | null = null;
let removeTracking: (() => void) | null = null;

export function configureClientErrorReporting(nextRoute: string | undefined, nextRevision: string): void {
  route = nextRoute && nextRoute.startsWith('/') && !nextRoute.startsWith('//') ? nextRoute : null;
  revision = nextRevision;
}

function describe(error: unknown): { message: string; stack: string } {
  if (error instanceof Error) return { message: error.message || error.name, stack: error.stack ?? '' };
  if (typeof error === 'string') return { message: error, stack: '' };
  try {
    return { message: JSON.stringify(error) ?? String(error), stack: '' };
  } catch {
    return { message: String(error), stack: '' };
  }
}

export function reportClientError(kind: string, error: unknown, extra: { stack?: string; requestId?: string } = {}): void {
  if (!route || typeof window === 'undefined' || sent >= PAGE_LIMIT) return;
  const { message, stack } = describe(error);
  const key = `${kind}\n${message}`;
  const now = Date.now();
  const last = recent.get(key);
  if (last !== undefined && now - last < DEDUPE_MILLISECONDS) return;
  recent.set(key, now);
  sent += 1;
  const body = JSON.stringify({
    kind,
    message: message.slice(0, MESSAGE_LIMIT),
    stack: [stack, extra.stack ?? ''].filter(Boolean).join('\n').slice(0, STACK_LIMIT),
    page: window.location.pathname,
    revision,
    request_id: extra.requestId || lastRequestId,
  });
  try {
    void (untrackedFetch ?? fetch)(route, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      keepalive: true,
      body,
    }).catch(() => undefined);
  } catch {
    // 上报失败不影响页面。
  }
}

/** 安装全局错误监听，返回卸载函数。 */
export function installGlobalErrorReporting(): () => void {
  const onError = (event: ErrorEvent) => reportClientError('window-error', event.error ?? event.message);
  const onRejection = (event: PromiseRejectionEvent) => reportClientError('unhandled-rejection', event.reason);
  window.addEventListener('error', onError);
  window.addEventListener('unhandledrejection', onRejection);
  return () => {
    window.removeEventListener('error', onError);
    window.removeEventListener('unhandledrejection', onRejection);
  };
}

/** 最近一次带 `X-Request-ID` 的响应的 ID（只认服务端格式）；还没有响应时为空串。 */
export function lastRequestIdSeen(): string {
  return lastRequestId;
}

/**
 * 包装全局 `fetch`，记住每个响应的 `X-Request-ID`（成功与失败都记）。应用里有几十处直接调用 `fetch`，
 * 在这一处接入才不会漏；响应与拒绝原样透传，记录出错也不影响请求。重复安装无效，返回卸载函数。
 */
export function installRequestIdTracking(): () => void {
  if (untrackedFetch || typeof globalThis.fetch !== 'function') return () => undefined;
  const native = globalThis.fetch;
  const tracked = function (this: unknown, ...args: Parameters<typeof fetch>) {
    return native.apply(this, args).then((response) => {
      try {
        lastRequestId = problemRequestId({ request_id: response.headers.get('X-Request-ID') ?? '' }) || lastRequestId;
      } catch {
        // 记录失败不能影响请求本身。
      }
      return response;
    });
  } as typeof fetch;
  globalThis.fetch = tracked;
  untrackedFetch = native;
  const remove = () => {
    if (globalThis.fetch === tracked) globalThis.fetch = native;
    if (removeTracking === remove) {
      untrackedFetch = null;
      removeTracking = null;
    }
  };
  removeTracking = remove;
  return remove;
}

/** 仅测试使用：清空去重与计数，撤掉 fetch 包装。 */
export function resetClientErrorReportingForTest(): void {
  removeTracking?.();
  route = null;
  revision = '';
  sent = 0;
  recent.clear();
  lastRequestId = '';
}
