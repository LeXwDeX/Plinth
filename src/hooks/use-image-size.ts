'use client';

import { useEffect, useState } from 'react';

export interface ImageSize {
  width: number;
  height: number;
}

/**
 * The real pixel size of a picture. A size the server already knows (a screenshot's) is used as it
 * is; otherwise the picture is loaded once to read it. Empty until known, and for a picture that
 * cannot be read.
 */
export function useImageSize(url: string, known: ImageSize | null = null): ImageSize | null {
  const [measured, setMeasured] = useState<{ url: string; size: ImageSize } | null>(null);
  useEffect(() => {
    if (known || !url) return;
    let cancelled = false;
    const image = new Image();
    image.onload = () => {
      if (!cancelled && image.naturalWidth > 0 && image.naturalHeight > 0) {
        setMeasured({ url, size: { width: image.naturalWidth, height: image.naturalHeight } });
      }
    };
    image.src = url;
    return () => { cancelled = true; image.onload = null; };
  }, [known, url]);
  if (known) return known;
  return measured && measured.url === url ? measured.size : null;
}
