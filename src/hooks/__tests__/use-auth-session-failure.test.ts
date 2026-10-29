import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { projectSessionFailure, SessionRequestError, sessionRetryDelayMs } from '../use-auth';

const retry = defaultCatalog.application.authentication.session_retry;

describe('session request failures (#62)', () => {
  it('treats 429 and other errors as failures, never as a signed-out visitor', () => {
    expect(projectSessionFailure(new SessionRequestError(429, 3), false)).toBe('rate_limited');
    expect(projectSessionFailure(new SessionRequestError(503, null), false)).toBe('unavailable');
    expect(projectSessionFailure(new TypeError('Failed to fetch'), false)).toBe('unavailable');
  });

  it('keeps 401 as the signed-out answer and ignores errors once a session was read', () => {
    expect(projectSessionFailure(new SessionRequestError(401, null), false)).toBeNull();
    expect(projectSessionFailure(new SessionRequestError(429, null), true)).toBeNull();
    expect(projectSessionFailure(undefined, false)).toBeNull();
  });

  it('backs off and honours Retry-After', () => {
    expect(sessionRetryDelayMs(new SessionRequestError(429, null), 0, retry)).toBe(retry.initial_delay_ms);
    expect(sessionRetryDelayMs(new SessionRequestError(429, null), 10, retry)).toBe(retry.max_backoff_ms);
    expect(sessionRetryDelayMs(new SessionRequestError(429, 45), 0, { ...retry, max_retry_after_ms: 60_000 })).toBe(45_000);
    expect(sessionRetryDelayMs(new SessionRequestError(429, 9_999), 0, retry)).toBe(retry.max_retry_after_ms);
  });

  it('has Schema text in both locales for the session failure notices', () => {
    const application = defaultCatalog.application as { content: { messages: Record<string, string> }; localization: { bundles: Record<string, { messages: Record<string, string> }> } };
    for (const key of ['ui.connectivity.session_rate_limited', 'ui.connectivity.session_unavailable']) {
      expect(application.content.messages[key]).toBeTruthy();
      expect(application.localization.bundles.en.messages[key]).toBeTruthy();
    }
  });
});
