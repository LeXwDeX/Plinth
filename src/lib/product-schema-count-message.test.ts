import { describe, expect, it } from 'vitest';

import { isSingularCount, schemaCountMessage, type ProductSchema } from '@/lib/product-schema';

function schemaWith(locale: string, messages: Record<string, string>): ProductSchema {
  return {
    localization: {
      default_locale: locale,
      active_locale: locale,
      locales: [],
      preference: { cookie: 'ui_locale', max_age_seconds: 1 },
    },
    content: { messages },
    runtime: { frontend: { display_locale: locale } },
  } as unknown as ProductSchema;
}

const messages = {
  'ui.test.count': '{count} items',
  'ui.test.count_one': '{count} item',
};

describe('schemaCountMessage singular choice', () => {
  it('uses the singular text for one in English and the general text for other counts', () => {
    const schema = schemaWith('en', messages);
    expect(schemaCountMessage(schema, 'ui.test.count', 1, { count: 1 })).toBe('1 item');
    expect(schemaCountMessage(schema, 'ui.test.count', 2, { count: 2 })).toBe('2 items');
  });

  it('keeps the general text in Chinese, which has no one category for this count', () => {
    const schema = schemaWith('zh-CN', { 'ui.test.count': '共 {count} 项' });
    expect(schemaCountMessage(schema, 'ui.test.count', 1, { count: 1 })).toBe('共 1 项');
  });

  it('falls back to the count === 1 rule for an invalid locale tag', () => {
    const schema = schemaWith('not a locale', messages);
    expect(isSingularCount('not a locale', 1)).toBe(true);
    expect(isSingularCount('not a locale', 2)).toBe(false);
    expect(schemaCountMessage(schema, 'ui.test.count', 1, { count: 1 })).toBe('1 item');
    expect(schemaCountMessage(schema, 'ui.test.count', 2, { count: 2 })).toBe('2 items');
  });

  it('falls back to the general text when the Schema declares no singular text', () => {
    const schema = schemaWith('en', { 'ui.test.count': '{count} items' });
    expect(schemaCountMessage(schema, 'ui.test.count', 1, { count: 1 })).toBe('1 items');
  });

  it('keeps the other placeholders beside {count} when the two forms share a format', () => {
    const schema = schemaWith('en', {
      'ui.test.estimate': 'Cost: {credits} credits ({count} × {per})',
      'ui.test.estimate_one': 'Cost: {credits} credits ({count} × {per})',
    });
    expect(schemaCountMessage(schema, 'ui.test.estimate', 1, { credits: 2, count: 1, per: 3 }))
      .toBe('Cost: 2 credits (1 × 3)');
  });
});
