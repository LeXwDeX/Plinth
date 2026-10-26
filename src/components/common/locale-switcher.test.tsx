import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { LocaleSelect } from './locale-switcher';

const application = defaultCatalog.application as unknown as ProductSchema;
let schema: ProductSchema = application;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => schema.content.messages[key] ?? '' }),
}));

beforeEach(() => {
  schema = application;
});

it('offers the declared locales with their own names and the current one selected', () => {
  schema = { ...application, localization: { ...application.localization!, active_locale: 'en' } };
  const html = renderToStaticMarkup(<LocaleSelect />);
  expect(html).toContain('lang="en"');
  expect(html).toContain('English');
  expect(html).toContain(`aria-label="${application.content.messages['ui.common.language']}"`);
});

it('renders nothing for a single-locale Schema', () => {
  schema = { ...application, localization: undefined };
  expect(renderToStaticMarkup(<LocaleSelect />)).toBe('');
});
