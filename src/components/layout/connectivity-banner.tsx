'use client';

import { useEffect } from 'react';
import { WifiOff, LogIn } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { watchConnectivity } from '@/lib/connectivity-watch';
import { useConnectivityStore } from '@/store/connectivity-store';

/**
 * A strip at the top of the app while the browser is offline or the login has ended. Unsent drafts are kept
 * (composer drafts are stored per session), and event streams reconnect when the network is back.
 */
export function ConnectivityBanner() {
  const { schema, text } = useProductSchema();
  const online = useConnectivityStore((state) => state.online);
  const expired = useConnectivityStore((state) => state.sessionExpired);
  const loginPath = schema.runtime.http.legacy_routes.auth_login;
  useEffect(() => watchConnectivity(loginPath), [loginPath]);
  if (online && !expired) return null;
  const login = schema.runtime.frontend.client_routes.login;
  return (
    <div
      role="alert"
      data-testid="connectivity-banner"
      data-kind={online ? 'session-expired' : 'offline'}
      className="flex shrink-0 items-center justify-center gap-2 bg-warning px-3 py-1.5 text-xs font-medium text-primary-foreground"
    >
      {online ? <LogIn aria-hidden className="size-3.5" /> : <WifiOff aria-hidden className="size-3.5" />}
      <span>{text(online ? 'ui.connectivity.session_expired' : 'ui.connectivity.offline')}</span>
      {online ? <a href={login} className="underline underline-offset-2" data-testid="connectivity-relogin">{text('ui.connectivity.relogin')}</a> : null}
    </div>
  );
}
