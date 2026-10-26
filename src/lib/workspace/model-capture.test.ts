import { describe, expect, it, vi } from 'vitest';

import { captureLoadedModel, captureLoadedModelHighResolution, type LoadedModelCaptureSource, type ResizableModelCaptureSource } from './model-capture';

function source(loaded: boolean): LoadedModelCaptureSource {
	return {
		loaded,
		toBlob: vi.fn(async () => new Blob(['image'], { type: 'image/png' })),
		getCameraOrbit: () => ({ toString: () => '45deg 55deg 4m' }),
		getCameraTarget: () => ({ toString: () => '0m 0m 0m' }),
		getFieldOfView: () => 45,
		currentTime: 1.234,
		animationName: 'walk',
	};
}

describe('model capture', () => {
	it('captures only a loaded frame and records its exact pose', async () => {
		const model = source(true);
		const result = await captureLoadedModel(model);
		expect(result.blob.type).toBe('image/png');
		expect(result.view).toMatchObject({ camera_orbit: '45deg 55deg 4m',
			camera_target: '0m 0m 0m', field_of_view: '45deg',
			animation_name: 'walk', animation_time_ms: 1234 });
		expect(model.toBlob).toHaveBeenCalledWith({ mimeType: 'image/png', idealAspect: false });
	});
	it('does not read back before model load', async () => {
		const model = source(false);
		await expect(captureLoadedModel(model)).rejects.toThrow('not ready');
		expect(model.toBlob).not.toHaveBeenCalled();
	});
	it('re-renders at the requested pixel size and restores the preview state', async () => {
		const style = { cssText: 'width: 100%; height: 100%' } as CSSStyleDeclaration;
		const renderScale = { minimumRenderScale: 0.5 };
		const model = {
			...source(true), style, paused: false,
			toBlob: vi.fn(async () => {
				expect(renderScale.minimumRenderScale).toBe(1);
				return new Blob(['image'], { type: 'image/png' });
			}),
			pause: vi.fn(), play: vi.fn(),
			cameraOrbit: 'auto', cameraTarget: 'auto', fieldOfView: 'auto',
			jumpCameraToGoal: vi.fn(), updateComplete: Promise.resolve(true),
			getIdealAspect: () => 2,
			getBoundingClientRect: () => ({ width: 400, height: 200 }),
		} satisfies ResizableModelCaptureSource;
		const frame = vi.fn((callback: FrameRequestCallback) => { callback(0); return 1; });
		const close = vi.fn();
		vi.stubGlobal('requestAnimationFrame', frame);
		vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 2048, height: 1024, close })));
		try {
			const capture = await captureLoadedModelHighResolution(model, '', 2048, 2, renderScale);
			expect(capture.blob.type).toBe('image/png');
			expect(model.pause).toHaveBeenCalledOnce();
			expect(model.play).toHaveBeenCalledOnce();
			expect(frame).toHaveBeenCalledTimes(3);
			expect(model.toBlob).toHaveBeenCalledOnce();
			expect(model.cameraOrbit).toBe('45deg 55deg 4m');
			expect(model.cameraTarget).toBe('0m 0m 0m');
			expect(model.fieldOfView).toBe('45deg');
			expect(model.jumpCameraToGoal).toHaveBeenCalledTimes(2);
			expect(close).toHaveBeenCalledOnce();
			expect(style.cssText).toBe('width: 100%; height: 100%');
			expect(renderScale.minimumRenderScale).toBe(0.5);
		} finally {
			vi.unstubAllGlobals();
		}
	});
	it('rejects a low-resolution readback and still restores the preview', async () => {
		const style = { cssText: 'width: 100%' } as CSSStyleDeclaration;
		const renderScale = { minimumRenderScale: 0.75 };
		const model = {
			...source(true), style, paused: true,
			pause: vi.fn(), play: vi.fn(),
			cameraOrbit: 'auto', cameraTarget: 'auto', fieldOfView: 'auto',
			jumpCameraToGoal: vi.fn(), updateComplete: Promise.resolve(true),
			getIdealAspect: () => 2,
			getBoundingClientRect: () => ({ width: 320, height: 320 }),
		} satisfies ResizableModelCaptureSource;
		vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1; });
		vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 640, height: 640, close: vi.fn() })));
		try {
			await expect(captureLoadedModelHighResolution(model, '', 2048, 1, renderScale)).rejects.toThrow('resolution mismatch');
			expect(style.cssText).toBe('width: 100%');
			expect(renderScale.minimumRenderScale).toBe(0.75);
			expect(model.toBlob).toHaveBeenCalledTimes(3);
			expect(Number.parseFloat(model.fieldOfView)).toBeCloseTo(2 * Math.atan(Math.tan(45 * Math.PI / 360) / 2) * 180 / Math.PI, 6);
			expect(model.play).not.toHaveBeenCalled();
		} finally {
			vi.unstubAllGlobals();
		}
	});
	it('keeps the shared render scale pinned until an overlapping readback ends', async () => {
		const renderScale = { minimumRenderScale: 0.5 };
		let releaseReadback: ((blob: Blob) => void) | undefined;
		const pendingReadback = new Promise<Blob>((resolve) => { releaseReadback = resolve; });
		const model = {
			...source(true),
			toBlob: vi.fn(() => pendingReadback),
			style: { cssText: 'width: 100%' } as CSSStyleDeclaration,
			paused: false, pause: vi.fn(), play: vi.fn(),
			cameraOrbit: 'auto', cameraTarget: 'auto', fieldOfView: 'auto',
			jumpCameraToGoal: vi.fn(), updateComplete: Promise.resolve(true),
			getIdealAspect: () => 1,
			getBoundingClientRect: () => ({ width: 400, height: 400 }),
		} satisfies ResizableModelCaptureSource;
		vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1; });
		vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 2048, height: 2048, close: vi.fn() })));
		try {
			const first = captureLoadedModelHighResolution(model, '', 2048, 1, renderScale);
			await vi.waitFor(() => expect(model.toBlob).toHaveBeenCalledOnce());
			await expect(captureLoadedModelHighResolution(model, '', 2048, 1, renderScale))
				.rejects.toThrow('already active');
			expect(renderScale.minimumRenderScale).toBe(1);
			releaseReadback?.(new Blob(['image'], { type: 'image/png' }));
			await first;
			expect(renderScale.minimumRenderScale).toBe(0.5);
		} finally {
			vi.unstubAllGlobals();
		}
	});
});
