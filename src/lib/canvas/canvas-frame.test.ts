import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import { resolveCanvasFrame } from './canvas-frame';

const canvas = catalog.application.canvas as unknown as CanvasProductSchema;
const rect: CanvasNode = { id: 'r', type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 344, y: 144, width: 512, height: 512, zIndex: 1 };

describe('resolveCanvasFrame (#246)', () => {
  it('uses a saved size as it is and never touches the nodes', () => {
    const resolved = resolveCanvasFrame(canvas, { frame: { width: 3840, height: 2160, coordinateVersion: 2 }, coordinateVersion: 2 }, [rect], { width: 1024, height: 1024 });
    expect(resolved).toEqual({ size: { width: 3840, height: 2160 }, nodes: [rect], initialized: false, migrated: false });
  });

  it('sizes a new pixel canvas (version, no size) from the initial size without scaling its nodes', () => {
    const resolved = resolveCanvasFrame(canvas, { frame: null, coordinateVersion: 2 }, [rect], { width: 2048, height: 2048 });
    expect(resolved).toEqual({ size: { width: 2048, height: 2048 }, nodes: [rect], initialized: true, migrated: false });
  });

  it('migrates a legacy canvas (no version) once into the initial pixel frame', () => {
    const resolved = resolveCanvasFrame(canvas, { frame: null, coordinateVersion: null }, [rect], { width: 2048, height: 2048 });
    expect(resolved.migrated).toBe(true);
    expect(resolved.nodes[0]).toMatchObject({ x: -424, y: -624, width: 2048, height: 2048, strokeWidth: 8 });
    // Resolving the migrated result again with its saved frame is a no-op.
    const again = resolveCanvasFrame(canvas, { frame: { ...resolved.size, coordinateVersion: 2 }, coordinateVersion: 2 }, resolved.nodes, { width: 1024, height: 1024 });
    expect(again.nodes).toBe(resolved.nodes);
  });

  it('keeps an empty legacy canvas as it is and clamps an out-of-range initial size', () => {
    const resolved = resolveCanvasFrame(canvas, {}, [], { width: 20000, height: 10 });
    expect(resolved).toEqual({ size: { width: 8192, height: 64 }, nodes: [], initialized: true, migrated: false });
  });
});
