import { describe, expect, it } from 'vitest';
import type { TrackedAsset } from '@/lib/workspace/task-projection';
import type { TrackedTask } from '@/store/workspace-store';
import { chatGaussianSplatResults, chatModelResults } from './model-results';

const asset = (id: string, kind: TrackedAsset['kind'], variant: string, ordinal: number): TrackedAsset => ({
  id, role: kind === 'model' ? 'model' : 'thumbnail', variant, delivery: 'required', ordinal,
  mediaType: kind === 'model' ? 'model/gltf-binary' : 'image/png', size: 1, url: `/assets/${id}`, kind,
});

const task = (taskId: string, sessionId: string | undefined, downloadables: TrackedAsset[]): TrackedTask => ({
  taskId, sessionId, capability: 'retexture', prompt: '', state: 'SUCCEEDED', progress: 100, progressMaximum: 100,
  terminal: true, failure: null, liveError: '', assets: downloadables, downloadables, resources: [],
  resourcesPending: false, notFound: false, updatedAt: 0,
});

describe('chatModelResults', () => {
  const finished = task('t1', 's1', [asset('thumb', 'image', '', 0), asset('glb', 'model', 'glb', 1), asset('fbx', 'model', 'fbx', 2)]);

  it('turns a finished 3D task of this session into a viewer selection with its capability and prompt', () => {
    expect(chatModelResults([{ ...finished, prompt: 'a red fox' }], ['t1'], 's1')).toEqual([{
      taskId: 't1', capability: 'retexture', prompt: 'a red fox',
      selection: { taskId: 't1', kind: 'model', url: '/assets/glb', assetId: 'glb', sessionId: 's1' },
    }]);
  });

  it('skips tasks of other messages, other sessions, unfinished tasks and image-only tasks', () => {
    expect(chatModelResults([finished], ['other'], 's1')).toEqual([]);
    expect(chatModelResults([finished], ['t1'], 's2')).toEqual([]);
    expect(chatModelResults([finished], ['t1'], null)).toEqual([]);
    expect(chatModelResults([task('t2', 's1', [])], ['t2'], 's1')).toEqual([]);
    expect(chatModelResults([task('t3', 's1', [asset('thumb', 'image', '', 0)])], ['t3'], 's1')).toEqual([]);
  });
});

it('routes exact Splat assets to the distinct detail target and ignores mesh and wrong-profile assets', () => {
  const splat: TrackedAsset = {
    id: 'splat', role: 'gaussian-splat', variant: 'splat', delivery: 'required', deliveryProfile: 'gaussian-splat-32-v1',
    ordinal: 0, mediaType: 'application/vnd.art-ai.gaussian-splat', size: 32, url: '/assets/splat', kind: 'splat',
  };
  const valid = { ...task('splat-task', 's1', [splat]), capability: 'image-to-splat' };
  expect(chatGaussianSplatResults([valid], ['splat-task'], 's1')).toMatchObject([
    { kind: 'splat', url: '/assets/splat', assetId: 'splat', taskId: 'splat-task' },
  ]);
  expect(chatGaussianSplatResults([{ ...valid, downloadables: [{ ...splat, deliveryProfile: 'wrong' }] }], ['splat-task'], 's1')).toEqual([]);
  expect(chatGaussianSplatResults([valid], ['splat-task'], 's2')).toEqual([]);
});
