'use client';

import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { useAnchoredPositioner } from '@/components/ui/overlay';

/** Gap between the handle and its preview, and between the preview and the window edge. */
const GAP_PIXELS = 12;

/** Where the preview box goes: beside the handle (right, else left), vertically centred on it and kept inside the window. */
export function previewPlacement(
  anchor: { left: number; right: number; top: number; bottom: number },
  size: number,
  window: { width: number; height: number },
): { left: number; top: number } {
  const besideRight = anchor.right + GAP_PIXELS;
  const besideLeft = anchor.left - GAP_PIXELS - size;
  const left = besideRight + size + GAP_PIXELS <= window.width
    ? besideRight
    : besideLeft >= GAP_PIXELS ? besideLeft : Math.max(GAP_PIXELS, window.width - size - GAP_PIXELS);
  const centre = (anchor.top + anchor.bottom) / 2;
  const top = Math.min(Math.max(GAP_PIXELS, centre - size / 2), Math.max(GAP_PIXELS, window.height - size - GAP_PIXELS));
  return { left, top };
}

export function previewBesideList(
  anchor: { top: number; bottom: number },
  list: { left: number; right: number },
  maximum: number,
  viewport: { width: number; height: number },
): { left: number; top: number; size: number } | null {
  const right = viewport.width - list.right - 2 * GAP_PIXELS;
  const left = list.left - 2 * GAP_PIXELS;
  const useRight = right >= maximum || right >= left;
  const size = Math.min(maximum, useRight ? right : left, viewport.height - 2 * GAP_PIXELS);
  if (size <= 34) return null;
  return {
    left: useRight ? list.right + GAP_PIXELS : list.left - GAP_PIXELS - size,
    top: Math.min(Math.max(GAP_PIXELS, (anchor.top + anchor.bottom - size) / 2), viewport.height - size - GAP_PIXELS),
    size,
  };
}

/**
 * #87: a picture handle in an Agent message ("@img25") shows its picture large while the pointer rests on
 * it. It opens after the Schema delay (`chat.frontend.handle_preview_delay_milliseconds`) and closes when
 * the pointer leaves or the page scrolls. The preview is drawn over everything but takes no pointer events,
 * so every click lands on what is under it. The picture is the downscaled preview copy (`previewAssetUrl`),
 * never the full-size original.
 */
export function HandlePreview({ handle, url, children, className, open, avoidRef }: {
  handle: string;
  /** The picture's platform address; empty means there is nothing to preview (a model handle). */
  url: string;
  children: ReactNode;
  className?: string;
  open?: boolean;
  avoidRef?: RefObject<HTMLElement | null>;
}) {
  const { schema } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const delay = schema.chat.frontend.handle_preview_delay_milliseconds;
  const size = schema.chat.frontend.handle_preview_maximum_pixels;
  const layer = useAnchoredPositioner();
  const anchorRef = useRef<HTMLSpanElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [position, setPosition] = useState<{ left: number; top: number; size: number } | null>(null);
  const [loaded, setLoaded] = useState(false);

  const cancel = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
  }, []);
  const close = useCallback(() => {
    cancel();
    setPosition(null);
  }, [cancel]);

  useEffect(() => cancel, [cancel]);
  const schedule = useCallback(() => {
    cancel();
    setLoaded(false);
    timer.current = setTimeout(() => {
      timer.current = null;
      const anchor = anchorRef.current?.getBoundingClientRect();
      if (!anchor) return;
      const viewport = { width: window.innerWidth, height: window.innerHeight };
      const list = avoidRef?.current?.getBoundingClientRect();
      setPosition(list ? previewBesideList(anchor, list, size, viewport) : { ...previewPlacement(anchor, size, viewport), size });
    }, delay);
  }, [avoidRef, cancel, delay, size]);
  useEffect(() => {
    if (open === undefined) return;
    if (open && url) schedule(); else close();
    return close;
  }, [open, url, schedule, close]);
  useEffect(() => {
    if (!position) return;
    // A scrolled chat moves the handle away from the preview: close rather than point at the wrong place.
    window.addEventListener('scroll', close, true);
    window.addEventListener('blur', close);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('blur', close);
      window.removeEventListener('resize', close);
    };
  }, [position, close]);

  if (!url) return <>{children}</>;
  // A platform picture is shown from its downscaled preview copy; only a picture with no platform address
  // (a pasted data URI) is shown as it is.
  const src = previewAssetUrl(url) || safePreviewURL(url);
  if (!src) return <>{children}</>;
  return (
    <span
      ref={anchorRef}
      className={className}
      data-testid="handle-preview-anchor"
      onPointerEnter={(event) => {
        if (open !== undefined || event.pointerType === 'touch') return;
        schedule();
      }}
      onPointerLeave={open === undefined ? close : undefined}
    >
      {children}
      {position && typeof document !== 'undefined' ? createPortal(
        <div
          aria-hidden
          data-testid="handle-preview"
          className={`pointer-events-none fixed ${layer} flex flex-col items-start gap-1 rounded-lg border border-border bg-popover p-1.5 shadow-lg`}
          style={{ left: position.left, top: position.top, maxWidth: position.size, maxHeight: position.size, visibility: loaded ? 'visible' : 'hidden' }}
        >
          <img
            src={src}
            alt={formatSchemaText(schemaText(schema, 'ui.studio.handle_preview.alt'), { handle })}
            decoding="async"
            onLoad={() => setLoaded(true)}
            onError={close}
            className="min-h-0 min-w-0 rounded-md object-contain"
            style={{ maxWidth: position.size - 14, maxHeight: position.size - 34 }}
          />
          <span className="px-0.5 font-mono text-[11px] leading-4 text-muted-foreground">{handle}</span>
        </div>,
        document.body,
      ) : null}
    </span>
  );
}
