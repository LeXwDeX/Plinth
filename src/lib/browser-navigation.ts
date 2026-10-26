import { useMemo, useSyncExternalStore } from 'react';

const navigationEvent = 'art-ai:navigate';

function subscribe(callback: () => void): () => void {
  window.addEventListener('popstate', callback);
  window.addEventListener(navigationEvent, callback);
  return () => {
    window.removeEventListener('popstate', callback);
    window.removeEventListener(navigationEvent, callback);
  };
}

function snapshot(): string {
  return window.location.href;
}

function navigate(href: string, replace: boolean): void {
  if (replace) {
    window.history.replaceState(null, '', href);
  } else {
    window.history.pushState(null, '', href);
  }
  window.dispatchEvent(new Event(navigationEvent));
}

export function usePathname(): string {
  const href = useSyncExternalStore(subscribe, snapshot, snapshot);
  return new URL(href).pathname;
}

export function useSearchParams(): URLSearchParams {
  const href = useSyncExternalStore(subscribe, snapshot, snapshot);
  return useMemo(() => new URL(href).searchParams, [href]);
}

export function useRouter() {
  return useMemo(
    () => ({
      push: (href: string) => navigate(href, false),
      replace: (href: string) => navigate(href, true),
      refresh: () => window.location.reload(),
      back: () => window.history.back(),
      forward: () => window.history.forward(),
    }),
    [],
  );
}
