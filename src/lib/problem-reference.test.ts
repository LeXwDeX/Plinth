import { afterEach, describe, expect, it } from 'vitest';

import { configureErrorReference, problemReason, reasonOf, RequestFailure, responseRequestId, withErrorReference, withRequestReference } from '@/lib/problem-reference';

describe('withRequestReference (#240)', () => {
  afterEach(() => configureErrorReference(undefined));

  it('appends the request ID through the Schema template', () => {
    configureErrorReference('（错误 ID：{id}）');
    expect(withRequestReference('保存失败', { detail: 'x', request_id: 'req_0123456789abcdef' })).toBe('保存失败（错误 ID：req_0123456789abcdef）');
  });

  it('leaves the text alone without a template, an ID, or with a malformed ID', () => {
    expect(withRequestReference('保存失败', { request_id: 'req_0123456789abcdef' })).toBe('保存失败');
    configureErrorReference('（错误 ID：{id}）');
    expect(withRequestReference('保存失败', {})).toBe('保存失败');
    expect(withRequestReference('保存失败', { request_id: '<script>' })).toBe('保存失败');
  });
});

describe('request failures keep the server request ID (#240)', () => {
  it('reads the ID from the body, else the header, and appends it to the message', () => {
    configureErrorReference('（错误 ID：{id}）');
    const fromBody = responseRequestId(new Response('{}', { status: 404 }), { request_id: 'req_0123456789abcdef' });
    expect(fromBody).toBe('req_0123456789abcdef');
    const fromHeader = responseRequestId(new Response('{}', { status: 404, headers: { 'X-Request-ID': 'req_fedcba9876543210' } }), {});
    expect(fromHeader).toBe('req_fedcba9876543210');
    expect(responseRequestId(new Response('{}', { status: 404, headers: { 'X-Request-ID': 'not-an-id' } }), {})).toBe('');
    const failure = new RequestFailure('HTTP 429', fromHeader);
    expect(withErrorReference('去背景提交失败', failure)).toBe('去背景提交失败（错误 ID：req_fedcba9876543210）');
    expect(withErrorReference('去背景提交失败', new Error('x'))).toBe('去背景提交失败');
    configureErrorReference(undefined);
  });
});

describe('problemReason (#288)', () => {
  it('reads detail only when the server marks it user-facing', () => {
    expect(problemReason({ code: 'invalid_input', detail: ' 参考图过大 ', user_facing: true })).toBe('参考图过大');
    expect(problemReason({ code: 'invalid_input', detail: '请求参数无效' })).toBe('');
    expect(problemReason({ detail: '参考图过大', user_facing: 'true' })).toBe('');
    expect(problemReason({ detail: 42, user_facing: true })).toBe('');
    expect(problemReason(null)).toBe('');
  });

  it('is carried by a RequestFailure and nothing else', () => {
    expect(reasonOf(new RequestFailure('参考图过大', 'req_0123456789abcdef', '参考图过大'))).toBe('参考图过大');
    expect(reasonOf(new RequestFailure('HTTP 400'))).toBe('');
    expect(reasonOf(new Error('参考图过大'))).toBe('');
  });
});
