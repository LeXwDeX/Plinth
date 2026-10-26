import { useConnectivityStore } from '@/store/connectivity-store';

/** A response that means the login ended: 401 from the app's own API, other than the sign-in call itself. */
export function isSessionExpiredResponse(input: { status: number; url: string; origin: string; loginPath: string }): boolean {
  if (input.status !== 401) return false;
  let url: URL;
  try { url = new URL(input.url, input.origin); } catch { return false; }
  if (url.origin !== input.origin || !url.pathname.startsWith('/api/')) return false;
  return url.pathname !== input.loginPath;
}

/**
 * Watches the page's own API calls for 401 and the browser's online/offline events, and reports both to the
 * connectivity store. Returns the function that undoes it. The wrapped fetch returns every response unchanged.
 */
export function watchConnectivity(loginPath: string): () => void {
  const store = useConnectivityStore.getState;
  const onOnline = () => store().setOnline(true);
  const onOffline = () => store().setOnline(false);
  store().setOnline(navigator.onLine !== false);
  window.addEventListener('online', onOnline);
  window.addEventListener('offline', onOffline);
  const original = window.fetch;
  const wrapped: typeof window.fetch = async (input, init) => {
    const response = await original.call(window, input, init);
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    if (isSessionExpiredResponse({ status: response.status, url, origin: window.location.origin, loginPath })) store().markSessionExpired();
    return response;
  };
  window.fetch = wrapped;
  return () => {
    window.removeEventListener('online', onOnline);
    window.removeEventListener('offline', onOffline);
    if (window.fetch === wrapped) window.fetch = original;
  };
}
