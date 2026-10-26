import * as React from 'react';

import { cn } from '@/lib/utils';

type SurfaceVariant = 'plain' | 'base' | 'panel' | 'inset' | 'card' | 'canvas' | 'muted' | 'soft' | 'raised' | 'frosted' | 'toolbar' | 'danger' | 'warning' | 'info' | 'success' | 'message-user' | 'message-agent';
type SurfaceRadius = 'none' | 'sm' | 'md' | 'lg' | 'xl';
type SurfacePadding = 'none' | 'sm' | 'md' | 'lg' | 'xl';
type SurfaceElevation = 'none' | 'sm' | 'lg' | 'xl';

const variants: Record<SurfaceVariant, string> = {
  plain: 'bg-panel',
  base: 'bg-surface',
  panel: 'border border-border bg-panel',
  inset: 'border border-border bg-surface',
  card: 'bg-card text-card-foreground ring-1 ring-foreground/10',
  canvas: 'border border-border bg-canvas',
  muted: 'border border-border bg-muted',
  soft: 'border border-border bg-muted/30',
  raised: 'border border-border bg-panel-raised',
  frosted: 'border border-border bg-panel/80 backdrop-blur',
  toolbar: 'border border-border bg-panel/95 backdrop-blur',
  danger: 'border border-destructive/30 bg-destructive/10 text-destructive',
  warning: 'border border-warning/30 bg-warning/10 text-warning',
  info: 'border border-info/30 bg-info/10 text-info',
  success: 'border border-success/30 bg-success/10 text-success',
  'message-user': 'bg-primary text-primary-foreground',
  'message-agent': 'bg-panel-raised text-foreground',
};

const radii: Record<SurfaceRadius, string> = {
  none: '',
  sm: 'rounded-sm',
  md: 'rounded-md',
  lg: 'rounded-lg',
  xl: 'rounded-xl',
};

const paddings: Record<SurfacePadding, string> = {
  none: '',
  sm: 'p-2',
  md: 'p-3',
  lg: 'p-4',
  xl: 'p-6',
};

const elevations: Record<SurfaceElevation, string> = {
  none: '',
  sm: 'shadow-sm',
  lg: 'shadow-lg',
  xl: 'shadow-xl',
};

type SurfaceProps = React.HTMLAttributes<HTMLElement> & {
  as?: 'div' | 'section' | 'article' | 'aside' | 'header' | 'nav' | 'form' | 'dl' | 'p';
  variant?: SurfaceVariant;
  radius?: SurfaceRadius;
  padding?: SurfacePadding;
  elevation?: SurfaceElevation;
};

/** Shared visual container. Layout and positioning remain the caller's responsibility. */
function Surface({
  as: Element = 'div',
  variant = 'panel',
  radius = 'lg',
  padding = 'none',
  elevation = 'none',
  className,
  ...props
}: SurfaceProps) {
  return (
    <Element
      data-slot="surface"
      data-variant={variant}
      className={cn(variants[variant], radii[radius], paddings[padding], elevations[elevation], className)}
      {...props}
    />
  );
}

export { Surface };
export type { SurfaceProps };
