'use client';

import { useState } from 'react';

import { ChipSquare } from '@/components/studio/composer-chips';
import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useImageSize } from '@/hooks/use-image-size';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatSchemaText } from '@/lib/product-schema';

/**
 * The attachment chip's thumbnail. Pointing at it (or focusing the chip) shows the picture large with its real
 * pixel size, so what is about to be sent can be checked before it is sent. The size is only read once the
 * preview is asked for.
 *
 * The chip itself shows the server preview of a platform picture (#290/#292); the hover view and the pixel size
 * read the original. A picture not yet uploaded is a raster data address and is shown as it is.
 */
export function AttachmentThumb({ url, alt }: { url: string; alt: string }) {
  const { text } = useProductSchema();
  const { previewAssetUrl } = useAssetURLRewriter();
  const [active, setActive] = useState(false);
  const original = safePreviewURL(url);
  const thumbnail = previewAssetUrl(url) || original;
  const size = useImageSize(active ? original : '');
  return (
    <span
      className="relative flex"
      data-testid="attachment-thumb"
      onMouseEnter={() => setActive(true)}
      onMouseLeave={() => setActive(false)}
      onFocus={() => setActive(true)}
      onBlur={() => setActive(false)}
    >
      <ChipSquare>
        {thumbnail ? <img src={thumbnail} alt={alt} className="size-full object-cover" /> : null}
      </ChipSquare>
      {active ? (
        <span
          role="tooltip"
          data-testid="attachment-hover-preview"
          className="pointer-events-none absolute bottom-full left-0 z-float mb-2 flex w-max max-w-[18rem] flex-col gap-1 rounded-lg border border-border bg-popover p-1.5 shadow-lg"
        >
          {original ? <img src={original} alt={alt} className="max-h-60 w-auto max-w-full rounded-sm object-contain" /> : null}
          {size ? (
            <span className="text-[11px] tabular-nums text-muted-foreground" data-testid="attachment-pixel-size">
              {formatSchemaText(text('ui.studio.composer.attachment.pixels'), { width: size.width, height: size.height })}
            </span>
          ) : null}
        </span>
      ) : null}
    </span>
  );
}
