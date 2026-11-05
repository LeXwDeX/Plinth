// Generates meshopt_vectors.json, the EXT_meshopt_compression test vectors,
// with the official meshoptimizer encoder and decoder (the WASM builds in the
// npm package the frontend already depends on). The Go decoder is checked
// against the decoder's bytes, so the vectors come from a second
// implementation.
// Usage: node meshopt_vectors.cjs <meshoptimizer package dir> meshopt_vectors.json
'use strict';
const path = require('path');
const fs = require('fs');

const dir = process.argv[2];
const output = process.argv[3];
const MeshoptEncoder = require(path.join(dir, 'meshopt_encoder.js'));
const MeshoptDecoder = require(path.join(dir, 'meshopt_decoder.js'));

// Deterministic PRNG (mulberry32) so vectors are reproducible.
function rng(seed) {
	return function () {
		seed |= 0;
		seed = (seed + 0x6d2b79f5) | 0;
		let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const b64 = (u8) => Buffer.from(u8.buffer, u8.byteOffset, u8.byteLength).toString('base64');
const bytes = (typed) => new Uint8Array(typed.buffer, typed.byteOffset, typed.byteLength);

function decode(count, stride, compressed, mode, filter) {
	const target = new Uint8Array(count * stride);
	MeshoptDecoder.decodeGltfBuffer(target, count, stride, compressed, mode, filter);
	return target;
}

const cases = [];
function add(name, mode, filter, count, stride, source) {
	const compressed = MeshoptEncoder.encodeGltfBuffer(source, count, stride, mode);
	const decoded = decode(count, stride, compressed, mode, filter);
	if (filter === undefined && mode !== 'TRIANGLES') {
		// Lossless modes must round-trip exactly.
		if (Buffer.compare(Buffer.from(decoded), Buffer.from(source)) !== 0) {
			throw new Error(name + ' did not round-trip');
		}
	}
	if (mode === 'TRIANGLES') {
		// The triangle codec may rotate each triangle's corners.
		const read = (u8, i) => (stride === 2 ? new Uint16Array(u8.buffer, u8.byteOffset, count)[i] : new Uint32Array(u8.buffer, u8.byteOffset, count)[i]);
		for (let t = 0; t < count / 3; t++) {
			const want = [read(source, 3 * t), read(source, 3 * t + 1), read(source, 3 * t + 2)];
			const got = [read(decoded, 3 * t), read(decoded, 3 * t + 1), read(decoded, 3 * t + 2)];
			const rotated = [0, 1, 2].some((r) => got.every((v, k) => v === want[(k + r) % 3]));
			if (!rotated) throw new Error(name + ' changed triangle ' + t);
		}
	}
	cases.push({ name, mode, filter: filter || 'NONE', count, stride, compressed: b64(compressed), decoded: b64(decoded) });
}

(async () => {
	await MeshoptEncoder.ready;
	await MeshoptDecoder.ready;

	// Smooth positions with noise: two vertex blocks at stride 12.
	{
		const random = rng(1);
		const count = 300;
		const positions = new Float32Array(count * 3);
		for (let i = 0; i < count; i++) {
			positions[i * 3] = Math.cos(i * 0.05) * 2 + random() * 0.01;
			positions[i * 3 + 1] = i * 0.01;
			positions[i * 3 + 2] = Math.sin(i * 0.05) * 2 + random() * 0.01;
		}
		add('vertex_positions', 'ATTRIBUTES', undefined, count, 12, bytes(positions));
	}
	// Random bytes exercise the verbatim group mode.
	{
		const random = rng(2);
		const data = new Uint8Array(70 * 16);
		for (let i = 0; i < data.length; i++) data[i] = Math.floor(random() * 256);
		add('vertex_random', 'ATTRIBUTES', undefined, 70, 16, data);
	}
	// Constant elements compress to zero-delta groups.
	{
		const data = new Uint8Array(40 * 8);
		for (let i = 0; i < data.length; i++) data[i] = (i % 8) * 17;
		add('vertex_constant', 'ATTRIBUTES', undefined, 40, 8, data);
	}
	// Small deltas use the 2- and 4-bit group modes with sentinels.
	{
		const random = rng(3);
		const data = new Uint8Array(600 * 4);
		let value = 0;
		for (let i = 0; i < 600; i++) {
			value = (value + Math.floor(random() * 7) - 3) & 0xff;
			data[i * 4] = value;
			data[i * 4 + 1] = (value * 3) & 0xff;
			data[i * 4 + 2] = random() < 0.1 ? Math.floor(random() * 256) : 5;
			data[i * 4 + 3] = i & 0xff;
		}
		add('vertex_small_deltas', 'ATTRIBUTES', undefined, 600, 4, data);
	}
	// A 12x12 grid in natural order and a shuffled triangle soup.
	{
		const side = 12;
		const indices = [];
		for (let y = 0; y < side - 1; y++) {
			for (let x = 0; x < side - 1; x++) {
				const a = y * side + x;
				indices.push(a, a + 1, a + side, a + 1, a + side + 1, a + side);
			}
		}
		add('triangles_grid_u16', 'TRIANGLES', undefined, indices.length, 2, bytes(new Uint16Array(indices)));
		add('triangles_grid_u32', 'TRIANGLES', undefined, indices.length, 4, bytes(new Uint32Array(indices)));
		const random = rng(4);
		const soup = [];
		for (let i = 0; i < 90; i++) {
			soup.push(Math.floor(random() * 5000), Math.floor(random() * 5000), Math.floor(random() * 5000));
		}
		add('triangles_soup_u32', 'TRIANGLES', undefined, soup.length, 4, bytes(new Uint32Array(soup)));
	}
	// Index sequences, for example line or point lists.
	{
		const random = rng(5);
		const sequence = [];
		for (let i = 0; i < 200; i++) sequence.push(i % 3 === 0 ? Math.floor(random() * 70000) : (sequence[i - 1] || 0) + 1);
		add('sequence_u32', 'INDICES', undefined, sequence.length, 4, bytes(new Uint32Array(sequence)));
		add('sequence_u16', 'INDICES', undefined, 100, 2, bytes(new Uint16Array(sequence.slice(0, 100).map((v) => v & 0xffff))));
	}
	// Filters: octahedral normals at 8 and 16 bits, quaternions, exponents.
	{
		const random = rng(6);
		const count = 64;
		const normals = new Float32Array(count * 4);
		for (let i = 0; i < count; i++) {
			let x = random() * 2 - 1, y = random() * 2 - 1, z = random() * 2 - 1;
			const l = Math.hypot(x, y, z) || 1;
			normals[i * 4] = x / l;
			normals[i * 4 + 1] = y / l;
			normals[i * 4 + 2] = z / l;
			normals[i * 4 + 3] = 1;
		}
		add('filter_octahedral_8', 'ATTRIBUTES', 'OCTAHEDRAL', count, 4, MeshoptEncoder.encodeFilterOct(normals, count, 4, 8));
		add('filter_octahedral_16', 'ATTRIBUTES', 'OCTAHEDRAL', count, 8, MeshoptEncoder.encodeFilterOct(normals, count, 8, 16));
		const quaternions = new Float32Array(count * 4);
		for (let i = 0; i < count; i++) {
			let x = random() * 2 - 1, y = random() * 2 - 1, z = random() * 2 - 1, w = random() * 2 - 1;
			const l = Math.hypot(x, y, z, w) || 1;
			quaternions.set([x / l, y / l, z / l, w / l], i * 4);
		}
		add('filter_quaternion', 'ATTRIBUTES', 'QUATERNION', count, 8, MeshoptEncoder.encodeFilterQuat(quaternions, count, 8, 12));
		const values = new Float32Array(count * 3);
		for (let i = 0; i < values.length; i++) values[i] = (random() - 0.5) * 1000;
		add('filter_exponential', 'ATTRIBUTES', 'EXPONENTIAL', count, 12, MeshoptEncoder.encodeFilterExp(values, count, 12, 15, 'Separate'));
	}
	// The unit cube used by the GLB test: positions and triangles.
	{
		const positions = new Float32Array([
			-0.5, -0.5, -0.5, 0.5, -0.5, -0.5, 0.5, 0.5, -0.5, -0.5, 0.5, -0.5,
			-0.5, -0.5, 0.5, 0.5, -0.5, 0.5, 0.5, 0.5, 0.5, -0.5, 0.5, 0.5,
		]);
		add('cube_positions', 'ATTRIBUTES', undefined, 8, 12, bytes(positions));
		const indices = new Uint16Array([
			4, 5, 6, 4, 6, 7, 1, 0, 3, 1, 3, 2, 5, 1, 2, 5, 2, 6,
			0, 4, 7, 0, 7, 3, 7, 6, 2, 7, 2, 3, 0, 1, 5, 0, 5, 4,
		]);
		add('cube_triangles', 'TRIANGLES', undefined, 36, 2, bytes(indices));
	}

	fs.writeFileSync(output, JSON.stringify({
		generator: 'meshoptimizer 0.22.0 encodeGltfBuffer; expected bytes from its decodeGltfBuffer',
		cases,
	}, null, '\t') + '\n');
})().catch((error) => {
	console.error(error);
	process.exit(1);
});
