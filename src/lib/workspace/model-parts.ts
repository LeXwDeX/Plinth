import { applyViewMode, meshesOf, type MaterialLike, type MaterialMemory, type SceneNodeLike, type ViewMode } from './view-mode';

/**
 * The parts of a loaded model: each of its own meshes (a model made in a split mode carries several).
 * The viewer's scene is three.js's, reached through structural types like view-mode does. A part can be
 * lit on the stage with a tinted copy laid over it, and exported alone by hiding the others while the
 * viewer writes its GLB.
 */
export interface ModelPart {
  /** The mesh's uuid: stable while the model stays loaded. */
  id: string;
  /** The name the file gave the mesh; empty when it has none. */
  name: string;
  triangles: number;
}

/** What the stage needs of the viewer element to export: `exportScene` of model-viewer. */
export interface PartExporter {
  exportScene(options: { binary: boolean; onlyVisible: boolean }): Promise<Blob>;
}

const HIGHLIGHT_COLOR = 0x4f8cff;
const HIGHLIGHT_OPACITY = 0.45;

/** Marks the tinted copy this module adds, so it is never taken for the model's own mesh. */
const PART_HIGHLIGHT = 'modelPartHighlight';

/** The tinted copy currently laid over a part of each model. */
const highlights = new WeakMap<SceneNodeLike, { source: SceneNodeLike; overlay: SceneNodeLike }>();

function triangleCount(mesh: SceneNodeLike): number {
  const geometry = mesh.geometry;
  const count = geometry?.index?.count ?? geometry?.attributes?.position?.count ?? 0;
  return Math.floor(count / 3);
}

export function listModelParts(root: SceneNodeLike): ModelPart[] {
  return meshesOf(root).flatMap((mesh) => (mesh.uuid ? [{ id: mesh.uuid, name: mesh.name || '', triangles: triangleCount(mesh) }] : []));
}

/** A see-through tinted version of a material, drawn a little above the surface it covers. */
function highlightMaterialOf(material: MaterialLike): MaterialLike | null {
  const tint = material.clone?.();
  if (!tint) return null;
  tint.map = null;
  tint.normalMap = null;
  tint.roughnessMap = null;
  tint.metalnessMap = null;
  tint.emissiveMap = null;
  tint.aoMap = null;
  tint.vertexColors = false;
  tint.wireframe = false;
  tint.color?.setHex(HIGHLIGHT_COLOR);
  tint.transparent = true;
  tint.opacity = HIGHLIGHT_OPACITY;
  tint.depthWrite = false;
  tint.alphaTest = 0;
  // Pulled toward the camera so it wins over the surface it copies.
  tint.polygonOffset = true;
  tint.polygonOffsetFactor = -1;
  tint.polygonOffsetUnits = -1;
  tint.needsUpdate = true;
  return tint;
}

function removeHighlight(root: SceneNodeLike): void {
  const current = highlights.get(root);
  if (!current) return;
  current.overlay.parent?.remove?.(current.overlay);
  const materials = current.overlay.material;
  if (materials) for (const material of Array.isArray(materials) ? materials : [materials]) material.dispose?.();
  highlights.delete(root);
}

/** Tints one part of the model (null clears it). Returns whether a part is now lit. */
export function setPartHighlight(root: SceneNodeLike, id: string | null): boolean {
  removeHighlight(root);
  if (id === null) return false;
  const source = meshesOf(root).find((mesh) => mesh.uuid === id);
  if (!source?.clone || !source.parent?.add) return false;
  const original = Array.isArray(source.material) ? source.material : [source.material as MaterialLike];
  const tints = original.map(highlightMaterialOf);
  if (tints.some((tint) => !tint)) { tints.forEach((tint) => tint?.dispose?.()); return false; }
  const overlay = source.clone(false);
  overlay.material = Array.isArray(source.material) ? (tints as MaterialLike[]) : (tints[0] as MaterialLike);
  // A blank name keeps animation tracks, which find nodes by name, on the model's own mesh.
  overlay.name = '';
  overlay.userData = { ...overlay.userData, [PART_HIGHLIGHT]: true, noHit: true };
  if (source.morphTargetInfluences) overlay.morphTargetInfluences = source.morphTargetInfluences;
  source.parent.add(overlay);
  highlights.set(root, { source, overlay });
  return true;
}

/** The part a hit object belongs to: itself or the nearest mesh above it that is the model's own. */
export function partFromHit(object: SceneNodeLike | null | undefined): string | null {
  for (let node = object ?? null; node; node = node.parent ?? null) {
    if (node.userData?.noHit) return null;
    if (node.isMesh && node.material) return node.uuid ?? null;
  }
  return null;
}

/**
 * Writes one part as a GLB: the textures are put back for the export, every other mesh (and the
 * overlays) is hidden, and the viewer exports only what is visible. The look and the visibility
 * come back whether the export works or not.
 */
export async function exportPart(element: PartExporter, root: SceneNodeLike, id: string, memory: MaterialMemory, restoreMode: ViewMode = 'texture'): Promise<Blob> {
  const target = meshesOf(root).find((mesh) => mesh.uuid === id);
  if (!target) throw new Error(`part ${id} is not in the model`);
  const hidden: SceneNodeLike[] = [];
  try {
    applyViewMode(root, 'texture', memory);
    root.traverse((node) => {
      if (!node.isMesh || node === target || node.visible === false) return;
      node.visible = false;
      hidden.push(node);
    });
    return await element.exportScene({ binary: true, onlyVisible: true });
  } finally {
    for (const node of hidden) node.visible = true;
    applyViewMode(root, restoreMode, memory);
  }
}

/** Names the object a list of parts belongs to, so a list read for one model is never shown for another. */
export function partsKeyOf(object: { taskId: string; assetId?: string }): string {
  return `${object.taskId}:${object.assetId ?? ''}`;
}

/** "<base>-<part name or fallback>.glb", without characters file systems refuse. */
export function partFilename(base: string, part: ModelPart, fallbackName: string): string {
  const clean = (value: string) => value.trim().replace(/[\\/:*?"<>|\u0000-\u001f]/g, '').replace(/\s+/g, '-').slice(0, 60);
  return `${[clean(base), clean(part.name) || clean(fallbackName)].filter(Boolean).join('-')}.glb`;
}
