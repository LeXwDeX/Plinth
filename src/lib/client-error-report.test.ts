import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  configureClientErrorReporting,
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
