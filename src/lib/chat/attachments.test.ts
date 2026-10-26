import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import {
  buildChatSelection,
  decodeAttachments,
  decodePick,
  encodeAttachmentClaims,
} from '@/lib/chat/attachments';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

function imageNode(id: string, overrides: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id, type: 'image', imageUrl: `/api/v1/tasks/task-${id}/assets/asset-${id}`, taskId: `task-${id}`,
    x: 0, y: 0, width: 10, height: 10, zIndex: 1, ...overrides,
  };
}

describe('attachment wire format', () => {
  it('encodes claims with the Schema member names and omits empty values', () => {
    const schema = schemaFixture();
    schema.chat.api.attachment.task_id = 'configured-task';
    schema.chat.api.attachment.capture_id = 'configured-capture';
    schema.chat.api.attachment.image_slot = 'configured-slot';
    expect(encodeAttachmentClaims(schema, [
      { kind: 'image', captureId: 'capture-1', taskId: 'model-1', imageSlot: 2 },
      { kind: 'model', taskId: 'model-1', assetId: '' },
    ])).toEqual([
      { kind: 'image', 'configured-capture': 'capture-1', 'configured-task': 'model-1', 'configured-slot': 2 },
      { kind: 'model', 'configured-task': 'model-1' },
    ]);
  });

  it('sends the artboard snapshot\'s layer count under the Schema member name (#272)', () => {
    const schema = schemaFixture();
    schema.chat.api.attachment.canvas_layers = 'configured-layers';
    expect(encodeAttachmentClaims(schema, [
      { kind: 'image', url: '/api/v1/input-assets/s', canvasLayers: 2 },
      { kind: 'image', url: '/u', canvasLayers: 0 },
    ])).toEqual([
      { kind: 'image', url: '/api/v1/input-assets/s', 'configured-layers': 2 },
      { kind: 'image', url: '/u' },
    ]);
  });

  it('sends the artboard snapshot\'s layer pictures under the Schema member name (#189)', () => {
    const schema = schemaFixture();
    schema.chat.api.attachment.canvas_sources = 'configured-sources';
    expect(encodeAttachmentClaims(schema, [
      { kind: 'image', url: '/s', canvasLayers: 1, canvasSources: ['/api/v1/tasks/t/assets/a'] },
      { kind: 'image', url: '/u', canvasSources: [] },
    ])).toEqual([
      { kind: 'image', url: '/s', canvasLayers: 1, 'configured-sources': ['/api/v1/tasks/t/assets/a'] },
      { kind: 'image', url: '/u' },
    ]);
  });

  it('decodes message attachments and ignores malformed entries', () => {
    const schema = schemaFixture();
    expect(decodeAttachments(schema, [
      { handle: '@img1', kind: 'image', role: 'capture', assetId: 'a', taskId: 't', captureId: 'c', imageSlot: 1 },
      { handle: '@model1', kind: 'model', taskId: 't' },
      { handle: '', kind: 'image' },
      { handle: '@x1', kind: 'audio' },
      'junk',
      null,
    ])).toEqual([
      { handle: '@img1', kind: 'image', role: 'capture', assetId: 'a', taskId: 't', captureId: 'c', imageSlot: 1, url: undefined },
      { handle: '@model1', kind: 'model', role: undefined, assetId: undefined, taskId: 't', captureId: undefined, imageSlot: undefined, url: undefined },
    ]);
    expect(decodeAttachments(schema, undefined)).toEqual([]);
    expect(decodeAttachments(schema, 'not a list')).toEqual([]);
  });

  it('decodes a session pick even without a handle, with an empty handle for none', () => {
    const schema = schemaFixture();
    expect(decodePick(schema, { kind: 'image', assetId: 'a', taskId: 't' })).toEqual({
      handle: '', kind: 'image', role: undefined, assetId: 'a', taskId: 't', captureId: undefined, imageSlot: undefined, url: undefined,
    });
    expect(decodePick(schema, { handle: '@img2', kind: 'image', assetId: 'a', taskId: 't' })).toMatchObject({ handle: '@img2' });
    expect(decodePick(schema, { handle: '@x1', kind: 'audio' })).toBeNull();
    expect(decodePick(schema, 'junk')).toBeNull();
    expect(decodePick(schema, null)).toBeNull();
    expect(decodePick(schema, undefined)).toBeNull();
  });
});

describe('buildChatSelection', () => {
  const base = { sessionId: 'session-1', currentObject: null, nodes: [] as CanvasNode[], selectedNodeIds: [] as string[] };

  it('reports the selected image nodes of the canvas with their lineage', () => {
    const nodes = [
      imageNode('a'),
      imageNode('b', { captureId: 'capture-9', taskId: 'model-task' }),
      imageNode('c'),
      imageNode('hidden', { visible: false }),
      { ...imageNode('text'), type: 'text' as const },
    ];
    const claims = buildChatSelection({
      ...base, nodes, selectedNodeIds: ['a', 'b', 'hidden', 'text', 'gone'],
    });
    expect(claims).toEqual([
      { kind: 'image', url: '/api/v1/tasks/task-a/assets/asset-a', taskId: 'task-a' },
      { kind: 'image', url: '/api/v1/tasks/task-b/assets/asset-b', taskId: 'model-task', captureId: 'capture-9' },
    ]);
  });

  it('never reports a placeholder task or an unselected canvas', () => {
    expect(buildChatSelection({ ...base, nodes: [imageNode('a')], selectedNodeIds: [] })).toEqual([]);
    expect(buildChatSelection({
      ...base, nodes: [imageNode('a', { taskId: 'canvas:session-1' })], selectedNodeIds: ['a'],
    })).toEqual([{ kind: 'image', url: '/api/v1/tasks/task-a/assets/asset-a' }]);
  });

  it('reports the 3D model on the stage, and nothing for an image without an identity', () => {
    const currentObject = { taskId: 'model-1', kind: 'model' as const, url: '/model.glb', assetId: 'asset-1', sessionId: 'session-1' };
    expect(buildChatSelection({ ...base, currentObject })).toEqual([
      { kind: 'model', taskId: 'model-1', assetId: 'asset-1' },
    ]);
    expect(buildChatSelection({
      ...base, currentObject: { ...currentObject, kind: 'image' },
    })).toEqual([]);
    // The model on the stage wins over a canvas selection left behind.
    expect(buildChatSelection({ ...base, currentObject, nodes: [imageNode('a')], selectedNodeIds: ['a'] })).toEqual([
      { kind: 'model', taskId: 'model-1', assetId: 'asset-1' },
    ]);
  });

  it('reports a fresh 3D capture that is shown but not yet clicked', () => {
    const currentObject = {
      taskId: 'model-1', kind: 'image' as const, url: '/api/v1/assets/shot', assetId: 'shot', captureId: 'capture-1', sessionId: 'session-1',
    };
    expect(buildChatSelection({ ...base, currentObject })).toEqual([
      { kind: 'image', url: '/api/v1/assets/shot', captureId: 'capture-1', taskId: 'model-1', assetId: 'shot' },
    ]);
    // A selected node wins over the viewed capture.
    expect(buildChatSelection({
      ...base, currentObject, nodes: [imageNode('a')], selectedNodeIds: ['a'],
    })).toHaveLength(1);
  });

  it('reports nothing without an active session', () => {
    expect(buildChatSelection({
      ...base, sessionId: null, nodes: [imageNode('a')], selectedNodeIds: ['a'],
    })).toEqual([]);
  });
});
