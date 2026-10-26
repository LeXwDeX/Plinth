import { describe, expect, it } from 'vitest';

import { coverOfTasks } from './session-cover';

const asset = (id: string, mediaType: string, role = 'image') => ({ id, role, media_type: mediaType, url: `/api/v1/tasks/x/assets/${id}` });

describe('coverOfTasks', () => {
  it('takes the newest task that has a picture', () => {
    const cover = coverOfTasks({ tasks: [
      { id: 't3', assets: [] },
      { id: 't2', assets: [asset('a2', 'image/png')] },
      { id: 't1', assets: [asset('a1', 'image/png')] },
    ] });
    expect(cover).toEqual({ taskId: 't2', url: '/api/v1/tasks/x/assets/a2' });
  });

  it('uses the thumbnail of a 3D task, and prefers the picture over a thumbnail when both exist', () => {
    expect(coverOfTasks({ tasks: [{ id: 'm', assets: [asset('glb', 'model/gltf-binary', 'model'), asset('th', 'image/png', 'thumbnail')] }] }))
      .toMatchObject({ taskId: 'm', url: '/api/v1/tasks/x/assets/th' });
    expect(coverOfTasks({ tasks: [{ id: 'i', assets: [asset('th', 'image/png', 'thumbnail'), asset('pic', 'image/png')] }] }))
      .toMatchObject({ url: '/api/v1/tasks/x/assets/pic' });
  });

  it('never uses a texture map as the cover of a 3D task', () => {
    expect(coverOfTasks({ tasks: [{ id: 'm', assets: [asset('glb', 'model/gltf-binary', 'model'), asset('tx', 'image/png', 'texture'), asset('th', 'image/png', 'thumbnail')] }] }))
      .toMatchObject({ url: '/api/v1/tasks/x/assets/th' });
    expect(coverOfTasks({ tasks: [{ id: 'm', assets: [asset('tx', 'image/png', 'texture')] }] })).toBeNull();
  });

  it('has no cover for an empty or malformed answer', () => {
    expect(coverOfTasks({ tasks: [] })).toBeNull();
    expect(coverOfTasks(null)).toBeNull();
    expect(coverOfTasks({ tasks: [{ id: 'm', assets: [asset('glb', 'model/gltf-binary', 'model')] }, 'x', { assets: 3 }] })).toBeNull();
  });
});
