import { describe, expect, it } from 'vitest';

import { buildChain, lineageDomain, parseLineage } from '@/lib/workspace/lineage';

const asset = (id: string, role: string, mediaType: string, ordinal = 0) => ({
  id, role, media_type: mediaType, ordinal, url: `/api/v1/tasks/t/assets/${id}`,
});
const task = (id: string, minute: number, assets: unknown[]) => ({
  id, kind: 'task', task_id: id, created_at: `2026-01-01T00:0${minute}:00Z`, assets,
});
const edge = (from: string, to: string, kind: string, evidence: string, extra: Record<string, string> = {}) => ({ from, to, kind, evidence, ...extra });

const payload = {
  truncated: false,
  nodes: [
    task('img2', 3, [asset('img2-a', 'image', 'image/png')]),
    task('mdl1', 2, [asset('mdl1-a', 'model', 'model/gltf-binary')]),
    task('img1', 1, [asset('img1-a', 'image', 'image/png')]),
    task('mdl2', 4, [asset('mdl2-a', 'model', 'model/gltf-binary')]),
    { id: 'unknown-1', kind: 'unknown' },
  ],
  edges: [
    edge('img1', 'mdl1', 'reference', 'same_bytes', { asset_id: 'img1-a' }),
    edge('mdl1', 'img2', 'capture', 'recorded', { capture_id: 'cap-1' }),
    edge('img2', 'mdl2', 'reference', 'recorded'),
    edge('unknown-1', 'img1', 'reference', 'unknown'),
  ],
};

describe('version chain', () => {
  it('numbers each domain by creation time: 2D v1, 3D v1, 2D v2, 3D v2', () => {
    const chain = buildChain(parseLineage(payload));
    expect(chain.map((entry) => `${entry.domain}${entry.version}`)).toEqual(['image1', 'model1', 'image2', 'model2']);
    expect(chain.map((entry) => entry.id)).toEqual(['img1', 'mdl1', 'img2', 'mdl2']);
  });

  it('links each version to its parent with the evidence the server gave', () => {
    const chain = buildChain(parseLineage(payload));
    const byId = Object.fromEntries(chain.map((entry) => [entry.id, entry]));
    expect(byId.mdl1.parents).toMatchObject([{ entryId: 'img1', domain: 'image', version: 1, edge: { evidence: 'same_bytes', kind: 'reference' } }]);
    expect(byId.img2.parents).toMatchObject([{ entryId: 'mdl1', domain: 'model', version: 1, edge: { kind: 'capture', captureId: 'cap-1' } }]);
    expect(byId.mdl2.parents).toMatchObject([{ entryId: 'img2', domain: 'image', version: 2 }]);
  });

  it('shows a source the server could not identify as unknown, never as a version', () => {
    const chain = buildChain(parseLineage(payload));
    const first = chain.find((entry) => entry.id === 'img1');
    expect(first?.parents).toEqual([expect.objectContaining({ entryId: '', domain: null, version: 0, edge: expect.objectContaining({ evidence: 'unknown' }) })]);
    // The unknown node is not a chain entry of its own.
    expect(chain.some((entry) => entry.id === 'unknown-1')).toBe(false);
  });

  it('does not trust an edge claiming known evidence from a node that is not a known task', () => {
    const graph = parseLineage({
      nodes: [task('a', 1, [asset('a-a', 'image', 'image/png')]), { id: 'unknown-1', kind: 'unknown' }],
      edges: [edge('unknown-1', 'a', 'source_task', 'recorded')],
    });
    expect(buildChain(graph)[0].parents[0]).toMatchObject({ entryId: '', domain: null, edge: { evidence: 'unknown' } });
  });

  it('drops malformed nodes and edges to nodes that do not exist', () => {
    const graph = parseLineage({
      nodes: [task('a', 1, [asset('a-a', 'image', 'image/png'), { id: 'no-url' }]), { id: '', kind: 'task' }, { id: 'x', kind: 'weird' }, 'junk'],
      edges: [edge('ghost', 'a', 'reference', 'recorded'), edge('a', 'a', 'nonsense', 'recorded'), 7],
    });
    expect(graph.nodes).toHaveLength(1);
    expect(graph.nodes[0].assets).toHaveLength(1);
    expect(graph.edges).toEqual([]);
    expect(parseLineage(null)).toEqual({ nodes: [], edges: [], groups: [], truncated: false });
  });

  it('classifies a node by its files: a model wins over images', () => {
    const graph = parseLineage({ nodes: [task('m', 1, [asset('p', 'preview', 'image/png'), asset('g', 'model', 'model/gltf-binary')])], edges: [] });
    expect(lineageDomain(graph.nodes[0])).toBe('model');
    expect(parseLineage({ truncated: true }).truncated).toBe(true);
  });
});

it.each([['clip', 'model/fbx'], ['print-archive', 'model/3mf']])('does not turn the explicit %s attachment into a model lineage source', (role, mime) => {
  const graph = parseLineage({ nodes: [task('file', 1, [asset('file',role,mime)])], edges: [] });
  expect(graph.nodes[0].assets[0].kind).toBe('other');
  expect(lineageDomain(graph.nodes[0])).toBe('file');
});
it('retains role=model FBX and no-role legacy FBX as model lineage', () => {
  const graph = parseLineage({ nodes: [task('m',1,[asset('m','model','model/fbx')]),task('old',2,[asset('old','','model/fbx')])], edges: [] });
  expect(graph.nodes.map(node => lineageDomain(node))).toEqual(['model','model']);
});

// #326: the version strip's model is the task's required output, so the parsed graph keeps the mark.
describe('required files', () => {
  it('keeps which files are required outputs', () => {
    const graph = parseLineage({ truncated: false, edges: [], nodes: [task('rig', 1, [
      asset('skeleton', 'model', 'model/gltf-binary', 1),
      { ...asset('character', 'model', 'model/gltf-binary', 8), required: true },
    ])] });
    expect(graph.nodes[0].assets.map((entry) => [entry.id, entry.required])).toEqual([['skeleton', false], ['character', true]]);
  });
});
