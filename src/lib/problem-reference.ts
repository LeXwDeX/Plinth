/**
 * #240：服务端错误体带 `request_id`（#233），界面错误文字后附上它，用户报错时可据此查日志。
 * 文案模板来自公开 Schema `chat.presentation.messages.error_reference`（含 `{id}`），
 * 由 ProductSchemaProvider 配置；未配置或没有 ID 时原样返回。
 */
let template = '';

export function configureErrorReference(next: string | undefined): void {
  template = next && next.includes('{id}') ? next : '';
}

/** 从错误体里读 request_id（只接受服务端格式 `req_` + 十六进制）。 */
export function problemRequestId(body: unknown): string {
  if (!body || typeof body !== 'object') return '';
  const value = (body as { request_id?: unknown }).request_id;
  return typeof value === 'string' && /^req_[0-9a-f]{8,32}$/.test(value) ? value : '';
}

export function withRequestReference(text: string, body: unknown): string {
  const id = problemRequestId(body);
  if (!template || !id) return text;
  return text + template.split('{id}').join(id);
}

/**
 * #288：服务端拒绝请求时，若错误体标了 `user_facing: true`，`detail` 就是写给用户的具体原因
 * （Schema 文案，例如参考图超过模型像素上限），界面应原样显示它，而不是自己的通用失败文案。
 * 没有该标记的 `detail` 只是通用说明，不当作原因。
 */
export function problemReason(body: unknown): string {
  if (!body || typeof body !== 'object') return '';
  const { detail, user_facing: userFacing } = body as { detail?: unknown; user_facing?: unknown };
  return userFacing === true && typeof detail === 'string' ? detail.trim() : '';
}

/** An HTTP failure that keeps the server's request ID for the error reference (#240) and its user-facing reason (#288). */
export class RequestFailure extends Error {
  readonly requestId: string;
  readonly reason: string;

  constructor(message: string, requestId = '', reason = '') {
    super(message);
    this.name = 'RequestFailure';
    this.requestId = requestId;
    this.reason = reason;
  }
}

/** The user-facing reason a failed request carries (#288), or '' when the server gave none. */
export function reasonOf(cause: unknown): string {
  return cause instanceof RequestFailure ? cause.reason : '';
}

/** The request ID of a failed response: the error body's `request_id`, else the `X-Request-ID` header. */
export function responseRequestId(response: Response, body?: unknown): string {
  return problemRequestId(body) || problemRequestId({ request_id: response.headers?.get?.('X-Request-ID') ?? '' });
}

/** The request ID an error carries, if any. */
export function requestIdOf(cause: unknown): string {
  if (!cause || typeof cause !== 'object') return '';
  return problemRequestId({ request_id: (cause as { requestId?: unknown }).requestId });
}

/** `text` followed by the error reference of the request that failed, when the error knows it. */
export function withErrorReference(text: string, cause: unknown): string {
  return withRequestReference(text, { request_id: requestIdOf(cause) });
}
