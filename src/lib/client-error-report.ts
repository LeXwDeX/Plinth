/**
 * #233：把浏览器里的错误（错误边界、window error、未处理的 Promise 拒绝）送到服务端日志。
 * 只在已配置路由后发送；同一错误 30 秒内只报一次，每次页面加载最多报 20 条；发送失败静默，
 * 不能让上报本身再引发错误。
 */
const DEDUPE_MILLISECONDS = 30_000;
const PAGE_LIMIT = 20;
const MESSAGE_LIMIT = 1000;
const STACK_LIMIT = 4000;

let route: string | null = null;
let revision = '';
let sent = 0;
const recent = new Map<string, number>();

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
    request_id: extra.requestId ?? '',
  });
  try {
    void fetch(route, {
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

/** 仅测试使用：清空去重与计数。 */
export function resetClientErrorReportingForTest(): void {
  route = null;
  revision = '';
  sent = 0;
  recent.clear();
}
