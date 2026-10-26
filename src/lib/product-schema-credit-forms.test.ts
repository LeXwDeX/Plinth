import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { formatSchemaText } from '@/lib/product-schema';

const en = defaultCatalog.application.localization.bundles.en.messages as Record<string, string>;
const enFields = defaultCatalog.application.localization.bundles.en.fields as Record<string, string>;
const canvas = defaultCatalog.application.canvas.presentation.text as Record<string, string>;

function placeholders(text: string): string[] {
  return [...text.matchAll(/\{([^}]+)\}/g)].map((match) => match[1]).sort();
}

describe('credit copy', () => {
  it('keeps the credits dimension invariant for one and forty credits', () => {
    for (const credits of [1, 40]) {
      expect(formatSchemaText(en['ui.studio.composer.send_cost'], { credits }))
        .toBe(`Generate — credits: ${credits}`);
      expect(formatSchemaText(en['ui.studio.composer.send_cost_hint'], { credits }))
        .toBe(`Chat alone uses no credits; if it generates, estimated credits: ${credits}`);
      expect(formatSchemaText(en['ui.studio.composer.send_with_cost'], { credits }))
        .toBe(`Send. Chat alone uses no credits; if it generates, estimated credits: ${credits}`);
    }
  });

  it('does not pluralize credits by output count', () => {
    expect(formatSchemaText(en['ui.studio.params.cost_one'], { total: 1 })).toBe('Estimated credits: 1');
    expect(formatSchemaText(en['ui.studio.params.cost_one'], { total: 40 })).toBe('Estimated credits: 40');
    expect(formatSchemaText(en['ui.studio.params.cost_many'], { unit: 1, total: 1 }))
      .toBe('Credits per item: 1; total credits: 1');
  });

  it('keeps the export estimate invariant for one and many outputs', () => {
    for (const key of ['ui.generation_formats.estimate', 'ui.generation_formats.estimate_one']) {
      expect(formatSchemaText(en[key], { credits: 1, count: 1, per: 1 }))
        .toBe('Estimated additional export cost in credits: 1 (1 × 1)');
      expect(formatSchemaText(en[key], { credits: 40, count: 2, per: 20 }))
        .toBe('Estimated additional export cost in credits: 40 (2 × 20)');
    }
  });

  it('uses the singular image in the selected-history copy', () => {
    expect(en['ui.chat.input.history_reference_selected_one']).toBe('Reference history image ({count})');
  });
});

describe('canvas count pairs', () => {
  const keys = Object.keys(canvas).filter((key) => !key.endsWith('_one') && canvas[key].includes('{count}'));

  it.each(keys)('%s has a matching singular pair', (key) => {
    const oneKey = `${key}_one`;
    expect(canvas[oneKey]).toBeTruthy();
    expect(enFields[`canvas.presentation.text[${oneKey}]`]).toBeTruthy();
    expect(placeholders(canvas[oneKey])).toEqual(placeholders(canvas[key]));
    expect(placeholders(enFields[`canvas.presentation.text[${oneKey}]`]))
      .toEqual(placeholders(enFields[`canvas.presentation.text[${key}]`]));
  });

  it('names one generated or uploaded file, node and sent image', () => {
    expect(enFields['canvas.presentation.text[files.generated_count_one]']).toBe('AI-generated {count}');
    expect(enFields['canvas.presentation.text[files.uploaded_count_one]']).toBe('Uploaded {count}');
    expect(enFields['canvas.presentation.text[project.list.node_summary_one]']).toBe('{count} node · {time}');
    expect(enFields['canvas.presentation.text[success.image_sent_one]']).toBe('Sent {count} image to the chat');
  });
});
