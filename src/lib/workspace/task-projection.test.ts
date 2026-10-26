import { describe, expect, it } from 'vitest';

import type { TaskStateSchema } from '@/lib/product-schema';

import {
  assetKind,
  clampProgress,
  imageAssets,
  isDownloadableAsset,
  viewableModelAsset,
  viewableGaussianSplatAsset,
  projectNativeTaskDetail,
  projectTaskAssets,
  resolveErrorText,
} from './task-projection';

function stateFixture(
  overrides: Partial<TaskStateSchema> = {},
): TaskStateSchema {
  return {
    internal_state: 'SUCCEEDED',
    public_state: 'SUCCEEDED',
    label: '成功',
    color: 'bg-emerald-100',
    terminal: true,
    outcome: 'success',
    progress: { mode: 'reported' },
    ...overrides,
  };
}

const states: TaskStateSchema[] = [
  stateFixture({
    internal_state: 'RUNNING',
    public_state: 'IN_PROGRESS',
    label: '进行中',
    terminal: false,
    outcome: 'running',
  }),
  stateFixture({
    internal_state: 'MATERIALIZING',
    public_state: 'IN_PROGRESS',
    label: '正在保存',
    terminal: false,
    outcome: 'running',
  }),
  stateFixture(),
  stateFixture({
    internal_state: 'FAILED',
    public_state: 'FAILED',
    label: '失败',
    terminal: true,
    outcome: 'failure',
  }),
];

function nativeDetail(overrides: Record<string, unknown> = {}) {
  return {
    id: 'task_1',
    state: 'SUCCEEDED',
    progress: 100,
    assets: [
      {
        id: 'asset_a',
        role: 'result_image',
        variant: 'v1',
        required: true,
        delivery: 'inline',
        ordinal: 1,
        media_type: 'image/png',
        size: 2048,
        sha256: 'aa',
        url: '/api/v1/tasks/task_1/assets/asset_a',
      },
      {
        id: 'asset_b',
        role: 'extra_export',
        required: false,
        delivery: 'background',
        ordinal: 2,
        media_type: 'image/webp',
        size: 1024,
        sha256: 'bb',
        url: '',
      },
    ],
    resources_pending: false,
    ...overrides,
  };
}

describe('projectNativeTaskDetail', () => {
  it('carries the owner judgment of running a source_expired step again (#326)', () => {
    const projection = projectNativeTaskDetail(nativeDetail({ state: 'FAILED',
      failure: { class: 'source_expired', retryable: false, detail: 'x', source_retry: 'saved_file' } }), states, 100);
    expect(projection?.failure?.sourceRetry).toBe('saved_file');
    const plain = projectNativeTaskDetail(nativeDetail({ state: 'FAILED', failure: { class: 'rejected', retryable: false, detail: 'x' } }), states, 100);
    expect(plain?.failure).not.toHaveProperty('sourceRetry');
  });
  it('projects state, progress, failure and assets from the native detail', () => {
    const projection = projectNativeTaskDetail(
      nativeDetail({
        failure: {
          class: 'upstream',
          retryable: true,
          ambiguous: false,
          detail: 'error.task_fetch_failed',
        },
      }),
      states,
      100,
    );
    expect(projection).not.toBeNull();
    expect(projection?.state).toBe('SUCCEEDED');
    expect(projection?.progress).toBe(100);
    expect(projection?.terminal).toBe(true);
    expect(projection?.failure).toEqual({
      failureClass: 'upstream',
      retryable: true,
      ambiguous: false,
      detail: 'error.task_fetch_failed',
    });
    expect(projection?.assets).toHaveLength(2);
    expect(projection?.assets[0]).toMatchObject({
      id: 'asset_a',
      role: 'result_image',
      variant: 'v1',
      delivery: 'inline',
      ordinal: 1,
      mediaType: 'image/png',
      size: 2048,
      kind: 'image',
      url: '/api/v1/tasks/task_1/assets/asset_a',
    });
  });

  it('exposes only assets with real URLs as downloadable (never placeholder links)', () => {
    const projection = projectNativeTaskDetail(
      nativeDetail({
        assets: [
          { id: 'a1', role: 'r', ordinal: 0, media_type: 'image/png', size: 1, url: '/api/v1/tasks/t/assets/a1' },
          { id: 'a2', role: 'r', ordinal: 1, media_type: 'image/png', size: 1, url: '' },
          { id: 'a3', role: 'r', ordinal: 2, media_type: 'image/png', size: 1 },
          { id: 'a4', role: 'r', ordinal: 3, media_type: 'image/png', size: 1, url: '#' },
        ],
      }),
      states,
      100,
    );
    expect(projection?.downloadables.map((asset) => asset.id)).toEqual(['a1']);
    expect(
      projection?.downloadables.every((asset) => isDownloadableAsset(asset)),
    ).toBe(true);
  });

  it('keeps a saved model visible when its format uses generic binary MIME', () => {
    const projection = projectNativeTaskDetail(nativeDetail({ assets: [
      { id: 'rigged', role: 'model', variant: 'rigged_character.glb', ordinal: 0, media_type: 'application/octet-stream', size: 12, url: '/api/v1/tasks/t/assets/rigged' },
    ] }), states, 100);
    expect(projection?.downloadables[0].kind).toBe('model');
  });

  it('projects the full Gaussian Splat identity and previews only exact profile matches', () => {
    const projection = projectNativeTaskDetail(nativeDetail({ assets: [
      { id: 'splat', role: 'gaussian-splat', variant: 'splat', delivery_profile: 'gaussian-splat-32-v1', ordinal: 1, media_type: 'application/vnd.art-ai.gaussian-splat', size: 32, url: '/api/v1/tasks/t/assets/splat' },
      { id: 'wrong-profile', role: 'gaussian-splat', variant: 'splat', delivery_profile: 'unknown', ordinal: 0, media_type: 'application/vnd.art-ai.gaussian-splat', size: 32, url: '/api/v1/tasks/t/assets/wrong' },
    ] }), states, 100);
    expect(projection?.assets[0]).toMatchObject({ deliveryProfile: 'gaussian-splat-32-v1', kind: 'splat' });
    expect(viewableGaussianSplatAsset(projection?.assets ?? [])?.id).toBe('splat');
    expect(viewableModelAsset(projection?.assets ?? [])).toBeNull();
  });

  it('hides partial assets until the task succeeds', () => {
    for (const state of ['RUNNING', 'MATERIALIZING', 'FAILED']) {
      const projection = projectNativeTaskDetail(nativeDetail({ state }), states, 100);
      expect(projection?.assets).toHaveLength(2);
      expect(projection?.downloadables).toEqual([]);
    }
  });

  it('derives terminal from internal or public state and treats unknown states as non-terminal', () => {
    expect(projectNativeTaskDetail(nativeDetail({ state: 'IN_PROGRESS' }), states, 100)?.terminal).toBe(false);
    expect(projectNativeTaskDetail(nativeDetail({ state: 'FAILED' }), states, 100)?.terminal).toBe(true);
    expect(projectNativeTaskDetail(nativeDetail({ state: 'MYSTERY' }), states, 100)?.terminal).toBe(false);
  });

  it('reads resources_pending and rejects malformed payloads', () => {
    expect(projectNativeTaskDetail(nativeDetail({ resources_pending: true }), states, 100)?.resourcesPending).toBe(true);
    expect(projectNativeTaskDetail(null, states, 100)).toBeNull();
    expect(projectNativeTaskDetail(['x'], states, 100)).toBeNull();
    expect(projectNativeTaskDetail('text', states, 100)).toBeNull();
  });

  it('keeps a successful asset downloadable when an optional resource fails', () => {
    const projection = projectNativeTaskDetail(nativeDetail({
      resources: [
        { role: 'converted_model', variant_prefix: 'fbx', delivery: 'background', state: 'failed', reason: 'error.resource_conversion_failed' },
        { role: 'thumbnail', state: 'ready', variant: 'small' },
        { role: '', state: 'failed' },
        null,
      ],
    }), states, 100);
    expect(projection?.state).toBe('SUCCEEDED');
    expect(projection?.failure).toBeNull();
    expect(projection?.downloadables.map((asset) => asset.id)).toEqual(['asset_a']);
    expect(projection?.resources).toEqual([
      { role: 'converted_model', variant: '', variantPrefix: 'fbx', delivery: 'background', state: 'failed', reason: 'error.resource_conversion_failed' },
      { role: 'thumbnail', variant: 'small', variantPrefix: '', delivery: '', state: 'ready', reason: '' },
    ]);
  });
});

describe('clampProgress', () => {
  it('clamps to the schema percentage scale and rejects non-numbers', () => {
    expect(clampProgress(42, 100)).toBe(42);
    expect(clampProgress(-5, 100)).toBe(0);
    expect(clampProgress(250, 100)).toBe(100);
    expect(clampProgress('80', 100)).toBe(0);
    expect(clampProgress(undefined, 100)).toBe(0);
    expect(clampProgress(42, 0)).toBe(0);
  });
});

describe('assetKind', () => {
  it('maps media types by top-level type without per-provider literals', () => {
    expect(assetKind('image/png')).toBe('image');
    expect(assetKind('image/webp')).toBe('image');
    expect(assetKind('model/gltf-binary')).toBe('model');
    expect(assetKind('model/gltf+json')).toBe('model');
    expect(assetKind('application/zip')).toBe('other');
    expect(assetKind('')).toBe('other');
  });
});

describe('resolveErrorText', () => {
  const messages = {
    'error.task_not_found': '任务不存在',
    'ui.workspace.tasks.load_failed': '加载失败',
  };

  it('maps error.* vocabulary keys through content messages', () => {
    expect(resolveErrorText(messages, 'error.task_not_found', 'fallback')).toBe(
      '任务不存在',
    );
  });

  it('passes through already-resolved server text', () => {
    expect(resolveErrorText(messages, 'upstream rejected prompt', 'fallback')).toBe(
      'upstream rejected prompt',
    );
  });

  it('falls back for empty or non-string details and unknown keys keep raw value', () => {
    expect(resolveErrorText(messages, null, 'fallback')).toBe('fallback');
    expect(resolveErrorText(messages, '', 'fallback')).toBe('fallback');
    expect(resolveErrorText(messages, 42, 'fallback')).toBe('fallback');
    expect(resolveErrorText(messages, 'error.unknown_key', 'fallback')).toBe(
      'error.unknown_key',
    );
  });
});

describe('viewableModelAsset / imageAssets', () => {
  const asset = (id: string, variant: string, mediaType: string, ordinal: number, kind: 'model' | 'image' = 'model', url = `/u/${id}`) =>
    ({ id, role: kind === 'model' ? 'model' : 'thumbnail', variant, mediaType, ordinal, url, kind });
  // Meshy stores several formats; the 3MF (a ZIP) comes first by ordinal and the GLB third.
  // The BLEND entry is a historical task: current delivery no longer offers it, but stored
  // files must stay downloadable.
  const meshy = [
    asset('m3mf', '3mf', 'model/3mf', 0),
    asset('mobj', 'obj', 'model/obj', 1),
    asset('mglb', 'glb', 'model/gltf-binary', 2),
    asset('mfbx', 'fbx', 'application/octet-stream', 3),
    asset('musdz', 'usdz', 'model/vnd.usdz+zip', 4),
    asset('mstl', 'stl', 'model/stl', 5),
    asset('mblend', 'blend', 'application/x-blender', 6),
    asset('thumb', 'primary', 'image/png', 0, 'image'),
  ];

  // #326: a Meshy Rigging result lists its basic-animation files first (the first GLB is a 79,600-byte
  // skeleton); the required rigged character is the model the stage shows and the panel binds.
  it('shows the required model before an attachment GLB listed earlier', () => {
    const rigged = [
      { ...asset('skeleton', 'basic_animations.running_armature_glb_url', 'model/gltf-binary', 1), required: false },
      { ...asset('running', 'basic_animations.running_glb_url', 'model/gltf-binary', 2), required: false },
      { ...asset('fbx', 'rigged_character_fbx_url', 'application/octet-stream', 7), required: true },
      { ...asset('character', 'rigged_character_glb_url', 'model/gltf-binary', 8), required: true },
    ];
    expect(viewableModelAsset(rigged)?.id).toBe('character');
  });

  it('reads the required mark of the task projection', () => {
    const projected = projectTaskAssets([{ id: 'a', role: 'model', variant: 'glb', media_type: 'model/gltf-binary', url: '/a', required: true },
      { id: 'b', role: 'model', variant: 'x', media_type: 'model/gltf-binary', url: '/b' }]);
    expect(projected.map((entry) => entry.required)).toEqual([true, false]);
  });

  it('picks the GLB over an earlier 3MF', () => {
    expect(viewableModelAsset(meshy)?.id).toBe('mglb');
  });

  it('never previews glTF JSON: the stage reads GLB only', () => {
    const list = [asset('a', '3mf', 'model/3mf', 0), asset('b', 'gltf', 'model/gltf+json', 1), asset('c', 'converted.gltf', 'application/octet-stream', 2)];
    expect(viewableModelAsset(list)).toBeNull();
  });

  it('prefers GLB over glTF regardless of ordinal', () => {
    const list = [asset('g', 'gltf', 'model/gltf+json', 0), asset('b', 'glb', 'model/gltf-binary', 5)];
    expect(viewableModelAsset(list)?.id).toBe('b');
  });

  it('returns null when only download-only formats exist', () => {
    expect(viewableModelAsset(meshy.filter((item) => item.id !== 'mglb'))).toBeNull();
  });

  it('leaves a Tripo GLB main model unaffected', () => {
    const tripo = [asset('t', 'model', 'model/gltf-binary', 0), asset('pre', 'primary', 'image/webp', 1, 'image')];
    expect(viewableModelAsset(tripo)?.id).toBe('t');
  });

  it('uses the variant only when the media type is missing or generic', () => {
    expect(viewableModelAsset([asset('x', 'glb', '', 0)])?.id).toBe('x');
    // The server keeps a CDN's placeholder type when the claim named none (Tripo answers binary/octet-stream).
    expect(viewableModelAsset([asset('x2', 'glb', 'binary/octet-stream', 0)])?.id).toBe('x2');
    expect(viewableModelAsset([asset('x3', 'fbx', 'application/octet-stream', 0)])).toBeNull();
    expect(viewableModelAsset([asset('y', 'glb', 'model/3mf', 0)])).toBeNull();
  });

  it('skips assets without a downloadable url', () => {
    expect(viewableModelAsset([asset('z', 'glb', 'model/gltf-binary', 0, 'model', '')])).toBeNull();
  });

  it('lists downloadable image assets ordered by ordinal', () => {
    expect(imageAssets(meshy).map((item) => item.id)).toEqual(['thumb']);
  });
});

it.each([['clip', 'model/fbx'], ['print-archive', 'model/3mf']])('keeps explicit %s outputs as attachments despite their model MIME', (role, media_type) => {
  const raw = { id: 'file', role, media_type, variant: role, ordinal: 0, size: 40, url: '/api/v1/tasks/t/assets/file' };
  expect(projectNativeTaskDetail(nativeDetail({ assets: [raw] }), states, 100)?.downloadables[0].kind).toBe('other');
});
it('keeps explicit model outputs and no-role legacy model MIME fallback', () => {
  expect(projectNativeTaskDetail(nativeDetail({ assets: [{ id: 'm', role: 'model', media_type: 'model/fbx', url: '/api/m' }, { id: 'legacy', media_type: 'model/fbx', url: '/api/legacy' }] }), states, 100)?.assets.map(asset => asset.kind)).toEqual(['model','model']);
});
