import { describe, expect, it } from 'vitest';
import type { CanvasNode } from '@/store/canvas-store';
import { computeNodesHash, nodeToRecord, recordToNode } from './node-persistence';

const shape: CanvasNode = {
  id: 'rectangle', type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '',
  x: 10, y: 20, width: 100, height: 60, zIndex: 1,
};

describe('canvas node persistence', () => {
  it.each([
    { strokeWidth: 8 }, { strokeColor: '#ff0000' }, { fillColor: '#00ff00' },
  ])('saves a style-only edit %j and restores the exact value', (style) => {
    const edited = { ...shape, ...style };
    expect(computeNodesHash([edited])).not.toBe(computeNodesHash([shape]));
    const record = JSON.parse(JSON.stringify(nodeToRecord(edited)));
    expect(recordToNode(record)).toMatchObject(style);
  });

  it('preserves zero line width and transparent fill', () => {
    const style = { strokeWidth: 0, strokeColor: '#ff0000', fillColor: 'transparent' };
    expect(recordToNode(nodeToRecord({ ...shape, ...style }))).toMatchObject(style);
  });

  it('keeps old nodes without style eligible for schema rendering defaults', () => {
    const restored = recordToNode(nodeToRecord(shape));
    expect(restored).toMatchObject({ id: shape.id, shapeType: 'rectangle' });
    expect(restored?.strokeWidth).toBeUndefined();
    expect(restored?.strokeColor).toBeUndefined();
    expect(restored?.fillColor).toBeUndefined();
  });
});

describe('canvas node persistence new fields', () => {
  it('round-trips rotation, opacity and a valid crop', () => {
    const node: CanvasNode = {
      ...shape,
      rotation: 45,
      opacity: 0.8,
      crop: { x: 5, y: 6, width: 50, height: 40 },
    };
    const restored = recordToNode(JSON.parse(JSON.stringify(nodeToRecord(node))));
    expect(restored).toMatchObject({
      rotation: 45,
      opacity: 0.8,
      crop: { x: 5, y: 6, width: 50, height: 40 },
    });
  });

  it.each([
    { name: 'NaN width', crop: { x: 0, y: 0, width: Number.NaN, height: 10 } },
    { name: 'negative width', crop: { x: 0, y: 0, width: -5, height: 10 } },
    { name: 'non-numeric y', crop: { x: 0, y: '6', width: 50, height: 40 } },
    { name: 'missing height', crop: { x: 0, y: 0, width: 50 } },
    { name: 'null', crop: null },
  ])('drops a corrupted crop ($name) instead of feeding drawImage NaN', ({ crop }) => {
    const record = JSON.parse(JSON.stringify(nodeToRecord(shape)));
    record.data = JSON.stringify({ ...JSON.parse(record.data), crop });
    const restored = recordToNode(record);
    expect(restored?.crop).toBeUndefined();
  });
});

describe('merged version marks', () => {
  it('round-trips where a merged picture came from and when', () => {
    const node: CanvasNode = { ...shape, type: 'image', shapeType: undefined, imageUrl: '/a', mergedFrom: 'task1', mergedAt: '2026-10-08T10:00:00Z' };
    const restored = recordToNode(JSON.parse(JSON.stringify(nodeToRecord(node))));
    expect(restored).toMatchObject({ mergedFrom: 'task1', mergedAt: '2026-10-08T10:00:00Z' });
    expect(computeNodesHash([node])).not.toBe(computeNodesHash([{ ...node, mergedFrom: undefined, mergedAt: undefined }]));
  });
});

describe('layer names', () => {
  it('round-trips a layer name and leaves unnamed layers unnamed', () => {
    expect(recordToNode(JSON.parse(JSON.stringify(nodeToRecord({ ...shape, name: '局部重绘' }))))?.name).toBe('局部重绘');
    expect(recordToNode(nodeToRecord(shape))?.name).toBeUndefined();
    expect(computeNodesHash([{ ...shape, name: 'a' }])).not.toBe(computeNodesHash([shape]));
  });
});

describe('inpaint mask source', () => {
  const maskAssetId = 'asset_0123456789abcdef0123456789abcdef';
  const patch: CanvasNode = {
    ...shape, type: 'image', shapeType: undefined, imageUrl: '/api/v1/assets/result',
    inpaint: { maskAssetId, width: 3840, height: 2160 },
  };

  it('round-trips the mask asset and its pixels on an image layer', () => {
    const restored = recordToNode(JSON.parse(JSON.stringify(nodeToRecord(patch))));
    expect(restored?.inpaint).toEqual({ maskAssetId, width: 3840, height: 2160 });
    expect(computeNodesHash([patch])).not.toBe(computeNodesHash([{ ...patch, inpaint: undefined }]));
  });

  it('drops malformed or non-platform references without losing the layer', () => {
    for (const inpaint of [
      { maskAssetId: 'https://example.test/mask.png', width: 3840, height: 2160 },
      { maskAssetId: 'data:image/png;base64,AA', width: 3840, height: 2160 },
      { maskAssetId, width: 0, height: 2160 },
      { maskAssetId, width: 3840.5, height: 2160 },
      { maskAssetId, width: 3840, height: -1 },
      null,
    ]) {
      const record = JSON.parse(JSON.stringify(nodeToRecord({ ...patch, inpaint: inpaint as never })));
      if (inpaint === null) record.data = JSON.stringify({ ...JSON.parse(record.data), inpaint: null });
      const restored = recordToNode(record);
      expect(restored?.id).toBe(patch.id);
      expect(restored?.inpaint).toBeUndefined();
    }
  });

  it('drops URL, token and other extra fields from both directions', () => {
    const dirty = {
      maskAssetId, width: 3840, height: 2160,
      url: 'https://example.test/mask.png?token=secret',
      token: 'secret',
    };
    const record = nodeToRecord({ ...patch, inpaint: dirty });
    expect(JSON.parse(record.data).inpaint).toEqual({ maskAssetId, width: 3840, height: 2160 });
    const stored = { ...JSON.parse(record.data), inpaint: { ...dirty, extra: 'x' } };
    const restored = recordToNode({ ...record, data: JSON.stringify(stored) });
    expect(restored?.inpaint).toEqual({ maskAssetId, width: 3840, height: 2160 });
    expect(restored).not.toHaveProperty('inpaint.url');
  });

  it('keeps the reference off non-image layers', () => {
    const record = JSON.parse(JSON.stringify(nodeToRecord({ ...shape, inpaint: patch.inpaint })));
    expect(JSON.parse(record.data).inpaint).toBeUndefined();
  });
});
