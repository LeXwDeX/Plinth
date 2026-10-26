import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { KindBadge, KindBadgeFace, kindBadgeClass } from './kind-badge';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

it('shows a picture as the cyan 2D badge with its icon and the Schema text', () => {
  const html = renderToStaticMarkup(<KindBadge kind="image" />);
  expect(html).toContain('data-kind="image"');
  expect(html).toContain('bg-kind-2d-soft');
  expect(html).toContain('text-kind-2d');
  expect(html).toContain('>2D<');
  expect(html).toContain('<svg');
  expect(html).toContain('aria-hidden="true"');
  expect(html).not.toContain('kind-3d');
});

it('shows a model as the orange 3D badge', () => {
  const html = renderToStaticMarkup(<KindBadge kind="model" />);
  expect(html).toContain('data-kind="model"');
  expect(html).toContain('bg-kind-3d-soft');
  expect(html).toContain('text-kind-3d');
  expect(html).toContain('>3D<');
  expect(html).not.toContain('kind-2d');
});

it('is 11px with a 5px radius, one look for both kinds', () => {
  for (const kind of ['image', 'model'] as const) {
    const classes = kindBadgeClass(kind);
    expect(classes).toContain('text-[11px]');
    expect(classes).toContain('rounded-[min(var(--radius-md),5px)]');
  }
});

it('lets a control wear the badge look and keep its own behaviour', () => {
  const html = renderToStaticMarkup(<button type="button" className={kindBadgeClass('model')}><KindBadgeFace kind="model" /></button>);
  expect(html).toContain('text-kind-3d');
  expect(html).toContain('>3D<');
});

it('takes its texts from the Schema', () => {
  expect(messages['ui.studio.kind.badge_2d']).toBe('2D');
  expect(messages['ui.studio.kind.badge_3d']).toBe('3D');
});
