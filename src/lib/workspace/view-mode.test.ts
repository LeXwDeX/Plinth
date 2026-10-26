import { describe, expect, it } from 'vitest';

import { applyViewMode, hasMaterials, largestTextureSize, type MaterialLike, type MaterialMemory, type SceneNodeLike } from '@/lib/workspace/view-mode';

function colour(hex: number) {
  let value = hex;
  return { getHex: () => value, setHex: (next: number) => { value = next; } };
}

function material(): MaterialLike {
  const made: MaterialLike = { map: { id: 'base' }, normalMap: { id: 'normal' }, roughnessMap: { id: 'rough' }, vertexColors: true, wireframe: false, alphaTest: 0.5, polygonOffset: false, metalness: 1, roughness: 0.2, color: colour(0xff8800) };
  made.clone = function clone(this: MaterialLike) {
    const copy: MaterialLike = { ...this, color: colour(this.color?.getHex() ?? 0), disposed: false } as MaterialLike;
    copy.dispose = () => { (copy as { disposed?: boolean }).disposed = true; };
    return copy;
  };
  return made;
}

/** A three.js-like group: meshes can be copied and laid next to each other. */
type Group = SceneNodeLike & { children: SceneNodeLike[]; add(child: SceneNodeLike): void };

function group(): Group {
  const node = {
    children: [] as SceneNodeLike[],
    traverse(visit: (node: SceneNodeLike) => void) { visit(node); [...node.children].forEach((child) => child.traverse(visit)); },
    add(child: SceneNodeLike) { child.parent = node; node.children.push(child); },
    remove(child: SceneNodeLike) { node.children = node.children.filter((item) => item !== child); child.parent = null; },
  };
  return node;
}

function mesh(item: MaterialLike | MaterialLike[], userData: Record<string, unknown> = {}): SceneNodeLike {
  const node: SceneNodeLike = {
    isMesh: true, name: 'body', material: item, userData,
    traverse(visit) { visit(node); },
    clone() { const copy = mesh(node.material as MaterialLike, { ...node.userData }); copy.name = node.name; return copy; },
  };
  return node;
}

function scene(...materials: Array<MaterialLike | MaterialLike[]>): Group {
  const root = group();
  materials.forEach((item) => root.add(mesh(item)));
  return root;
}

describe('3D view modes', () => {
  it('shows clay without pictures, shininess or vertex colours', () => {
    const one = material();
    expect(applyViewMode(scene(one), 'clay', new WeakMap() as MaterialMemory)).toBe(1);
    expect(one.map).toBeNull();
    expect(one.normalMap).toBeNull();
    expect(one.vertexColors).toBe(false);
    expect(one.metalness).toBe(0);
    expect(one.wireframe).toBe(false);
    expect(one.polygonOffset).toBe(false);
    expect(one.color?.getHex()).toBe(0xcfcfcf);
    expect(one.needsUpdate).toBe(true);
  });

  it('shows wires as a see-through copy over a dark fill, and puts the original look back', () => {
    const one = material();
    const original = { ...one };
    const root = scene(one);
    const memory = new WeakMap() as MaterialMemory;
    applyViewMode(root, 'wire', memory);
    expect(one.wireframe).toBe(false);
    expect(one.map).toBeNull();
    expect(one.color?.getHex()).toBe(0x2a2d34);
    expect(one.polygonOffset).toBe(true);
    expect(root.children).toHaveLength(2);
    const overlay = root.children[1];
    const wire = overlay.material as MaterialLike;
    expect(wire).not.toBe(one);
    expect(wire.wireframe).toBe(true);
    expect(wire.transparent).toBe(true);
    expect(wire.depthWrite).toBe(false);
    expect(wire.alphaTest).toBe(0);
    expect(wire.polygonOffset).toBe(false);
    expect(wire.map).toBeNull();
    expect(wire.color?.getHex()).toBe(0xe6e6e6);
    expect(overlay.name).toBe('');
    expect(overlay.userData?.noHit).toBe(true);

    applyViewMode(root, 'wire', memory);
    expect(root.children).toHaveLength(2);

    applyViewMode(root, 'clay', memory);
    expect(root.children).toHaveLength(1);
    expect((wire as { disposed?: boolean }).disposed).toBe(true);
    expect(one.wireframe).toBe(false);
    expect(one.polygonOffset).toBe(false);
    applyViewMode(root, 'texture', memory);
    expect(one.map).toBe(original.map);
    expect(one.normalMap).toBe(original.normalMap);
    expect(one.roughnessMap).toBe(original.roughnessMap);
    expect(one.vertexColors).toBe(true);
    expect(one.wireframe).toBe(false);
    expect(one.polygonOffset).toBe(false);
    expect(one.metalness).toBe(1);
    expect(one.roughness).toBe(0.2);
    expect(one.color?.getHex()).toBe(0xff8800);
  });

  it('handles meshes with several materials and a material shared by two meshes once', () => {
    const shared = material();
    const other = material();
    const root = scene(shared, [shared, other]);
    expect(applyViewMode(root, 'wire', new WeakMap() as MaterialMemory)).toBe(2);
    expect(other.polygonOffset).toBe(true);
    expect(root.children).toHaveLength(4);
    const wires = root.children[3].material as MaterialLike[];
    expect(wires).toHaveLength(2);
    expect(wires.every((wire) => wire.wireframe)).toBe(true);
  });

  it('leaves helpers the viewer marks as not part of the model alone', () => {
    const model = material();
    const helper = material();
    const root = scene(model);
    root.add(mesh(helper, { noHit: true }));
    expect(applyViewMode(root, 'wire', new WeakMap() as MaterialMemory)).toBe(1);
    expect(helper.map).toEqual({ id: 'base' });
    expect(helper.wireframe).toBe(false);
    expect(root.children).toHaveLength(3);
  });

  it('reports a scene with no meshes as having nothing to change', () => {
    expect(hasMaterials(scene())).toBe(false);
    expect(hasMaterials(null)).toBe(false);
    expect(hasMaterials(scene(material()))).toBe(true);
  });
});

describe('largestTextureSize', () => {
  const picture = (width: number, height: number) => ({ image: { width, height } });

  it('picks the largest picture across slots and materials', () => {
    const small = { ...material(), map: picture(512, 512), normalMap: picture(1024, 256) };
    const big = { ...material(), roughnessMap: picture(2048, 1024), map: picture(64, 64) };
    expect(largestTextureSize(scene(small, big), new WeakMap() as MaterialMemory)).toEqual({ width: 2048, height: 1024 });
  });

  it('reads the original picture after clay mode removed it', () => {
    const one = { ...material(), map: picture(4096, 4096), normalMap: null, roughnessMap: null };
    const root = scene(one);
    const memory = new WeakMap() as MaterialMemory;
    applyViewMode(root, 'clay', memory);
    expect(one.map).toBeNull();
    expect(largestTextureSize(root, memory)).toEqual({ width: 4096, height: 4096 });
  });

  it('is null when no material has a sized image', () => {
    const one = { ...material(), map: { image: {} }, normalMap: picture(0, 10), roughnessMap: null };
    expect(largestTextureSize(scene(one), new WeakMap() as MaterialMemory)).toBeNull();
    expect(largestTextureSize(scene(), new WeakMap() as MaterialMemory)).toBeNull();
  });
});
