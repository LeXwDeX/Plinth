export interface ModelCaptureView {
	camera_orbit: string;
	camera_target: string;
	field_of_view: string;
	environment: string;
	animation_name?: string;
	animation_time_ms: number;
	renderer_version: string;
}

export interface LoadedModelCaptureSource {
	loaded: boolean;
	toBlob(options: { mimeType: string; idealAspect: boolean }): Promise<Blob>;
	getCameraOrbit(): { toString(): string };
	getCameraTarget(): { toString(): string };
	getFieldOfView(): number;
	currentTime: number;
	animationName?: string | void;
	getAttribute?(name: string): string | null;
}

export interface ResizableModelCaptureSource extends LoadedModelCaptureSource {
	style: CSSStyleDeclaration;
	getBoundingClientRect(): { width: number; height: number };
	getIdealAspect(): number;
	readonly paused: boolean;
	pause(): void;
	play(): void;
	cameraOrbit: string;
	cameraTarget: string;
	fieldOfView: string;
	jumpCameraToGoal(): void;
	readonly updateComplete: Promise<boolean>;
}

interface RenderScaleControl {
	minimumRenderScale: number;
}

let highResolutionCaptureActive = false;

function nextFrame(): Promise<void> {
	return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

/** Re-render the already loaded scene; resizing an existing PNG cannot reveal model detail. */
export async function captureLoadedModelHighResolution(
	source: ResizableModelCaptureSource,
	backgroundColor: string,
	longEdgePixels: number,
	devicePixelRatio: number,
	renderScale?: RenderScaleControl,
): Promise<{ blob: Blob; view: ModelCaptureView }> {
	if (!source.loaded) throw new Error('model is not ready');
	if (highResolutionCaptureActive) throw new Error('model capture already active');
	const rect = source.getBoundingClientRect();
	if (!Number.isFinite(rect.width) || !Number.isFinite(rect.height) || rect.width <= 0 || rect.height <= 0 ||
		!Number.isInteger(longEdgePixels) || longEdgePixels < 512 || longEdgePixels > 4096 ||
		!Number.isFinite(devicePixelRatio) || devicePixelRatio <= 0) {
		throw new Error('invalid model capture dimensions');
	}
	const scale = longEdgePixels / (Math.max(rect.width, rect.height) * devicePixelRatio);
	const cssWidth = rect.width * scale;
	const cssHeight = rect.height * scale;
	const savedStyle = source.style.cssText;
	const wasPaused = source.paused;
	const effectiveFieldOfView = source.getFieldOfView();
	const aspectCompensation = Math.max(1, source.getIdealAspect() / (rect.width / rect.height));
	const declaredFieldOfView = 2 * Math.atan(
		Math.tan(effectiveFieldOfView * Math.PI / 360) / aspectCompensation,
	) * 180 / Math.PI;
	if (!Number.isFinite(declaredFieldOfView) || declaredFieldOfView <= 0) {
		throw new Error('invalid model camera field of view');
	}
	const pose = {
		orbit: source.getCameraOrbit().toString(),
		target: source.getCameraTarget().toString(),
		fieldOfView: `${declaredFieldOfView}deg`,
	};
	const previousRenderScale = renderScale?.minimumRenderScale;
	highResolutionCaptureActive = true;
	try {
		// model-viewer dynamically lowers the shared renderer scale after a drag.
		// The global capture lock also guards this temporary full-resolution pin.
		if (renderScale) renderScale.minimumRenderScale = 1;
		source.pause();
		// A resize makes model-viewer jump to the camera goal. Pin the actual
		// visible pose first, including any unfinished drag/zoom damping.
		source.cameraOrbit = pose.orbit;
		source.cameraTarget = pose.target;
		source.fieldOfView = pose.fieldOfView;
		source.jumpCameraToGoal();
		await source.updateComplete;
		// The outer viewport keeps its layout. A CSS transform keeps the same
		// composition on screen while ResizeObserver sees the larger scene size.
		source.style.position = 'absolute';
		source.style.left = '0';
		source.style.top = '0';
		source.style.width = `${cssWidth}px`;
		source.style.height = `${cssHeight}px`;
		source.style.transformOrigin = 'top left';
		source.style.transform = `scale(${rect.width / cssWidth}, ${rect.height / cssHeight})`;
		source.style.pointerEvents = 'none';
		// The renderer restores full resolution after the scene stops moving.
		// Verify decoded PNG pixels; never pass an interpolated preview as HD.
		for (let attempt = 0; attempt < 3; attempt++) {
			await nextFrame();
			await nextFrame();
			await nextFrame();
			const capture = await captureLoadedModel(source, backgroundColor);
			const bitmap = await createImageBitmap(capture.blob);
			try {
				const actualLongEdge = Math.max(bitmap.width, bitmap.height);
				if (actualLongEdge >= longEdgePixels - 2 && actualLongEdge <= longEdgePixels + 2 &&
					Math.abs(bitmap.width / bitmap.height - rect.width / rect.height) <= 0.005) {
					return capture;
				}
			} finally {
				bitmap.close();
			}
		}
		throw new Error('model capture resolution mismatch');
	} finally {
		try {
			source.style.cssText = savedStyle;
			// Keep the pose the user chose. Restoring an old auto-orbit goal would
			// visibly move the preview away from the frame just sent to 2D.
			source.cameraOrbit = pose.orbit;
			source.cameraTarget = pose.target;
			source.fieldOfView = pose.fieldOfView;
			source.jumpCameraToGoal();
			if (!wasPaused) source.play();
		} finally {
			try {
				if (renderScale) renderScale.minimumRenderScale = Number.isFinite(previousRenderScale)
					? previousRenderScale! : 0.5;
			} finally {
				highResolutionCaptureActive = false;
			}
		}
	}
}

export async function captureLoadedModel(source: LoadedModelCaptureSource, backgroundColor?: string): Promise<{
	blob: Blob;
	view: ModelCaptureView;
}> {
	if (!source.loaded) throw new Error('model is not ready');
	const view: ModelCaptureView = {
		camera_orbit: source.getCameraOrbit().toString(),
		camera_target: source.getCameraTarget().toString(),
		field_of_view: `${source.getFieldOfView()}deg`,
		environment: source.getAttribute?.('environment-image') || 'neutral',
		animation_name: source.animationName || undefined,
		animation_time_ms: Math.max(0, Math.round(source.currentTime * 1000)),
		renderer_version: 'model-viewer-webgl-capture-v2-hd',
	};
	const rendered = await source.toBlob({ mimeType: 'image/png', idealAspect: false });
	if (rendered.type !== 'image/png' || rendered.size === 0) throw new Error('model readback failed');
	// model-viewer exports only its transparent WebGL canvas. The viewport's
	// solid CSS background must be composited into the uploaded PNG so the 2D
	// canvas receives the pixels the user actually saw.
	if (!backgroundColor) return { blob: rendered, view };
	const bitmap = await createImageBitmap(rendered);
	try {
		const canvas = document.createElement('canvas');
		canvas.width = bitmap.width;
		canvas.height = bitmap.height;
		const context = canvas.getContext('2d');
		if (!context) throw new Error('capture canvas unavailable');
		context.fillStyle = backgroundColor;
		context.fillRect(0, 0, bitmap.width, bitmap.height);
		context.drawImage(bitmap, 0, 0);
		const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(
			(value) => value ? resolve(value) : reject(new Error('capture encoding failed')), 'image/png'));
		return { blob, view };
	} finally {
		bitmap.close();
	}
}
