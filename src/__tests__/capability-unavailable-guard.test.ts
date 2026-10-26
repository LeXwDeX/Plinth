import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';

const unavailableKey = 'ui.capability_unavailable';

describe('后处理能力不可用守卫', () => {
  it('Catalog 声明不可用原因文案，且说明网关配置项', () => {
    const messages = (
      defaultCatalog.application.content as { messages: Record<string, string> }
    ).messages;
    const value = messages[unavailableKey];
    expect(value).toBeTruthy();
    expect(value).toContain('PUBLIC_BASE_URL');
    expect(value).toContain('ASSET_SIGNING_KEY');
  });
});
