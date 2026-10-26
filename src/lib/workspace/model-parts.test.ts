import { describe, expect, it } from 'vitest';

import { exportPart, listModelParts, partFilename, partFromHit, partsKeyOf, setPartHighlight, type PartExporter } from '@/lib/workspace/model-parts';
import { applyViewMode, type MaterialLike, type MaterialMemory, type SceneNodeLike } from '@/lib/workspace/view-mode';

function colour(hex: number) {
  let value = hex;
  return { getHex: () => value, setHex: (next: number) => { value = next; } };
}

function material(): MaterialLike {
  const made: MaterialLike = { map: { id: 'base' }, vertexColors: true, wireframe: false, alphaTest: 0.5, metalness: 1, roughness: 0.2, color: colour(0xff8800) };
  made.clone = function clone(this: MaterialLike) {
    const copy: MaterialLike = { ...this, color: colour(this.color?.getHex() ?? 0) } as MaterialLike;
    copy.dispose = () => { (copy as { disposed?: boolean }).disposed = true; };
    return copy;
  };
  return made;
}

type Group = SceneNodeLike & { children: SceneNodeLike[]; add(child: SceneNodeLike): void };

function group(): Group {
  const node: Group = {
    children: [],
    visible: true,
    traverse(visit) { visit(node); [...node.children].forEach((child) => child.traverse(visit)); },
    add(child) { child.parent = node; node.children.push(child); },
    remove(child) { node.children = node.children.filter((item) => item !== child); child.parent = null; },
  };
  return node;
}

let counter = 0;

function mesh(name: string, geometry: SceneNodeLike['geometry'], userData: Record<string, unknown> = {}): SceneNodeLike {
  const node: SceneNodeLike = {
    isMesh: true, name, uuid: `uuid-${++counter}`, visible: true, geometry, material: material(), userData,
    traverse(visit) { visit(node); },
    clone() { const copy = mesh(node.name ?? '', node.geometry, { ...node.userData }); copy.material = node.material; return copy; },
  };
  return node;
}

function model() {
  const root = group();
  const head = mesh('head', { index: { count: 30 } });
  const body = mesh('', { attributes: { position: { count: 12 } } });
  root.add(head);
  root.add(body);
  return { root, head, body };
}

describe('listModelParts', () => {
  it('lists the model\'s own meshes with their triangle counts, from the index or else the positions', () => {
    const { root, head, body } = model();
    expect(listModelParts(root)).toEqual([
      { id: head.uuid, name: 'head', triangles: 10 },
      { id: body.uuid, name: '', triangles: 4 },
    ]);
  });

  it('skips the wire and highlight copies', () => {
    const { root, head } = model();
    applyViewMode(root, 'wire', new WeakMap() as MaterialMemory);
    setPartHighlight(root, head.uuid as string);
    expect(listModelParts(root)).toHaveLength(2);
  });
});

describe('setPartHighlight', () => {
  it('lays one tinted see-through copy over the part and moves it to the next part', () => {
    const { root, head, body } = model();
    expect(setPartHighlight(root, head.uuid as string)).toBe(true);
    const overlays = () => root.children.filter((node) => node.userData?.modelPartHighlight);
    expect(overlays()).toHaveLength(1);
    const tint = overlays()[0].material as MaterialLike;
    expect(tint.color?.getHex()).toBe(0x4f8cff);
    expect(tint.map).toBeNull();
    expect(tint.transparent).toBe(true);
    expect(tint.depthWrite).toBe(false);
    expect(overlays()[0].userData?.noHit).toBe(true);
    expect(overlays()[0].name).toBe('');
    // The part's own material is left alone.
    expect((head.material as MaterialLike).map).not.toBeNull();
    setPartHighlight(root, body.uuid as string);
    expect(overlays()).toHaveLength(1);
    expect(setPartHighlight(root, null)).toBe(false);
    expect(overlays()).toHaveLength(0);
  });

  it('does nothing for an unknown part, and view modes still treat only the model\'s meshes', () => {
    const { root, head } = model();
    expect(setPartHighlight(root, 'missing')).toBe(false);
    setPartHighlight(root, head.uuid as string);
    const memory = new WeakMap() as MaterialMemory;
    expect(applyViewMode(root, 'clay', memory)).toBe(2);
    expect(root.children).toHaveLength(3);
  });
});

describe('partFromHit', () => {
  it('maps a hit to its mesh and ignores helpers', () => {
    const { head } = model();
    expect(partFromHit(head)).toBe(head.uuid);
    const child: SceneNodeLike = { traverse() {}, parent: head };
    expect(partFromHit(child)).toBe(head.uuid);
    expect(partFromHit(mesh('x', undefined, { noHit: true }))).toBeNull();
    expect(partFromHit(null)).toBeNull();
  });
});

describe('exportPart', () => {
  it('exports only the part with textures back, then restores visibility and the view mode', async () => {
    const { root, head, body } = model();
    const memory = new WeakMap() as MaterialMemory;
    applyViewMode(root, 'wire', memory);
    setPartHighlight(root, head.uuid as string);
    const seen: Array<{ visible: string[]; mapKept: boolean; options: unknown }> = [];
    const exporter: PartExporter = {
      async exportScene(options) {
        const visible: string[] = [];
        root.traverse((node) => { if (node.isMesh && node.visible) visible.push(node.uuid as string); });
        seen.push({ visible, mapKept: (head.material as MaterialLike).map !== null, options });
        return new Blob(['glb']);
      },
    };
    const blob = await exportPart(exporter, root, head.uuid as string, memory, 'wire');
    expect(blob.size).toBe(3);
    expect(seen).toEqual([{ visible: [head.uuid], mapKept: true, options: { binary: true, onlyVisible: true } }]);
    expect(body.visible).toBe(true);
    root.traverse((node) => { if (node.isMesh) expect(node.visible).toBe(true); });
    // The wire view is back: the pictures are off again and its copies are laid.
    expect((head.material as MaterialLike).map).toBeNull();
    expect(root.children.filter((node) => node.isMesh).length).toBeGreaterThanOrEqual(4);
  });

  it('restores everything when the export fails, and refuses a part that is not there', async () => {
    const { root, head, body } = model();
    const memory = new WeakMap() as MaterialMemory;
    const failing: PartExporter = { exportScene: () => Promise.reject(new Error('boom')) };
    await expect(exportPart(failing, root, head.uuid as string, memory)).rejects.toThrow('boom');
    expect(body.visible).toBe(true);
    await expect(exportPart(failing, root, 'missing', memory)).rejects.toThrow();
  });
});

describe('partFilename / partsKeyOf', () => {
  it('names the file after the model and the part, falling back to the given name', () => {
    expect(partFilename('model2', { id: 'a', name: 'left arm', triangles: 1 }, '部件 2')).toBe('model2-left-arm.glb');
    expect(partFilename('model2', { id: 'a', name: '', triangles: 1 }, '部件 2')).toBe('model2-部件-2.glb');
    expect(partFilename('m', { id: 'a', name: 'a/b:c*?', triangles: 1 }, 'x')).toBe('m-abc.glb');
  });

  it('keys by task and asset', () => {
    expect(partsKeyOf({ taskId: 't1', assetId: 'a1' })).toBe('t1:a1');
    expect(partsKeyOf({ taskId: 't1' })).toBe('t1:');
  });
});
