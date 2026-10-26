import { describe, expect, it } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { blankTile } from '@/lib/workspace/versions';

import { versionCaption, versionLabel, versionTitle } from './version-label';

const schema = {
  content: {
    messages: {
      'ui.studio.version.label_2d': '2D v{version}',
      'ui.studio.version.label_3d': '3D v{version}',
      'ui.studio.version.unnumbered_2d': '2D',
      'ui.studio.version.unnumbered_3d': '3D',
      'ui.studio.version.upload': '上传',
      'ui.studio.version.label_merge': '2D m{version}',
      'ui.studio.version.capture_caption': '{parent} 截图',
      'ui.studio.version.capture_caption_alone': '截图',
    },
  },
} as unknown as ProductSchema;

const model = blankTile({ key: 'n:m', taskId: 'm', domain: 'model', status: 'ready', version: 1 });
const shot = blankTile({
  key: 'c:c1', taskId: 'm', domain: 'image', status: 'ready', version: 1, origin: 'capture',
  parents: [{ key: 'n:m', known: true, kind: 'capture', evidence: 'recorded', link: 'capture', step: '' }],
});

describe('version labels', () => {
  it('numbers by domain and leaves an upload without a number', () => {
    expect(versionLabel(schema, model, 'model')).toBe('3D v1');
    expect(versionLabel(schema, { ...shot, origin: 'upload' }, 'image')).toBe('上传');
    expect(versionLabel(schema, null, 'image')).toBe('2D');
    expect(versionLabel(schema, { ...shot, origin: 'merge' }, 'image')).toBe('2D m1');
  });

  it('captions a screenshot with the model it was taken from', () => {
    expect(versionCaption(schema, shot, [model, shot])).toBe('3D v1 截图');
    expect(versionTitle(schema, shot, [model, shot])).toBe('2D v1 · 3D v1 截图');
    expect(versionCaption(schema, { ...shot, parents: [] }, [shot])).toBe('截图');
  });

  it('captions a group with its own label and an ordinary result with nothing', () => {
    expect(versionTitle(schema, { ...model, groupLabel: '三视图' }, [model])).toBe('3D v1 · 三视图');
    expect(versionTitle(schema, model, [model])).toBe('3D v1');
  });
});
