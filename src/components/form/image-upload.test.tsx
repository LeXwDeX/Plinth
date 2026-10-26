import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { declaredFormats, ImageUpload } from './image-upload';

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: defaultCatalog.application,
    text: (key: string) => defaultCatalog.application.content.messages[
      key as keyof typeof defaultCatalog.application.content.messages
    ],
  }),
}));

afterEach(() => vi.unstubAllGlobals());

it('allows selecting BMP references in the shared image upload', () => {
  vi.stubGlobal('React', React);
  const markup = renderToStaticMarkup(<ImageUpload onUpload={() => {}} />);
  const accept = markup.match(/accept="([^"]+)"/)?.[1].split(',');
  expect(accept).toContain('.bmp');
  expect(accept).toContain('image/bmp');
});

it('exposes a keyboard operable upload trigger tied to its field', () => {
  const markup = renderToStaticMarkup(<ImageUpload id="reference" aria-describedby="reference-help" onUpload={() => {}} />);
  expect(markup).toContain('<button type="button"');
  expect(markup).toContain('aria-describedby="reference-help"');
  expect(markup).toContain('<input id="reference" type="file"');
});

// #329 follow-up: an upload control for a declared parameter lists that parameter's Schema media types; the general
// attachment control (no target parameter yet) keeps its general text.
it('states only the formats the target parameter declares', () => {
  vi.stubGlobal('React', React);
  const markup = renderToStaticMarkup(<ImageUpload accept="image/png,image/jpeg,image/webp" onUpload={() => {}} />);
  expect(markup).toContain('支持 PNG、JPG、WebP，最大');
  expect(markup).not.toContain('GIF');
  expect(markup).not.toContain('BMP');
});

it('keeps the general format text when no target parameter is known', () => {
  vi.stubGlobal('React', React);
  const markup = renderToStaticMarkup(<ImageUpload onUpload={() => {}} />);
  expect(markup).toContain('支持 JPG、PNG、WebP、GIF、BMP，BMP 自动转为 PNG，最大');
});

it('names declared formats from the Schema and falls back to the subtype', () => {
  const schema = defaultCatalog.application as unknown as Parameters<typeof declaredFormats>[0];
  expect(declaredFormats(schema, 'image/png, image/jpeg,image/png')).toBe('PNG、JPG');
  expect(declaredFormats(schema, 'image/avif')).toBe('AVIF');
  expect(declaredFormats(schema, undefined)).toBe('');
  expect(declaredFormats(schema, '')).toBe('');
});
