import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { safeAssetURL, safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';

const read = (path: string) => readFileSync(path, 'utf8');

describe('browser asset URLs stay same-origin', () => {
  it('drops remote and legacy proxy URLs', () => {
    expect(safeAssetURL('https://cdn.example.com/a.png')).toBe('');
    expect(safeAssetURL('//cdn.example.com/a.png')).toBe('');
    expect(safeAssetURL('/api/meshy/proxy-asset?url=x')).toBe('');
    expect(safeAssetURL('/api/v1/tasks/t/assets/a')).toBe('/api/v1/tasks/t/assets/a');
  });

  it('previews composer attachment data URIs but still drops remote URLs', () => {
    const attachment = 'data:image/png;base64,iVBORw0KGgo=';
    expect(safePreviewURL(attachment)).toBe(attachment);
    expect(safePreviewURL('data:image/svg+xml;base64,PHN2Zz4=')).toBe('');
    expect(safePreviewURL('data:text/html;base64,PGI+')).toBe('');
    expect(safePreviewURL('https://cdn.example.com/a.png')).toBe('');
    expect(safePreviewURL('/api/v1/tasks/t/assets/a')).toBe('/api/v1/tasks/t/assets/a');
    expect(safeAssetURL(attachment)).toBe('');
  });

  it.each([
    ['src/components/result/result-detail.tsx', [/safePreviewURL\(displayTarget\.url\)/]],
    ['src/components/studio/lightbox.tsx', [/href=\{safeAssetURL\(item\.downloadUrl\)\}/, /src=\{safeAssetURL\(item\.url\)\}/]],
    ['src/components/studio/compare-view.tsx', [/src=\{safeAssetURL\(slot\.url\)\}/, /src=\{safeAssetURL\(before\.url\)\}/, /src=\{safeAssetURL\(after\.url\)\}/]],
    ['src/components/studio/stage.tsx', [/url: safeAssetURL\(slot\.url\)/, /downloadUrl: safeAssetURL\(slot\.url\)/]],
  ])('%s routes every rendered URL through safeAssetURL', (path, patterns) => {
    const source = read(path);
    for (const pattern of patterns) expect(source).toMatch(pattern);
    expect(source).not.toMatch(/src=\{(slot|before|after|item|target|displayTarget)\.url\}/);
    expect(source).not.toMatch(/href=\{item\.downloadUrl\}/);
  });
});
