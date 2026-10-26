import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { schemaText } from '@/lib/product-schema';
import { parseLineage } from '@/lib/workspace/lineage';
import { detailSource } from '@/lib/workspace/task-detail';
import { buildVersionTiles } from '@/lib/workspace/versions';

const schema = defaultCatalog.application as unknown as ProductSchema;
const at = (minute: number) => `2026-10-21T16:${String(minute).padStart(2, '0')}:00Z`;

// #189: the session lineage of a Creative Lab figure (actual shape from the
// isolated acceptance session): upload -> design preview (image) -> build.
const graph = parseLineage({
  nodes: [
    { id: 'upload:photo', kind: 'upload', created_at: at(1), assets: [{ id: 'photo', role: 'image', media_type: 'image/png', ordinal: 0, url: '/api/v1/assets/photo' }] },
    { id: 'proto', kind: 'task', task_id: 'proto', capability: 'creative-lab-prototype', created_at: at(2),
      assets: [{ id: 'preview', role: 'image', variant: '1', media_type: 'image/png', ordinal: 1, url: '/api/v1/tasks/proto/assets/preview' }] },
    { id: 'build', kind: 'task', task_id: 'build', capability: 'creative-lab-build', created_at: at(3), assets: [
      { id: 'mtl', role: 'material', variant: 'mtl', media_type: 'text/plain', ordinal: 1, url: '/api/v1/tasks/build/assets/mtl' },
      { id: 'glb', role: 'model', variant: 'glb', media_type: 'model/gltf-binary', ordinal: 1, url: '/api/v1/tasks/build/assets/glb' },
      { id: 'obj', role: 'model', variant: 'obj', media_type: 'model/obj', ordinal: 1, url: '/api/v1/tasks/build/assets/obj' },
    ] },
  ],
  edges: [
    { from: 'upload:photo', to: 'proto', kind: 'reference', evidence: 'recorded', asset_id: 'photo' },
    { from: 'proto', to: 'build', kind: 'source_task', evidence: 'recorded' },
  ],
});

describe('Creative Lab lineage', () => {
  it('names the paired design preview as the source of a build', () => {
    const versions = buildVersionTiles({ schema, graph, tracked: [], sessionId: 's1' });
    const source = detailSource(schema, 'build', versions);
    expect(source.label).not.toBe(schemaText(schema, 'ui.result.detail.unknown_source'));
    expect(source.parentKey).toBe(versions.byTask.get('proto'));
  });
});
