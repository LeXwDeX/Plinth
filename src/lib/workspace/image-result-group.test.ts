import { describe, expect, it } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { imageGroupOfPictures } from '@/lib/workspace/image-result-group';

const slots = [{ variant: 'front', label: '正面' }, { variant: 'left', label: '左侧' }, { variant: 'back', label: '背面' }, { variant: 'right', label: '右侧' }];
const schema = {
  capabilities: [
    { id: 'image-to-multiview', label_cn: '生成四视角图片', image_result_group: { slots, source_selectable: true } },
    { id: 'edit-multiview', label_cn: '编辑四视角图片', image_result_group: { slots, partial_message: 'ui.studio.result.image_group_partial' } },
    { id: 'text-to-image', label_cn: '文生图' },
  ],
} as unknown as ProductSchema;

const asset = (task: string, variant: string) => ({ id: `${task}-${variant}`, kind: 'image', variant, url: `/api/v1/tasks/${task}/assets/${task}-${variant}` });
const picture = (task: string, variant: string) => ({ taskId: task, assetId: `${task}-${variant}`, url: `/api/v1/tasks/${task}/assets/${task}-${variant}` });

describe('imageGroupOfPictures', () => {
  it('labels and orders the pictures of one four-view Task by the public slots, not by arrival order', () => {
    const tasks = [{ taskId: 'g', capability: 'image-to-multiview', assets: ['right', 'back', 'front', 'left'].map((variant) => asset('g', variant)) }];
    const pictures = ['right', 'back', 'front', 'left'].map((variant) => picture('g', variant));
    const group = imageGroupOfPictures(schema, pictures, (item) => item, tasks);
    expect(group?.title).toBe('生成四视角图片');
    expect(group?.partialMessage).toBe('');
    expect(group?.views.map((view) => [view.variant, view.label, view.item.assetId])).toEqual([
      ['front', '正面', 'g-front'], ['left', '左侧', 'g-left'], ['back', '背面', 'g-back'], ['right', '右侧', 'g-right'],
    ]);
  });

  it('marks an edit that delivered only some views as partial and never invents the other views', () => {
    const tasks = [{ taskId: 'e', capability: 'edit-multiview', assets: [asset('e', 'back')] }];
    const group = imageGroupOfPictures(schema, [picture('e', 'back')], (item) => item, tasks);
    expect(group?.title).toBe('编辑四视角图片');
    expect(group?.partialMessage).toBe('ui.studio.result.image_group_partial');
    expect(group?.views.map((view) => view.label)).toEqual(['背面']);
  });

  it('shows a generated group that is missing views as plain candidates: only a declared partial group may be partial', () => {
    const tasks = [{ taskId: 'g', capability: 'image-to-multiview', assets: ['front', 'left', 'back'].map((variant) => asset('g', variant)) }];
    expect(imageGroupOfPictures(schema, ['front', 'left', 'back'].map((variant) => picture('g', variant)), (item) => item, tasks)).toBeNull();
  });

  it('matches a picture by URL when its attachment has no asset ID', () => {
    const tasks = [{ taskId: 'e', capability: 'edit-multiview', assets: [asset('e', 'front')] }];
    const group = imageGroupOfPictures(schema, [{ taskId: 'e', url: asset('e', 'front').url }], (item) => item, tasks);
    expect(group?.views.map((view) => view.variant)).toEqual(['front']);
  });

  it('falls back to plain candidates for ordinary images, mixed Tasks, unknown Tasks or unknown views', () => {
    const ordinary = [{ taskId: 't', capability: 'text-to-image', assets: [asset('t', 'front')] }];
    expect(imageGroupOfPictures(schema, [picture('t', 'front')], (item) => item, ordinary)).toBeNull();
    const two = [
      { taskId: 'a', capability: 'image-to-multiview', assets: [asset('a', 'front')] },
      { taskId: 'b', capability: 'image-to-multiview', assets: [asset('b', 'left')] },
    ];
    expect(imageGroupOfPictures(schema, [picture('a', 'front'), picture('b', 'left')], (item) => item, two)).toBeNull();
    expect(imageGroupOfPictures(schema, [picture('x', 'front')], (item) => item, [])).toBeNull();
    const odd = [{ taskId: 'g', capability: 'image-to-multiview', assets: [asset('g', 'top')] }];
    expect(imageGroupOfPictures(schema, [picture('g', 'top')], (item) => item, odd)).toBeNull();
    const repeated = [{ taskId: 'g', capability: 'image-to-multiview', assets: [asset('g', 'front')] }];
    expect(imageGroupOfPictures(schema, [picture('g', 'front'), picture('g', 'front')], (item) => item, repeated)).toBeNull();
    expect(imageGroupOfPictures(schema, [], (item) => item, repeated)).toBeNull();
  });
});
