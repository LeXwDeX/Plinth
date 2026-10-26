import type { TextureSize } from './model-stats';

/**
 * How the 3D stage shows the model: with its textures, as a plain grey clay model, or as
 * wires. The viewer draws with three.js materials, so a mode changes those materials in
 * place and remembers what each one was, to put it back when the textures come back.
 * Wires are drawn on a copy of each mesh laid over a dark fill, so only the front edges show
 * and a dense scanned mesh still reads as a mesh instead of a white blob.
 */
export type ViewMode = 'texture' | 'clay' | 'wire';

export const VIEW_MODES: readonly ViewMode[] = ['texture', 'clay', 'wire'];

/** The part of a three.js material the modes touch. */
export interface MaterialLike {
  map?: unknown;
  normalMap?: unknown;
  roughnessMap?: unknown;
  metalnessMap?: unknown;
  emissiveMap?: unknown;
  aoMap?: unknown;
  vertexColors?: boolean;
  wireframe?: boolean;
  transparent?: boolean;
  opacity?: number;
  depthWrite?: boolean;
  alphaTest?: number;
  polygonOffset?: boolean;
  polygonOffsetFactor?: number;
  polygonOffsetUnits?: number;
  metalness?: number;
  roughness?: number;
  color?: { getHex(): number; setHex(hex: number): unknown };
  needsUpdate?: boolean;
  clone?(): MaterialLike;
  dispose?(): void;
}

/**
 * A node of the viewer's scene: a mesh has a material (or several). `clone`, `parent` and
 * `add`/`remove` are three.js's own, used to lay the wire copy next to a mesh.
 */
export interface SceneNodeLike {
  isMesh?: boolean;
  name?: string;
  uuid?: string;
  visible?: boolean;
  geometry?: { index?: { count: number } | null; attributes?: { position?: { count: number } } };
  material?: MaterialLike | MaterialLike[];
  userData?: Record<string, unknown>;
  parent?: SceneNodeLike | null;
  morphTargetInfluences?: number[];
  traverse(visit: (node: SceneNodeLike) => void): void;
  clone?(recursive?: boolean): SceneNodeLike;
  add?(node: SceneNodeLike): unknown;
  remove?(node: SceneNodeLike): unknown;
}

type Remembered = Pick<MaterialLike, 'map' | 'normalMap' | 'roughnessMap' | 'metalnessMap' | 'emissiveMap' | 'aoMap' | 'vertexColors' | 'wireframe' | 'polygonOffset' | 'polygonOffsetFactor' | 'polygonOffsetUnits' | 'metalness' | 'roughness'> & { color?: number };

/** What each material looked like before a mode changed it. */
export type MaterialMemory = WeakMap<MaterialLike, Remembered>;

const CLAY_COLOR = 0xcfcfcf;
const WIRE_FILL_COLOR = 0x2a2d34;
const WIRE_COLOR = 0xe6e6e6;
const WIRE_OPACITY = 0.55;
const CLAY_ROUGHNESS = 0.85;

/** Marks the wire copies this module adds, so they are never taken for the model's own meshes. */
const WIRE_OVERLAY = 'viewModeWireOverlay';

/** The wire copy laid next to each mesh while the wire view is on. */
const wireOverlays = new WeakMap<SceneNodeLike, SceneNodeLike>();

function listOf(material: MaterialLike | MaterialLike[]): MaterialLike[] {
  return Array.isArray(material) ? material : [material];
}

/** The model's own meshes: not the wire copies, nor helpers the viewer marks as not part of the model. */
export function meshesOf(root: SceneNodeLike): SceneNodeLike[] {
  const found: SceneNodeLike[] = [];
  root.traverse((node) => {
    if (!node.isMesh || !node.material || node.userData?.[WIRE_OVERLAY] || node.userData?.noHit) return;
    found.push(node);
  });
  return found;
}

function materialsOf(root: SceneNodeLike): MaterialLike[] {
  const found = new Set<MaterialLike>();
  for (const mesh of meshesOf(root)) for (const material of listOf(mesh.material as MaterialLike | MaterialLike[])) found.add(material);
  return [...found];
}

/** The see-through wire version of a (already shape-only) material. */
function wireMaterialOf(material: MaterialLike): MaterialLike | null {
  const wire = material.clone?.();
  if (!wire) return null;
  wire.wireframe = true;
  wire.transparent = true;
  wire.opacity = WIRE_OPACITY;
  wire.depthWrite = false;
  // A cut-out material's alpha cutoff could drop the half-see-through lines.
  wire.alphaTest = 0;
  wire.polygonOffset = false;
  wire.color?.setHex(WIRE_COLOR);
  wire.needsUpdate = true;
  return wire;
}

/** Lays a wire copy of the mesh next to it, sharing its shape, transform, skeleton and morphs. */
function addWireOverlay(mesh: SceneNodeLike): void {
  if (wireOverlays.has(mesh) || !mesh.clone || !mesh.parent?.add) return;
  const wires = listOf(mesh.material as MaterialLike | MaterialLike[]).map(wireMaterialOf);
  if (wires.some((wire) => !wire)) { wires.forEach((wire) => wire?.dispose?.()); return; }
  const overlay = mesh.clone(false);
  overlay.material = Array.isArray(mesh.material) ? (wires as MaterialLike[]) : (wires[0] as MaterialLike);
  // A blank name keeps animation tracks, which find nodes by name, on the model's own mesh.
  overlay.name = '';
  overlay.userData = { ...overlay.userData, [WIRE_OVERLAY]: true, noHit: true };
  if (mesh.morphTargetInfluences) overlay.morphTargetInfluences = mesh.morphTargetInfluences;
  mesh.parent.add(overlay);
  wireOverlays.set(mesh, overlay);
}

function removeWireOverlay(mesh: SceneNodeLike): void {
  const overlay = wireOverlays.get(mesh);
  if (!overlay) return;
  overlay.parent?.remove?.(overlay);
  if (overlay.material) for (const wire of listOf(overlay.material)) wire.dispose?.();
  wireOverlays.delete(mesh);
}

/** Whether the scene has anything the modes could change. */
export function hasMaterials(root: SceneNodeLike | null | undefined): boolean {
  return Boolean(root) && materialsOf(root as SceneNodeLike).length > 0;
}

/** The texture slots a material can carry pictures in. */
const TEXTURE_SLOTS = ['map', 'normalMap', 'roughnessMap', 'metalnessMap', 'emissiveMap', 'aoMap'] as const;

/**
 * The largest picture on the loaded model's materials, as the model was made (a mode that took the
 * pictures off is looked past through `memory`). Null when no picture has a size.
 */
export function largestTextureSize(root: SceneNodeLike, memory: MaterialMemory): TextureSize | null {
  let largest: TextureSize | null = null;
  for (const material of materialsOf(root)) {
    const source = memory.get(material) ?? material;
    for (const slot of TEXTURE_SLOTS) {
      const image = (source[slot] as { image?: { width?: unknown; height?: unknown } } | null | undefined)?.image;
      const { width, height } = image ?? {};
      if (typeof width !== 'number' || typeof height !== 'number') continue;
      if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) continue;
      if (!largest || width * height > largest.width * largest.height) largest = { width, height };
    }
  }
  return largest;
}

/**
 * Puts the model into one mode. `memory` holds each material's original look the first time it
 * is changed; one memory per loaded model. Returns the number of materials changed.
 */
export function applyViewMode(root: SceneNodeLike, mode: ViewMode, memory: MaterialMemory): number {
  const meshes = meshesOf(root);
  const materials = materialsOf(root);
  for (const material of materials) {
    if (!memory.has(material)) {
      memory.set(material, {
        map: material.map, normalMap: material.normalMap, roughnessMap: material.roughnessMap, metalnessMap: material.metalnessMap,
        emissiveMap: material.emissiveMap, aoMap: material.aoMap, vertexColors: material.vertexColors, wireframe: material.wireframe,
        polygonOffset: material.polygonOffset, polygonOffsetFactor: material.polygonOffsetFactor, polygonOffsetUnits: material.polygonOffsetUnits,
        metalness: material.metalness, roughness: material.roughness, color: material.color?.getHex(),
      });
    }
    const original = memory.get(material) as Remembered;
    if (mode === 'texture') {
      const { color, ...rest } = original;
      Object.assign(material, rest);
      if (color !== undefined) material.color?.setHex(color);
    } else {
      // Clay and wires look at the shape only: no colour pictures, vertex colours or shininess.
      material.map = null;
      material.normalMap = null;
      material.roughnessMap = null;
      material.metalnessMap = null;
      material.emissiveMap = null;
      material.aoMap = null;
      material.vertexColors = false;
      material.metalness = 0;
      material.roughness = CLAY_ROUGHNESS;
      material.color?.setHex(mode === 'wire' ? WIRE_FILL_COLOR : CLAY_COLOR);
      material.wireframe = false;
      // The dark fill sits a little behind the wire copy so the front edges win the depth test
      // and the edges on the far side stay hidden.
      material.polygonOffset = mode === 'wire' ? true : original.polygonOffset;
      material.polygonOffsetFactor = mode === 'wire' ? 1 : original.polygonOffsetFactor;
      material.polygonOffsetUnits = mode === 'wire' ? 1 : original.polygonOffsetUnits;
    }
    material.needsUpdate = true;
  }
  for (const mesh of meshes) {
    if (mode === 'wire') addWireOverlay(mesh);
    else removeWireOverlay(mesh);
  }
  return materials.length;
}
