import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { previewKind } from '@/components/account/asset-preview-dialog';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

describe('shared top bar', () => {
  it('renders the workspace and the admin/account shell through the same AppTopbar', () => {
    expect(read('../studio/studio-topbar.tsx')).toContain('<AppTopbar');
    expect(read('./main-layout.tsx')).toContain('<AppTopbar');
    // The old 56px header and the sidebar brand block were the second and third top bars.
    expect(() => read('./header.tsx')).toThrow();
    expect(read('./sidebar.tsx')).not.toContain('brand.title');
  });

  it('opens My Resources without the left navigation', () => {
    expect(read('../../../web/src/router.tsx')).toContain('<MainLayout sidebar={false}><AccountPage />');
  });
});

describe('asset preview kind', () => {
  it('previews images and videos and offers download only for other files', () => {
    expect(previewKind('image/png')).toBe('image');
    expect(previewKind('video/mp4')).toBe('video');
    expect(previewKind('model/gltf-binary')).toBe('none');
  });
});
