import { describe, expect, it } from 'vitest';

import { isSessionExpiredResponse } from './connectivity-watch';
import { useConnectivityStore } from '@/store/connectivity-store';

const base = { origin: 'http://x.test', loginPath: '/api/auth/login' };

describe('connectivity', () => {
  it('reads a 401 from the own API as an ended login, except the sign-in call', () => {
    expect(isSessionExpiredResponse({ ...base, status: 401, url: '/api/v1/chat/sessions' })).toBe(true);
    expect(isSessionExpiredResponse({ ...base, status: 401, url: '/api/auth/login' })).toBe(false);
    expect(isSessionExpiredResponse({ ...base, status: 401, url: 'https://other.test/api/x' })).toBe(false);
    expect(isSessionExpiredResponse({ ...base, status: 403, url: '/api/x' })).toBe(false);
  });
  it('raises a reconnect tick only when the network returns', () => {
    const store = useConnectivityStore;
    store.setState({ online: true, reconnectTick: 0, sessionExpired: false });
    store.getState().setOnline(false);
    expect(store.getState().reconnectTick).toBe(0);
    store.getState().setOnline(true);
    expect(store.getState().reconnectTick).toBe(1);
    store.getState().setOnline(true);
    expect(store.getState().reconnectTick).toBe(1);
  });
});
