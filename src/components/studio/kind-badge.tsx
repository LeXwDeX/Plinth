'use client';

import { Box, Image as ImageGlyph } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

/** What a version or object is: a picture (2D) or a model (3D). */
export type BadgeKind = 'image' | 'model';

const BADGE_TEXT_KEY: Record<BadgeKind, string> = {
  image: 'ui.studio.kind.badge_2d',
  model: 'ui.studio.kind.badge_3d',
};

/**
 * The one way a 2D / 3D kind is shown (UI spec §2): icon plus "2D" or "3D", 11px, 5px radius, the kind's
 * soft background with the kind's colour on icon and text. Cyan means 2D and orange means 3D, nothing else.
 * Shared by the badge itself and by controls that must look like it (the result card's 3D preview toggle).
 */
export function kindBadgeClass(kind: BadgeKind): string {
  return cn(
    'inline-flex shrink-0 items-center gap-1 rounded-[min(var(--radius-md),5px)] px-1.5 py-px text-[11px] font-semibold leading-4 [&_svg]:size-3 [&_svg]:shrink-0',
    kind === 'model' ? 'bg-kind-3d-soft text-kind-3d' : 'bg-kind-2d-soft text-kind-2d',
  );
}

export function KindBadgeFace({ kind }: { kind: BadgeKind }) {
  const { schema } = useProductSchema();
  const Icon = kind === 'model' ? Box : ImageGlyph;
  return (
    <>
      <Icon aria-hidden />
      {schemaText(schema, BADGE_TEXT_KEY[kind])}
    </>
  );
}

export function KindBadge({ kind, className }: { kind: BadgeKind; className?: string }) {
  return (
    <span className={cn(kindBadgeClass(kind), className)} data-testid="kind-badge" data-kind={kind}>
      <KindBadgeFace kind={kind} />
    </span>
  );
}
