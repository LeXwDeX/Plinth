import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { Action } from './button';

it('disables a loading action and exposes busy state while keeping its name', () => {
  const html = renderToStaticMarkup(<Action loading icon={<span>+</span>}>Create</Action>);
  expect(html).toContain('disabled=""');
  expect(html).toContain('aria-busy="true"');
  expect(html).toContain('Create');
  expect(html).not.toContain('>+</span>');
});

it('renders an icon as decoration', () => {
  const html = renderToStaticMarkup(<Action icon={<span>+</span>}>Create</Action>);
  expect(html).toContain('aria-hidden="true"');
  expect(html).toContain('Create');
});
