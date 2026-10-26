import { describe, expect, it } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { activeLocale, localeOptions, localePreferenceCookie } from './locale';
import { formatDateTime, formatNumber } from './format';

function schemaWith(localization: ProductSchema['localization']): ProductSchema {
  return { runtime: { frontend: { display_locale: 'zh-CN' } }, localization } as unknown as ProductSchema;
}

const declared = {
  default_locale: 'zh-CN',
  active_locale: 'en',
  locales: [{ id: 'zh-CN', label: '简体中文' }, { id: 'en', label: 'English' }],
  preference: { cookie: 'plinth_locale', max_age_seconds: 31536000 },
};

describe('UI locale', () => {
  it('reads the locale the server projected', () => {
    expect(activeLocale(schemaWith(declared))).toBe('en');
  });

  it('falls back to the default and then display_locale', () => {
    expect(activeLocale(schemaWith({ ...declared, active_locale: undefined }))).toBe('zh-CN');
    expect(activeLocale(schemaWith(undefined))).toBe('zh-CN');
    expect(localeOptions(schemaWith(undefined))).toEqual([]);
  });

  it('saves a declared locale in the Schema-named cookie', () => {
    expect(localePreferenceCookie(schemaWith(declared), 'zh-CN', false))
      .toBe('plinth_locale=zh-CN; Path=/; Max-Age=31536000; SameSite=Lax');
    expect(localePreferenceCookie(schemaWith(declared), 'en', true)).toMatch(/; Secure$/);
  });

  it('refuses locales the Schema does not declare', () => {
    expect(localePreferenceCookie(schemaWith(declared), 'fr', false)).toBeNull();
    expect(localePreferenceCookie(schemaWith(undefined), 'en', false)).toBeNull();
  });
});

describe('locale-aware formatting', () => {
  it('formats numbers in the requested locale', () => {
    expect(formatNumber(1234567.5, 'en')).toBe('1,234,567.5');
    expect(formatNumber(1234567.5, 'de')).toBe('1.234.567,5');
  });

  it('formats dates in the requested locale and drops invalid ones', () => {
    const value = Date.UTC(2026, 9, 20, 12);
    expect(formatDateTime(value, 'en', { timeZone: 'UTC', month: 'long' })).toBe('October');
    expect(formatDateTime(value, 'zh-CN', { timeZone: 'UTC', month: 'long' })).toBe('十月');
    expect(formatDateTime('not a date', 'en')).toBe('');
  });
});
