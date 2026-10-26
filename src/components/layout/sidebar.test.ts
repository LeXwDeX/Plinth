import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;

function registeredIcons(): Set<string> {
  const source = readFileSync(
    new URL('../../components/layout/sidebar.tsx', import.meta.url),
    'utf8',
  );
  const registry = /const navigationIcons: Record<string, LucideIcon> = \{([\s\S]*?)\n\};/.exec(
    source,
  );
  if (!registry) throw new Error('navigationIcons registry not found');
  return new Set(
    [...registry[1].matchAll(/^\s{2}'?([a-z-]+)'?:/gm)].map((match) => match[1]),
  );
}

describe('sidebar navigation icons', () => {
  it('registers an icon for every navigation icon the Schema declares', () => {
    const registered = registeredIcons();
    for (const item of schema.content.navigation) {
      expect(registered, `navigation ${item.key} 缺少图标登记`).toContain(
        item.icon,
      );
    }
  });

  it('keeps the workspace, account and admin destinations visible on the round trip', () => {
    expect(
      schema.content.navigation.map((item) => ({
        key: item.key,
        href: item.href,
        placement: item.placement,
        admin_only: item.admin_only === true,
      })),
    ).toEqual([
      { key: 'workspace', href: '/', placement: 'main', admin_only: false },
      { key: 'admin', href: '/admin', placement: 'bottom', admin_only: true },
      { key: 'account', href: '/account', placement: 'main', admin_only: false },
    ]);
  });
});
