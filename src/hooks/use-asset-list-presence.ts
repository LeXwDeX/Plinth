'use client';

import { useEffect, useRef } from 'react';

import { assetListPresenceURL, type AssetListClient } from '@/lib/asset-lists/client';
import type { ProductSchema } from '@/lib/product-schema';

/**
 * #328: the open browser tab's presence heartbeat for the project whose lists
 * it is showing. The server pauses automatic production when no tab has sent a
 * heartbeat within its grace period. Background tabs keep heartbeating on
 * purpose: the browser may throttle them and the server's grace covers the
 * gap, so a throttled tab still counts as present.
 */
export const PRESENCE_TAB_STORAGE_KEY = 'plinth.asset-list.tab';
const DEFAULT_HEARTBEAT_SECONDS = 15;

/** The part of the list client the heartbeat uses. */
export type PresenceClient = Pick<AssetListClient, 'presence'>;

export interface UseAssetListPresenceOptions {
  client: PresenceClient;
  /** The open session's project; nothing is sent while empty. */
  projectId: string | null;
  /** False on a Catalog without `asset_list.presence`: not one request is sent. */
  enabled: boolean;
  /** The Catalog the client was built with: heartbeat interval and beacon route. */
  schema?: ProductSchema;
  /** Overrides `asset_list.presence.heartbeat_seconds`; 15 seconds when neither says. */
  heartbeatSeconds?: number;
  /** Test seam. The real id lives in sessionStorage so every tab has its own. */
  tabId?: string;
}

function randomTabID(): string {
  const uuid = globalThis.crypto?.randomUUID?.();
  if (uuid) return uuid;
  return `tab-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

/** One id per tab: sessionStorage survives reloads in the tab and is not shared with another tab. */
function storedTabID(): string {
  try {
    const stored = window.sessionStorage.getItem(PRESENCE_TAB_STORAGE_KEY);
    if (stored) return stored;
    const created = randomTabID();
    window.sessionStorage.setItem(PRESENCE_TAB_STORAGE_KEY, created);
    return created;
  } catch {
    // Private mode or blocked storage: a per-mount id is still unique per tab.
    return randomTabID();
  }
}

export function useAssetListPresence(options: UseAssetListPresenceOptions): void {
  const { client, projectId, enabled, schema, heartbeatSeconds } = options;
  const tabRef = useRef<string | null>(null);
  // The pagehide listener and the effect cleanup both leave; the latest active
  // heartbeat registers itself here so pagehide can reach it.
  const leaveRef = useRef<(() => void) | null>(null);

  const configured = heartbeatSeconds ?? schema?.asset_list?.presence?.heartbeat_seconds ?? DEFAULT_HEARTBEAT_SECONDS;
  const intervalMs = (Number.isFinite(configured) && configured > 0 ? configured : DEFAULT_HEARTBEAT_SECONDS) * 1000;

  useEffect(() => {
    if (!enabled || !projectId) return;
    const tabID = tabRef.current ?? (tabRef.current = options.tabId || storedTabID());
    const beaconURL = schema ? assetListPresenceURL(schema, projectId) : '';
    let left = false;
    let stopped = false;

    // Presence is background bookkeeping: a failed request is retried by the
    // next tick, and nothing is logged.
    const call = (state: 'present' | 'leave') => {
      try {
        void Promise.resolve(client.presence(projectId, tabID, state)).catch(() => {});
      } catch {
        // A client that refuses to build the URL must not break the shell.
      }
    };
    const leave = () => {
      if (left) return;
      left = true;
      if (beaconURL && typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
        try {
          const body = new Blob([JSON.stringify({ tab_id: tabID, state: 'leave' })], { type: 'application/json' });
          if (navigator.sendBeacon(beaconURL, body)) return;
        } catch {
          // Beacon refused the request; the keepalive fetch below still runs.
        }
      }
      call('leave');
    };
    const tick = () => {
      if (stopped) return;
      // A page restored from the back/forward cache heartbeats again, so its
      // later unmount must leave again too.
      left = false;
      call('present');
    };

    tick();
    const timer = window.setInterval(tick, intervalMs);
    leaveRef.current = leave;

    // A project change or unmount leaves the old project before React starts
    // the next heartbeat effect for the new one.
    return () => {
      stopped = true;
      window.clearInterval(timer);
      leave();
      if (leaveRef.current === leave) leaveRef.current = null;
    };
  }, [client, enabled, projectId, intervalMs, options.tabId, schema]);

  useEffect(() => {
    if (!enabled) return;
    const onPageHide = () => leaveRef.current?.();
    window.addEventListener('pagehide', onPageHide);
    return () => window.removeEventListener('pagehide', onPageHide);
  }, [enabled]);
}