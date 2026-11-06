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

it('keeps the inline control and label on one row, with the source and help on lines below (#112)', () => {
  const html = renderToStaticMarkup(
    <Field id="enabled" label="Enabled" layout="inline" meta={<span data-testid="meta">Default</span>} help="Scale to real size">
      {control => <input type="checkbox" {...control} />}
    </Field>,
  );
  expect(html).toMatch(/^<div data-slot="field" class="flex flex-col gap-1">/);
  const row = html.match(/<div class="(flex [^"]*items-center[^"]*)">(.*?)<\/div>/);
  expect(row?.[1]).toBeDefined();
  expect(row?.[2]).toContain('<input');
  expect(row?.[2]).toContain('<label for="enabled"');
  expect(row![2].indexOf('<input')).toBeLessThan(row![2].indexOf('<label for="enabled"'));
  // #147: a fixed 16px slot puts the label at pl-6 whatever size the browser draws the checkbox.
  expect(row![2]).toMatch(/^<span class="flex w-4 shrink-0"><input/);
  expect(row![2]).not.toContain('data-testid="meta"');
  expect(row![2]).not.toContain('id="enabled-help"');
  expect(html).toMatch(/<label for="enabled" class="[^"]*whitespace-nowrap/);
  const rowEnd = html.indexOf(row![0]) + row![0].length;
  expect(html.indexOf('data-testid="meta"')).toBeGreaterThan(rowEnd);
  expect(html.indexOf('data-testid="meta"')).toBeLessThan(html.indexOf('id="enabled-help"'));
});

it('places the source between the label and the control in a stacked field (#112)', () => {
  const html = renderToStaticMarkup(
    <Field id="quality" label="Quality" meta={<span data-testid="meta">Default</span>} help="Pick one">
      {control => <select {...control} />}
    </Field>,
  );
  expect(html.indexOf('<label for="quality"')).toBeLessThan(html.indexOf('data-testid="meta"'));
  expect(html.indexOf('data-testid="meta"')).toBeLessThan(html.indexOf('<select'));
  expect(html.indexOf('<select')).toBeLessThan(html.indexOf('id="quality-help"'));
});
