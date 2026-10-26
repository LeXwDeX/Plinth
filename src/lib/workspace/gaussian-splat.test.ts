import { describe, expect, it } from 'vitest';
import { GAUSSIAN_SPLAT_MAX_BYTES, parseGaussianSplat, readGaussianSplatResponse } from './gaussian-splat';

function sample(overrides: Partial<{ x: number; y: number; z: number; sx: number; sy: number; sz: number; alpha: number; rotation: number }> = {}) {
  const bytes = new Uint8Array(32);
  const view = new DataView(bytes.buffer);
  view.setFloat32(0, overrides.x ?? 1, true);
  view.setFloat32(4, overrides.y ?? 0, true);
  view.setFloat32(8, overrides.z ?? 0, true);
  view.setFloat32(12, overrides.sx ?? 0.5, true);
  view.setFloat32(16, overrides.sy ?? 0.25, true);
  view.setFloat32(20, overrides.sz ?? 0.125, true);
  bytes[27] = overrides.alpha ?? 255;
  bytes.set([overrides.rotation ?? 255, 128, 128, 128], 28);
  return bytes;
}

describe('Gaussian Splat profile validation', () => {
  it('accepts the binary profile and computes 3σ bounds', () => {
    expect(parseGaussianSplat(sample())).toEqual({
      pointCount: 1,
      bounds: { min: [-0.5, -0.75, -0.375], max: [2.5, 0.75, 0.375] },
    });
  });

  it('uses conservative bounds for a rotated anisotropic splat', () => {
    const bytes = sample({ x: 0, y: 2, z: 3, sx: 1, sy: 2, sz: 3 });
    bytes.set([218, 128, 128, 218], 28); // w,x,y,z: 90° around Z, scales are unequal
    const bounds = parseGaussianSplat(bytes).bounds;
    expect(bounds.min[0]).toBeCloseTo(-6);
    expect(bounds.max[0]).toBeCloseTo(6);
    expect(bounds.min[1]).toBeCloseTo(-1);
    expect(bounds.max[1]).toBeCloseTo(5);
    expect(bounds.min[2]).toBeCloseTo(-6);
    expect(bounds.max[2]).toBeCloseTo(12);
  });

  it('validates transparent records without letting them move the visible bounds', () => {
    const visible = sample({ x: 0, sx: 1, sy: 1, sz: 1 });
    const transparent = sample({ x: 1024, sx: 1, sy: 1, sz: 1, alpha: 0 });
    const bytes = new Uint8Array([...visible, ...transparent]);
    expect(parseGaussianSplat(bytes)).toEqual({ pointCount: 2, bounds: { min: [-3, -3, -3], max: [3, 3, 3] } });
    new DataView(bytes.buffer).setFloat32(32, Infinity, true);
    expect(() => parseGaussianSplat(bytes)).toThrow(/position/);
  });

  it.each([
    ['empty', new Uint8Array()],
    ['truncated', new Uint8Array(31)],
    ['all-transparent', sample({ alpha: 0 })],
    ['bad scale', sample({ sx: 0 })],
    ['out-of-range position', sample({ x: 1025 })],
    ['bad quaternion', sample({ rotation: 128 })],
  ])('rejects %s data', (_name, bytes) => expect(() => parseGaussianSplat(bytes)).toThrow());

  it('accepts the exact byte budget and rejects one byte over it while reading', async () => {
    const exact = new Uint8Array(GAUSSIAN_SPLAT_MAX_BYTES);
    const response = new Response(new ReadableStream({
      start(controller) { controller.enqueue(exact); controller.close(); },
    }));
    await expect(readGaussianSplatResponse(response)).resolves.toHaveLength(GAUSSIAN_SPLAT_MAX_BYTES);

    const overflow = new Response(new ReadableStream({
      start(controller) { controller.enqueue(new Uint8Array(GAUSSIAN_SPLAT_MAX_BYTES)); controller.enqueue(new Uint8Array(1)); controller.close(); },
    }));
    await expect(readGaussianSplatResponse(overflow)).rejects.toThrow(/size limit/);
  });
});
