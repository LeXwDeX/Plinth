import type { ProductSchema } from '@/lib/product-schema';

/** One selectable UI locale, as the Product Schema declares it. */
export interface LocaleOption {
  id: string;
  /** The locale's own name ("English", "简体中文"), never translated. */
  label: string;
}

/**
 * The locale this Schema projection is written in. The server picks it per
 * request (saved choice → Accept-Language → default), so the browser reads it
 * instead of guessing; `html lang` and Intl formatting follow it.
 */
export function activeLocale(schema: ProductSchema): string {
  return schema.localization?.active_locale
    ?? schema.localization?.default_locale
    ?? schema.runtime.frontend.display_locale;
}

/** The locales a person can switch between; fewer than two means no switcher. */
export function localeOptions(schema: ProductSchema): LocaleOption[] {
  return schema.localization?.locales ?? [];
}

/**
 * The `document.cookie` assignment that saves a locale choice, or null for a
 * locale the Schema does not declare. The cookie is the "saved choice" the
 * server reads before Accept-Language.
 */
export function localePreferenceCookie(schema: ProductSchema, locale: string, secure: boolean): string | null {
  const preference = schema.localization?.preference;
  if (!preference || !localeOptions(schema).some((option) => option.id === locale)) return null;
  return [
    `${encodeURIComponent(preference.cookie)}=${encodeURIComponent(locale)}`,
    'Path=/',
    `Max-Age=${preference.max_age_seconds}`,
    'SameSite=Lax',
    ...(secure ? ['Secure'] : []),
  ].join('; ');
}

/**
 * Saves the choice and reloads, so the shell, `html lang` and every piece of
 * copy come from the server's projection in that locale at once.
 */
export function chooseLocale(schema: ProductSchema, locale: string): void {
  if (locale === activeLocale(schema)) return;
  const cookie = localePreferenceCookie(schema, locale, window.location.protocol === 'https:');
  if (!cookie) return;
  document.cookie = cookie;
  window.location.reload();
}
