import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';

// #670: the toast region's accessible name came from the library default
// ("Notifications alt+T"), English inside the Chinese interface.
describe('toast region label', () => {
  it('reads its accessible name from the Schema in both languages', () => {
    const application = defaultCatalog.application as unknown as {
      content: { messages: Record<string, string> };
      localization: { bundles: { en: { messages: Record<string, string> } } };
    };
    expect(application.content.messages['ui.notifications.region']).toBe('通知');
    expect(application.localization.bundles.en.messages['ui.notifications.region']).toBe('Notifications');
    const source = readFileSync('web/src/main.tsx', 'utf8');
    expect(source).toContain("containerAriaLabel={schemaText(schema, 'ui.notifications.region')}");
  });
});
