import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { Field } from './field';

it('connects a control with its label, help and error', () => {
  const html = renderToStaticMarkup(
    <Field id="prompt" label="Prompt" help="Describe the image" error="Required">
      {control => <input {...control} />}
    </Field>,
  );
  expect(html).toContain('<label for="prompt"');
  expect(html).toContain('aria-describedby="prompt-help prompt-error"');
  expect(html).toContain('aria-invalid="true"');
  expect(html).toContain('id="prompt-error" role="alert"');
});

it('uses a legend for a checkbox group', () => {
  const html = renderToStaticMarkup(
    <Field id="actions" label="Actions" help="Choose one" group>
      {() => <label><input type="checkbox" />Run</label>}
    </Field>,
  );
  expect(html).toContain('<fieldset');
  expect(html).toContain('<legend');
  expect(html).toContain('aria-describedby="actions-help"');
});

it('stacks label, control, help and error with a flex gap, not child margins', () => {
  // A child-margin stack lost the gap after the inline label and kept it after a
  // Select trigger followed by Base UI's out-of-flow hidden input, so a Select field
  // stood taller than a date field in the same filter row (#268). Flex gap does neither.
  const html = renderToStaticMarkup(
    <Field id="user" label="User" help="Pick one" error="Required">
      {control => <><button type="button" {...control} /><input type="hidden" /></>}
    </Field>,
  );
  const container = html.match(/^<div data-slot="field" class="([^"]*)"/)?.[1] ?? '';
  expect(container.split(' ')).toEqual(expect.arrayContaining(['flex', 'flex-col', 'gap-2']));
  expect(container).not.toMatch(/space-y-/);
  expect(html.indexOf('<label for="user"')).toBeLessThan(html.indexOf('<button'));
  expect(html.indexOf('<button')).toBeLessThan(html.indexOf('id="user-help"'));
  expect(html.indexOf('id="user-help"')).toBeLessThan(html.indexOf('id="user-error"'));
});

it('keeps the inline layout as a centred row', () => {
  const html = renderToStaticMarkup(
    <Field id="enabled" label="Enabled" layout="inline">
      {control => <input type="checkbox" {...control} />}
    </Field>,
  );
  expect(html).toMatch(/^<div data-slot="field" class="flex items-center gap-2">/);
  expect(html.indexOf('<input')).toBeLessThan(html.indexOf('<label for="enabled"'));
});
