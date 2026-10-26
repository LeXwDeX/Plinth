import JSZip from 'jszip';
import { describe, expect, it } from 'vitest';

import {
  buildArchive,
  canSaveArchiveToDisk,
  extensionOf,
  isPlatformAssetUrl,
  planBatchExport,
  saveArchiveToDisk,
} from './batch-export';
import type { VersionTile } from './versions';

const asset = (id: string, kind: 'image' | 'model', url: string, mediaType: string) => ({ id, role: kind, variant: '', mediaType, ordinal: 0, url, kind });
const tile = (key: string, patch: Partial<VersionTile>): VersionTile => ({
  key, taskId: key, taskIds: [key], domain: 'image', version: 1, status: 'ready', progress: 1, capability: '',
  createdAt: '', assets: [], thumbnailUrl: '', groupLabel: '', groupComplete: true, slots: [], views: [], parents: [], failure: '', origin: 'task', operation: '', captureId: '', size: null, ...patch,
});
const label = (item: VersionTile) => `${item.domain === 'model' ? '3D' : '2D'} v${item.version}`;

describe('planBatchExport', () => {
  const tiles = [
    tile('a', { version: 1, assets: [asset('1', 'image', '/api/v1/tasks/t/assets/1', 'image/png')] }),
    tile('b', { version: 2, assets: [asset('2', 'image', '/api/v1/tasks/t/assets/2', 'image/png'), asset('3', 'image', '/api/v1/tasks/t/assets/3', 'image/jpeg')] }),
    tile('m', { version: 1, domain: 'model', assets: [asset('4', 'model', '/api/v1/tasks/t/assets/4', 'model/gltf-binary')], thumbnailUrl: '/api/x' }),
    tile('r', { version: 3, status: 'running', assets: [] }),
    tile('x', { version: 4, assets: [asset('5', 'image', 'https://evil.example/x.png', 'image/png'), asset('6', 'image', '//evil.example/x.png', 'image/png')] }),
  ];

  it('names files by version, numbers the candidates of one version and uses the real extension', () => {
    const entries = planBatchExport(tiles, new Set(['a', 'b', 'm']), label);
    expect(entries.map((entry) => entry.name)).toEqual(['2D-v1.png', '2D-v2-1.png', '2D-v2-2.jpg', '3D-v1.glb']);
  });

  it('keeps to the strip order and skips versions that are not selected or not finished', () => {
    expect(planBatchExport(tiles, new Set(['m', 'a', 'r']), label).map((entry) => entry.name)).toEqual(['2D-v1.png', '3D-v1.glb']);
  });

  it('takes only platform asset routes, never an external address', () => {
    expect(planBatchExport(tiles, new Set(['x']), label)).toEqual([]);
    expect(isPlatformAssetUrl('/api/v1/tasks/t/assets/1')).toBe(true);
    expect(isPlatformAssetUrl('https://evil.example/api/x')).toBe(false);
    expect(isPlatformAssetUrl('//evil.example/api/x')).toBe(false);
    expect(isPlatformAssetUrl('/elsewhere/x')).toBe(false);
  });

  it('never repeats a file name', () => {
    const twins = [tile('p', { version: 1, assets: [asset('1', 'image', '/api/a', 'image/png')] }), tile('q', { version: 1, assets: [asset('2', 'image', '/api/b', 'image/png')] })];
    expect(planBatchExport(twins, new Set(['p', 'q']), label).map((entry) => entry.name)).toEqual(['2D-v1.png', '2D-v1-2.png']);
  });
});

describe('planBatchExport, one file per version', () => {
  const asset = (id: string, url = `/api/v1/tasks/t/assets/${id}`) => ({ id, role: 'image', variant: '', mediaType: 'image/png', ordinal: 0, url, kind: 'image' as const });
  const group = tile('g', { version: 1, taskIds: ['g'], assets: [asset('1'), asset('2'), asset('3'), asset('4')] });
  const views = tile('v', {
    version: 3, taskId: 'front', taskIds: ['front', 'side', 'back'], assets: [asset('10'), asset('11'), asset('12')],
    slots: [{ slot: 'side', label: '侧', taskId: 'side', assetId: '11' }, { slot: 'front', label: '正', taskId: 'front', assetId: '10' }],
  });
  const single = tile('s', { version: 4, assets: [asset('20')] });
  const tiles = [group, tile('g2', { version: 2, assets: [asset('5'), asset('6')] }), views, single];

  it('takes only the candidate that was picked, and says which in the file name', () => {
    const entries = planBatchExport(tiles, new Set(['g']), label, { scope: 'chosen', chosenAssetId: () => '3' });
    expect(entries).toEqual([{ name: '2D-v1-3.png', url: '/api/v1/tasks/t/assets/3' }]);
  });

  it('is one file per selected version: four versions selected is four files', () => {
    const entries = planBatchExport(tiles, new Set(['g', 'g2', 'v', 's']), label, { scope: 'chosen' });
    expect(entries.map((entry) => entry.name)).toEqual(['2D-v1-1.png', '2D-v2-1.png', '2D-v3-1.png', '2D-v4.png']);
  });

  it('falls back to the front view of a three-view version, then to the first picture', () => {
    expect(planBatchExport(tiles, new Set(['v']), label, { scope: 'chosen' }).map((entry) => entry.url)).toEqual(['/api/v1/tasks/t/assets/10']);
    expect(planBatchExport(tiles, new Set(['g2']), label, { scope: 'chosen', chosenAssetId: () => 'gone' }).map((entry) => entry.url)).toEqual(['/api/v1/tasks/t/assets/5']);
  });

  it('still brings every candidate of a version when asked for the whole group', () => {
    expect(planBatchExport(tiles, new Set(['g']), label).map((entry) => entry.name)).toEqual(['2D-v1-1.png', '2D-v1-2.png', '2D-v1-3.png', '2D-v1-4.png']);
  });
});

describe('extensionOf', () => {
  it('maps the known types and falls back to the subtype', () => {
    expect(extensionOf('image/jpeg')).toBe('jpg');
    expect(extensionOf('model/gltf-binary')).toBe('glb');
    expect(extensionOf('image/avif')).toBe('avif');
    expect(extensionOf('')).toBe('bin');
  });
});

describe('buildArchive', () => {
  const ok = (body: string) => new Response(body, { status: 200 });

  it('packs every file under its name', async () => {
    const archive = await buildArchive(
      [{ name: 'a.png', url: '/api/1' }, { name: 'b.png', url: '/api/2' }],
      async (url) => ok(`bytes of ${url}`),
    );
    expect(archive).toMatchObject({ added: 2, failed: 0 });
    const zip = await JSZip.loadAsync(await archive!.blob.arrayBuffer());
    expect(Object.keys(zip.files).sort()).toEqual(['a.png', 'b.png']);
    expect(await zip.file('b.png')!.async('string')).toBe('bytes of /api/2');
  });

  it('still packs the rest when one file cannot be fetched, and says so', async () => {
    const archive = await buildArchive(
      [{ name: 'a.png', url: '/api/1' }, { name: 'b.png', url: '/api/2' }, { name: 'c.png', url: '/api/3' }],
      async (url) => (url === '/api/2' ? new Response('', { status: 404 }) : ok('x')),
    );
    expect(archive).toMatchObject({ added: 2, failed: 1 });
    expect(Object.keys((await JSZip.loadAsync(await archive!.blob.arrayBuffer())).files).sort()).toEqual(['a.png', 'c.png']);
  });

  it('returns nothing when no file could be fetched', async () => {
    expect(await buildArchive([{ name: 'a.png', url: '/api/1' }], async () => { throw new Error('offline'); })).toBeNull();
  });

  it('keeps non-ASCII names readable for any unzip tool', async () => {
    const archive = await buildArchive([{ name: '产出 1.png', url: '/api/1' }], async () => ok('x'));
    expect(Object.keys((await JSZip.loadAsync(await archive!.blob.arrayBuffer())).files)).toEqual(['产出 1.png']);
  });

  it('reports one progress step per file', async () => {
    const steps: { done: number; total: number; bytes: number }[] = [];
    await buildArchive(
      [{ name: 'a.png', url: '/api/1' }, { name: 'b.png', url: '/api/2' }, { name: 'c.png', url: '/api/3' }],
      async (url) => (url === '/api/2' ? new Response('', { status: 404 }) : ok('abc')),
      1,
      (progress) => steps.push(progress),
    );
    expect(steps.map((step) => step.done)).toEqual([1, 2, 3]);
    expect(steps[steps.length - 1]).toEqual({ done: 3, total: 3, bytes: 6 });
  });
});

describe('saveArchiveToDisk', () => {
  const withPicker = async (run: (written: BlobPart[], picker: () => unknown) => Promise<void>) => {
    const written: BlobPart[] = [];
    const picker = () => ({
      createWritable: async () => ({
        write: async (data: BlobPart) => { written.push(data); },
        close: async () => undefined,
        abort: async () => undefined,
      }),
    });
    const scope = globalThis as { window?: unknown };
    const previous = scope.window;
    scope.window = { showSaveFilePicker: picker };
    try {
      await run(written, picker);
    } finally {
      scope.window = previous;
    }
  };

  it('is offered only when the browser has the File System Access API', async () => {
    expect(canSaveArchiveToDisk()).toBe(false);
    await withPicker(async () => {
      expect(canSaveArchiveToDisk()).toBe(true);
    });
  });

  it('writes the same readable ZIP one entry at a time', async () => {
    await withPicker(async (written) => {
      const archive = await saveArchiveToDisk(
        [{ name: 'a.png', url: '/api/1' }, { name: 'b.png', url: '/api/2' }, { name: 'c.png', url: '/api/3' }],
        'out.zip',
        undefined,
        async (url) => (url === '/api/3' ? new Response('', { status: 500 }) : new Response(`bytes of ${url}`, { status: 200 })),
      );
      expect(archive).toMatchObject({ added: 2, failed: 1 });
      const zip = await JSZip.loadAsync(await new Blob(written).arrayBuffer());
      expect(Object.keys(zip.files).sort()).toEqual(['a.png', 'b.png']);
      expect(await zip.file('b.png')!.async('string')).toBe('bytes of /api/2');
    });
  });
});
