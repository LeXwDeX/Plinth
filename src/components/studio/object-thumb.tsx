'use client';

import { useState } from 'react';
import { Box, Image as ImageIcon } from 'lucide-react';

import { cn } from '@/lib/utils';

/**
 * #624: the small picture of an object (a version tile, the composer's object chip). A 3D version has a preview only
 * when its Task delivered one (a thumbnail asset); many processing steps (rigging, animation, resizing) deliver none.
 * With no preview, or a preview that does not load, the square shows the kind's icon on a muted ground, never an empty
 * black square or a broken picture. Fills its parent.
 */
export function ObjectThumbImage({ kind, src, className }: { kind: 'image' | 'model'; src: string; className?: string }) {
  const [failed, setFailed] = useState('');
  const shown = src !== '' && failed !== src;
  const Icon = kind === 'model' ? Box : ImageIcon;
  return (
    <span
      data-testid="object-thumb"
      data-state={shown ? 'image' : 'placeholder'}
      data-kind={kind}
      className={cn('flex size-full items-center justify-center', !shown && 'bg-muted text-muted-foreground', className)}
    >
      {shown
        ? <img src={src} alt={''} loading="lazy" decoding="async" onError={() => setFailed(src)} className="size-full object-cover" />
        : <Icon aria-hidden className="size-1/2 max-h-5 max-w-5" />}
    </span>
  );
}
