'use client';

import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText, schemaText } from '@/lib/product-schema';

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

/**
 * #87: a picture handle in an Agent message ("@img25") shows its picture large while the pointer rests on
 * it. It opens after the Schema delay (`chat.frontend.handle_preview_delay_milliseconds`) and closes when
 * the pointer leaves or the page scrolls. The preview is drawn over everything but takes no pointer events,
 * so every click lands on what is under it. The picture is the downscaled preview copy (`previewAssetUrl`),
 * never the full-size original.
 */
export function HandlePreview({ handle, url, children, className }: {
  handle: string;
  /** The picture's platform address; empty means there is nothing to preview (a model handle). */
  url: string;
  children: ReactNode;
  className?: string;
}) {
  const { schema } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const delay = schema.chat.frontend.handle_preview_delay_milliseconds;
  const size = schema.chat.frontend.handle_preview_maximum_pixels;
  const anchorRef = useRef<HTMLSpanElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null);

  const cancel = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
  }, []);
  const close = useCallback(() => {
    cancel();
    setPosition(null);
  }, [cancel]);

  useEffect(() => cancel, [cancel]);
  useEffect(() => {
    if (!position) return;
    // A scrolled chat moves the handle away from the preview: close rather than point at the wrong place.
    window.addEventListener('scroll', close, true);
    window.addEventListener('blur', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('blur', close);
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
        if (event.pointerType === 'touch') return;
        cancel();
        timer.current = setTimeout(() => {
          timer.current = null;
          const anchor = anchorRef.current?.getBoundingClientRect();
          if (!anchor) return;
          setPosition(previewPlacement(anchor, size, { width: window.innerWidth, height: window.innerHeight }));
        }, delay);
      }}
      onPointerLeave={close}
    >
      {children}
      {position && typeof document !== 'undefined' ? createPortal(
        <div
          aria-hidden
          data-testid="handle-preview"
          className="pointer-events-none fixed z-float flex flex-col items-start gap-1 rounded-lg border border-border bg-popover p-1.5 shadow-lg"
          style={{ left: position.left, top: position.top, maxWidth: size, maxHeight: size }}
        >
          <img
            src={src}
            alt={formatSchemaText(schemaText(schema, 'ui.studio.handle_preview.alt'), { handle })}
            decoding="async"
            className="min-h-0 min-w-0 rounded-md object-contain"
            style={{ maxWidth: size - 14, maxHeight: size - 34 }}
          />
          <span className="px-0.5 font-mono text-[11px] leading-4 text-muted-foreground">{handle}</span>
        </div>,
        document.body,
      ) : null}
    </span>
  );
}
