export const GAUSSIAN_SPLAT_RECORD_BYTES = 32;
export const GAUSSIAN_SPLAT_MAX_BYTES = 32 * 1024 * 1024;
export const GAUSSIAN_SPLAT_MAX_POINTS = GAUSSIAN_SPLAT_MAX_BYTES / GAUSSIAN_SPLAT_RECORD_BYTES;

export interface GaussianSplatBounds {
  min: [number, number, number];
  max: [number, number, number];
}

export interface ParsedGaussianSplat {
  pointCount: number;
  bounds: GaussianSplatBounds;
}

function invalid(message: string): never {
  throw new Error(message);
}

/** Validate the bounded little-endian 32-byte Tripo .splat profile and compute conservative 3σ bounds. */
export function parseGaussianSplat(bytes: Uint8Array): ParsedGaussianSplat {
  if (bytes.byteLength === 0 || bytes.byteLength > GAUSSIAN_SPLAT_MAX_BYTES
    || bytes.byteLength % GAUSSIAN_SPLAT_RECORD_BYTES !== 0) return invalid('Invalid Gaussian Splat byte length');

  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const min: [number, number, number] = [Infinity, Infinity, Infinity];
  const max: [number, number, number] = [-Infinity, -Infinity, -Infinity];
  const count = bytes.byteLength / GAUSSIAN_SPLAT_RECORD_BYTES;
  let hasVisiblePoint = false;
  for (let i = 0; i < count; i += 1) {
    const offset = i * GAUSSIAN_SPLAT_RECORD_BYTES;
    const position: [number, number, number] = [0, 0, 0];
    const scales: [number, number, number] = [0, 0, 0];
    for (let axis = 0; axis < 3; axis += 1) {
      const value = view.getFloat32(offset + axis * 4, true);
      if (!Number.isFinite(value) || Math.abs(value) > 1024) return invalid('Invalid Gaussian Splat position');
      const scale = view.getFloat32(offset + 12 + axis * 4, true);
      if (!Number.isFinite(scale) || scale < 1 / 65536 || scale > 1024) return invalid('Invalid Gaussian Splat scale');
      position[axis] = value;
      scales[axis] = scale;
    }
    const quaternion = [bytes[offset + 28], bytes[offset + 29], bytes[offset + 30], bytes[offset + 31]];
    if (quaternion.every((value) => value === 128)) return invalid('Invalid Gaussian Splat rotation');
    // The .splat byte format stores quaternion components in w, x, y, z order.
    let [qw, qx, qy, qz] = quaternion.map((value) => (value - 128) / 128);
    const quaternionLength = Math.hypot(qx, qy, qz, qw);
    qx /= quaternionLength;
    qy /= quaternionLength;
    qz /= quaternionLength;
    qw /= quaternionLength;
    const rotation = [
      [1 - 2 * (qy * qy + qz * qz), 2 * (qx * qy - qz * qw), 2 * (qx * qz + qy * qw)],
      [2 * (qx * qy + qz * qw), 1 - 2 * (qx * qx + qz * qz), 2 * (qy * qz - qx * qw)],
      [2 * (qx * qz - qy * qw), 2 * (qy * qz + qx * qw), 1 - 2 * (qx * qx + qy * qy)],
    ];
    // Transparent records remain validated, but cannot affect visible camera framing.
    if (bytes[offset + 27] === 0) continue;
    hasVisiblePoint = true;
    for (let axis = 0; axis < 3; axis += 1) {
      const radius = 3 * Math.hypot(rotation[axis][0] * scales[0], rotation[axis][1] * scales[1], rotation[axis][2] * scales[2]);
      min[axis] = Math.min(min[axis], position[axis] - radius);
      max[axis] = Math.max(max[axis], position[axis] + radius);
    }
  }
  if (!hasVisiblePoint) return invalid('Gaussian Splat has no visible points');
  return { pointCount: count, bounds: { min, max } };
}

/** Read a response incrementally so a false/missing Content-Length cannot exceed the profile budget. */
export async function readGaussianSplatResponse(response: Response): Promise<Uint8Array> {
  if (!response.ok || !response.body) throw new Error(`Gaussian Splat download failed (${response.status})`);
  const declaredSize = Number(response.headers.get('content-length'));
  if (Number.isFinite(declaredSize) && declaredSize > GAUSSIAN_SPLAT_MAX_BYTES) throw new Error('Gaussian Splat exceeds the size limit');
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      length += value.byteLength;
      if (length > GAUSSIAN_SPLAT_MAX_BYTES) throw new Error('Gaussian Splat exceeds the size limit');
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    throw error;
  }
  const bytes = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  return bytes;
}
