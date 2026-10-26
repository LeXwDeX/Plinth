import { formatNumber } from '@/lib/i18n/format';

/**
 * 3D 模型的统计：从模型自带的 glTF JSON 读出，不向服务端要。读不出来（没有 JSON、
 * 结构不符）就返回 null，界面不显示这些数字，而不是显示 0。
 */
export interface ModelStats {
  triangles: number;
  vertices: number;
  meshes: number;
  materials: number;
  textures: number;
  animations: number;
}

type Json = Record<string, unknown>;

function list(value: unknown): Json[] {
  return Array.isArray(value) ? value.filter((item): item is Json => item !== null && typeof item === 'object') : [];
}

function count(accessors: Json[], index: unknown): number {
  if (typeof index !== 'number') return 0;
  const value = accessors[index]?.count;
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0;
}

/** glTF primitive modes: 4 triangles, 5 strip, 6 fan; points and lines have no faces. */
function trianglesOf(mode: unknown, indices: number): number {
  const kind = typeof mode === 'number' ? mode : 4;
  if (kind === 4) return Math.floor(indices / 3);
  if (kind === 5 || kind === 6) return Math.max(indices - 2, 0);
  return 0;
}

export function modelStatsFromGltf(gltf: unknown): ModelStats | null {
  if (gltf === null || typeof gltf !== 'object') return null;
  const json = gltf as Json;
  const accessors = list(json.accessors);
  const meshes = list(json.meshes);
  if (meshes.length === 0 || accessors.length === 0) return null;
  let triangles = 0;
  let vertices = 0;
  for (const mesh of meshes) {
    for (const primitive of list(mesh.primitives)) {
      const attributes = primitive.attributes !== null && typeof primitive.attributes === 'object' ? primitive.attributes as Json : {};
      const positions = count(accessors, attributes.POSITION);
      vertices += positions;
      triangles += trianglesOf(primitive.mode, primitive.indices !== undefined ? count(accessors, primitive.indices) : positions);
    }
  }
  if (vertices === 0) return null;
  return {
    triangles,
    vertices,
    meshes: meshes.length,
    materials: list(json.materials).length,
    textures: list(json.textures).length,
    animations: list(json.animations).length,
  };
}

export interface TextureSize { width: number; height: number }

/** 4608 -> "4,608"; the full number, grouped the way the UI locale groups digits. */
export function groupedCount(value: number, locale: string): string {
  return formatNumber(Math.round(value), locale);
}
