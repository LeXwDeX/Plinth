import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  configureClientErrorReporting,
  installRequestIdTracking,
  lastRequestIdSeen,
  reportClientError,
  resetClientErrorReportingForTest,
} from '@/lib/client-error-report';

describe('client error reporting (#233)', () => {
  const fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
  beforeEach(() => {
    resetClientErrorReportingForTest();
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('window', { location: { pathname: '/workspace' } });
    fetchMock.mockClear();
  });
  afterEach(() => vi.unstubAllGlobals());

  it('sends nothing until the Schema route is configured', () => {
    reportClientError('window-error', new Error('boom'));
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('posts the error to the configured route once per dedupe window', () => {
    configureClientErrorReporting('/api/v1/client-errors', 'abc123');
    reportClientError('app-error-boundary', new Error('boom'), { stack: 'at Panel', requestId: 'req_1' });
    reportClientError('app-error-boundary', new Error('boom'));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/client-errors');
    const body = JSON.parse(String(init.body));
    expect(body).toMatchObject({ kind: 'app-error-boundary', message: 'boom', page: '/workspace', revision: 'abc123', request_id: 'req_1' });
    expect(body.stack).toContain('at Panel');
  });

  it('rejects external routes and caps reports per page load', () => {
    configureClientErrorReporting('https://evil.test/collect', 'x');
    reportClientError('window-error', new Error('one'));
    expect(fetchMock).not.toHaveBeenCalled();
    configureClientErrorReporting('/api/v1/client-errors', 'x');
    for (let index = 0; index < 30; index += 1) reportClientError('window-error', new Error(`e${index}`));
    expect(fetchMock).toHaveBeenCalledTimes(20);
  });
});

describe('client error reports name the request before them (#387)', () => {
  const route = '/api/v1/client-errors';
  const pageRequest = 'req_00000000000000a1';
  const laterRequest = 'req_00000000000000b2';
  const reportRequest = 'req_00000000000000c3';
  const tagged = (id: string, status = 200) => new Response(null, { status, headers: { 'X-Request-ID': id } });
  const postedBody = (mock: ReturnType<typeof vi.fn>) => JSON.parse(String((mock.mock.calls.at(-1) as unknown as [string, RequestInit])[1].body));

  beforeEach(() => {
    resetClientErrorReportingForTest();
    vi.stubGlobal('window', { location: { pathname: '/workspace' } });
  });
  afterEach(() => {
    resetClientErrorReportingForTest();
    vi.unstubAllGlobals();
  });

  it('remembers the X-Request-ID of the latest response and hands every response through untouched', async () => {
    const first = tagged(pageRequest);
    const second = tagged(laterRequest);
    const native = vi.fn().mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    vi.stubGlobal('fetch', native);
    const uninstall = installRequestIdTracking();
    expect(lastRequestIdSeen()).toBe('');

    expect(await fetch('/api/v1/tasks')).toBe(first);
    expect(lastRequestIdSeen()).toBe(pageRequest);
    expect(await fetch('/api/v1/session', { method: 'POST' })).toBe(second);
    expect(lastRequestIdSeen()).toBe(laterRequest);
    expect(native).toHaveBeenNthCalledWith(2, '/api/v1/session', { method: 'POST' });

    uninstall();
    expect(fetch).toBe(native);
  });

  it('keeps the previous ID when a request fails or a response has no usable ID', async () => {
    const native = vi.fn()
      .mockResolvedValueOnce(tagged(pageRequest))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValueOnce(new Response(null, { status: 502 }))
      .mockResolvedValueOnce(tagged('not a request id, forged log text'));
    vi.stubGlobal('fetch', native);
    installRequestIdTracking();

    await fetch('/api/v1/tasks');
    await expect(fetch('/api/v1/tasks')).rejects.toThrow('Failed to fetch');
    await fetch('/api/v1/tasks');
    await fetch('/api/v1/tasks');
    expect(lastRequestIdSeen()).toBe(pageRequest);
  });

  it('installs once and leaves a replaced fetch alone when it is removed', () => {
    const native = vi.fn();
    vi.stubGlobal('fetch', native);
    const uninstall = installRequestIdTracking();
    const wrapper = fetch;
    expect(wrapper).not.toBe(native);
    installRequestIdTracking()();
    expect(fetch).toBe(wrapper);

    const replacement = vi.fn();
    vi.stubGlobal('fetch', replacement);
    uninstall();
    expect(fetch).toBe(replacement);
  });

  it('sends the remembered ID with an error report that has none of its own', async () => {
    const native = vi.fn(async (url: string) => tagged(url === route ? reportRequest : pageRequest, url === route ? 204 : 200));
    vi.stubGlobal('fetch', native);
    configureClientErrorReporting(route, 'rev1');
    installRequestIdTracking();
    await fetch('/api/v1/tasks');

    reportClientError('app-error-boundary', new Error('boom'));
    expect(postedBody(native).request_id).toBe(pageRequest);
    await new Promise((resolve) => setTimeout(resolve, 0));
    // The report's own response carries an ID too; it must not displace the request being reported on.
    expect(lastRequestIdSeen()).toBe(pageRequest);

    reportClientError('window-error', new Error('later'));
    expect(postedBody(native).request_id).toBe(pageRequest);
  });

  it('lets a request ID the caller knows win over the remembered one, and sends none before any request', async () => {
    const native = vi.fn(async () => tagged(pageRequest));
    vi.stubGlobal('fetch', native);
    configureClientErrorReporting(route, 'rev1');
    installRequestIdTracking();

    reportClientError('window-error', new Error('first'));
    expect(postedBody(native).request_id).toBe('');

    await fetch('/api/v1/tasks');
    reportClientError('model-viewer-load:fetch', new Error('known'), { requestId: laterRequest });
    expect(postedBody(native).request_id).toBe(laterRequest);
  });
});
