import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';
import { CanvasExportError, exportCanvasToBlob } from './export-canvas';

function canvasSchemaFixture(): CanvasProductSchema {
  return structuredClone(
    defaultCatalog.application.canvas,
  ) as unknown as CanvasProductSchema;
}

/** Eight pixels a character, ascent 10 and descent 4, so lines and baselines are predictable. */
const measureText = (value: string) => ({ width: Array.from(value).length * 8, fontBoundingBoxAscent: 10, fontBoundingBoxDescent: 4 });

function textNode(): CanvasNode {
  return {
    id: 'text-a',
    type: 'text',
    imageUrl: '',
    text: 'Schema text',
    taskId: '',
    x: 10,
    y: 20,
    width: 30,
    height: 12,
    zIndex: 1,
  };
}

describe('Canvas export Product Schema', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('uses Schema dimensions, background, text style and output media type', async () => {
    const schema = canvasSchemaFixture();
    schema.frontend.export.padding_pixels = 7;
    schema.frontend.export.default_background_color = '#010203';
    schema.frontend.export.output_media_type = 'image/configured';
    schema.frontend.text.default_color = '#040506';
    schema.frontend.text.font_family = 'configured-font';
    schema.frontend.text.line_height = 1.5;

    const operations: unknown[][] = [];
    const context = {
      fillStyle: '',
      font: '',
      textBaseline: 'alphabetic',
      globalAlpha: 1,
      measureText,
      save: vi.fn(),
      restore: vi.fn(),
      translate: vi.fn(),
      rotate: vi.fn(),
      fillRect: vi.fn(function (
        this: typeof context,
        ...values: number[]
      ) {
        operations.push(['fillRect', this.fillStyle, ...values]);
      }),
      fillText: vi.fn(function (
        this: typeof context,
        ...values: unknown[]
      ) {
        operations.push([
          'fillText',
          this.fillStyle,
          this.font,
          this.textBaseline,
          ...values,
        ]);
      }),
    };
    let encodedMediaType = '';
    const canvas = {
      width: 0,
      height: 0,
      getContext: vi.fn(() => context),
      toBlob: vi.fn((
        callback: (blob: Blob | null) => void,
        mediaType: string,
      ) => {
        encodedMediaType = mediaType;
        callback(new Blob(['configured']));
      }),
    };
    vi.stubGlobal('document', {
      createElement: vi.fn(() => canvas),
    });

    await exportCanvasToBlob([{ ...textNode(), width: 60 }], schema);

    // 60 wide node: "Schema text" (11 chars x 8px) wraps to "Schema" / "text" at the space.
    expect(canvas.width).toBe(74);
    expect(canvas.height).toBe(26);
    expect(encodedMediaType).toBe('image/configured');
    // Default font size 24, line height 1.5 -> 36px lines; first baseline (10-4)/2 + 18 = 21.
    expect(operations).toEqual([
      ['fillRect', '#010203', 0, 0, 74, 26],
      ['fillText', '#040506', 'normal normal 24px configured-font', 'alphabetic', 'Schema', 0, 21],
      ['fillText', '#040506', 'normal normal 24px configured-font', 'alphabetic', 'text', 0, 57],
    ]);
  });

  it('renders at a larger pixel scale when asked, drawing in board units', async () => {
    const schema = canvasSchemaFixture();
    schema.frontend.export.padding_pixels = 0;
    const scale = vi.fn();
    const context = {
      fillStyle: '', font: '', textBaseline: 'alphabetic', globalAlpha: 1,
      save: vi.fn(), restore: vi.fn(), translate: vi.fn(), rotate: vi.fn(), fillRect: vi.fn(), fillText: vi.fn(), measureText, scale,
    };
    const canvas = {
      width: 0, height: 0, getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => callback(new Blob(['x']))),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });

    await exportCanvasToBlob([textNode()], schema, { scale: 2, backgroundColor: schema.frontend.export.transparent_background_value });

    expect(canvas.width).toBe(60);
    expect(canvas.height).toBe(24);
    expect(scale).toHaveBeenCalledWith(2, 2);
    expect(context.fillRect).not.toHaveBeenCalled();
  });

  it('uses the configured empty export message', async () => {
    const schema = canvasSchemaFixture();
    schema.presentation.text['error.export_empty'] =
      'configured empty export';

    await expect(exportCanvasToBlob([], schema)).rejects.toThrow(
      'configured empty export',
    );
  });

  it('rejects an export when a source image cannot be loaded', async () => {
    const schema = canvasSchemaFixture();
    const toBlob = vi.fn();
    vi.stubGlobal('document', {
      createElement: vi.fn(() => ({
        getContext: () => ({ fillRect: vi.fn(), fillStyle: '' }),
        toBlob,
      })),
    });
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })));

    await expect(exportCanvasToBlob([{
      ...textNode(), id: 'missing-image', type: 'image', imageUrl: '/missing.png',
    }], schema)).rejects.toThrow(schema.presentation.text['error.export_failed']);
    expect(toBlob).not.toHaveBeenCalled();
  });

  it('exports the actual Shape fill, stroke and width', async () => {
    const schema = canvasSchemaFixture();
    schema.frontend.export.default_background_color =
      schema.frontend.export.transparent_background_value;

    const operations: unknown[][] = [];
    const context = {
      fillStyle: '',
      strokeStyle: '',
      lineWidth: 0,
      globalAlpha: 1,
      save: vi.fn(),
      restore: vi.fn(),
      translate: vi.fn(),
      rotate: vi.fn(),
      fillRect: vi.fn(function (
        this: typeof context,
        ...values: number[]
      ) {
        operations.push(['fill', this.fillStyle, ...values]);
      }),
      strokeRect: vi.fn(function (
        this: typeof context,
        ...values: number[]
      ) {
        operations.push([
          'stroke',
          this.strokeStyle,
          this.lineWidth,
          ...values,
        ]);
      }),
    };
    const canvas = {
      width: 0,
      height: 0,
      getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => {
        callback(new Blob(['shape']));
      }),
    };
    vi.stubGlobal('document', {
      createElement: vi.fn(() => canvas),
    });

    await exportCanvasToBlob([
      {
        id: 'shape-a',
        type: 'shape',
        shapeType: 'rectangle',
        imageUrl: '',
        taskId: '',
        x: 0,
        y: 0,
        width: 20,
        height: 10,
        zIndex: 1,
        fillColor: '#445566',
        strokeColor: '#112233',
        strokeWidth: 5,
      },
    ], schema);

    expect(operations).toEqual([
      ['fill', '#445566', 0, 0, 20, 10],
      ['stroke', '#112233', 5, 0, 0, 20, 10],
    ]);
  });
});

describe('Canvas export rotation parity with Konva', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function rotateRecordingContext(operations: unknown[][]) {
    return {
      fillStyle: '',
      globalAlpha: 1,
      save: vi.fn(),
      restore: vi.fn(),
      translate: vi.fn(function (this: unknown, ...values: number[]) {
        operations.push(['translate', ...values]);
      }),
      rotate: vi.fn(function (this: unknown, ...values: number[]) {
        operations.push(['rotate', ...values]);
      }),
      fillRect: vi.fn(),
      fillText: vi.fn(), measureText,
    };
  }

  function recordingCanvas(context: unknown) {
    return {
      width: 0,
      height: 0,
      getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => {
        callback(new Blob(['rotated']));
      }),
    };
  }

  it('rotates around the node top-left origin like Konva and applies opacity', async () => {
    const schema = canvasSchemaFixture();
    schema.frontend.export.default_background_color =
      schema.frontend.export.transparent_background_value;
    const operations: unknown[][] = [];
    const context = rotateRecordingContext(operations);
    const canvas = recordingCanvas(context);
    vi.stubGlobal('document', {
      createElement: vi.fn(() => canvas),
    });

    await exportCanvasToBlob([
      {
        id: 'text-rotated',
        type: 'text',
        imageUrl: '',
        text: 'R',
        taskId: '',
        // 90° 绕左上角旋转：四角 (10,20)(10,50)(-2,20)(-2,50)，
        // 包围盒 minX=-2, minY=20, 宽 12, 高 30。
        x: 10,
        y: 20,
        width: 30,
        height: 12,
        rotation: 90,
        opacity: 0.5,
        zIndex: 1,
      },
    ], schema);

    const padding = schema.frontend.export.padding_pixels;
    expect(canvas.width).toBe(12 + padding * 2);
    expect(canvas.height).toBe(30 + padding * 2);
    // Konva 一致性：节点局部原点平移到画布坐标后旋转。
    expect(operations.slice(0, 2)).toEqual([
      ['translate', 10 - -2 + padding, 20 - 20 + padding],
      ['rotate', Math.PI / 2],
    ]);
    expect(context.globalAlpha).toBe(0.5);
  });
});

describe('Canvas export text parity with the board', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('draws a multi-line rotated text node with its own size inside one rotated frame of reference', async () => {
    const schema = canvasSchemaFixture();
    schema.frontend.export.default_background_color = schema.frontend.export.transparent_background_value;
    const operations: unknown[][] = [];
    const context = {
      fillStyle: '', font: '', textBaseline: '', globalAlpha: 1, measureText,
      save: vi.fn(), restore: vi.fn(), fillRect: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(),
      translate: vi.fn((...values: number[]) => operations.push(['translate', ...values])),
      rotate: vi.fn((...values: number[]) => operations.push(['rotate', ...values])),
      fillText: vi.fn((...values: unknown[]) => operations.push(['fillText', ...values])),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => ({
      width: 0, height: 0, getContext: () => context,
      toBlob: (callback: (blob: Blob | null) => void) => callback(new Blob(['text'])),
    })) });

    // The node's own size (48) and colour; two paragraphs; 30 degrees about its top-left corner.
    await exportCanvasToBlob([{
      ...textNode(), text: 'ab\ncd', width: 100, rotation: 30, fontSize: 48, strokeColor: '#123456',
    }], schema, { frame: { x: 0, y: 0, width: 200, height: 200 } });

    const lineHeight = 48 * schema.frontend.text.line_height;
    const baseline = (10 - 4) / 2 + lineHeight / 2;
    expect(operations).toEqual([
      ['translate', 10, 20],
      ['rotate', (30 * Math.PI) / 180],
      ['fillText', 'ab', 0, baseline],
      ['fillText', 'cd', 0, baseline + lineHeight],
    ]);
    expect(context.fillStyle).toBe('#123456');
    expect(context.font).toBe(`normal normal 48px ${schema.frontend.text.font_family}`);
  });

  it('does not take the colour from the interface theme: an unset colour is the Schema default', async () => {
    const schema = canvasSchemaFixture();
    const colours: string[] = [];
    const context = {
      fillStyle: '', font: '', textBaseline: '', globalAlpha: 1, measureText,
      save: vi.fn(), restore: vi.fn(), fillRect: vi.fn(), translate: vi.fn(), rotate: vi.fn(),
      fillText: vi.fn(function (this: { fillStyle: string }) { colours.push(this.fillStyle); }),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => ({
      width: 0, height: 0, getContext: () => context,
      toBlob: (callback: (blob: Blob | null) => void) => callback(new Blob(['text'])),
    })) });
    await exportCanvasToBlob([{ ...textNode(), text: 'a' }], schema);
    expect(colours).toEqual([schema.frontend.text.default_color]);
  });
});

describe('Canvas frame delivery', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('exports the dashed frame dimensions, clips rotated content and ignores off-frame assets', async () => {
    const schema = canvasSchemaFixture();
    const operations: unknown[][] = [];
    const context = {
      fillStyle: '',
      globalAlpha: 1,
      save: vi.fn(), restore: vi.fn(),
      beginPath: vi.fn(() => operations.push(['beginPath'])),
      rect: vi.fn((...values: number[]) => operations.push(['rect', ...values])),
      clip: vi.fn(() => operations.push(['clip'])),
      fillRect: vi.fn((...values: number[]) => operations.push(['fillRect', ...values])),
      translate: vi.fn((...values: number[]) => operations.push(['translate', ...values])),
      rotate: vi.fn((...values: number[]) => operations.push(['rotate', ...values])),
      fillText: vi.fn(), measureText,
    };
    const canvas = {
      width: 0, height: 0,
      getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => callback(new Blob(['frame']))),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);

    await exportCanvasToBlob([
      { ...textNode(), x: 110, y: 55, rotation: 90 },
      { ...textNode(), id: 'outside', type: 'image', imageUrl: '/outside.png', x: 400, y: 400 },
    ], schema, { frame: { x: 100, y: 50, width: 40, height: 20 } });

    expect(canvas.width).toBe(40);
    expect(canvas.height).toBe(20);
    expect(operations.slice(0, 6)).toEqual([
      ['fillRect', 0, 0, 40, 20],
      ['beginPath'], ['rect', 0, 0, 40, 20], ['clip'],
      ['translate', 10, 5], ['rotate', Math.PI / 2],
    ]);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('exports exactly the chosen pixel size, filling the frame', async () => {
    const schema = canvasSchemaFixture();
    const scales: number[][] = [];
    const context = {
      fillStyle: '', globalAlpha: 1, save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(),
      fillRect: vi.fn(), translate: vi.fn(), rotate: vi.fn(), fillText: vi.fn(), measureText,
      scale: vi.fn((...values: number[]) => scales.push(values)),
    };
    const canvas = {
      width: 0, height: 0,
      getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => callback(new Blob(['frame']))),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });

    // A 3:2 frame 512 board units wide, sent at 1536x1024.
    await exportCanvasToBlob([{ ...textNode(), x: 10, y: 10 }], schema, {
      frame: { x: 0, y: 0, width: 512, height: 512 / 1.5 }, size: { width: 1536, height: 1024 },
    });

    expect(canvas.width).toBe(1536);
    expect(canvas.height).toBe(1024);
    expect(scales[0][0]).toBe(3);
    expect(scales[0][1]).toBeCloseTo(3);
  });

  it('refuses an empty frame or invalid frame geometry', async () => {
    const schema = canvasSchemaFixture();
    await expect(exportCanvasToBlob([{ ...textNode(), type: 'image', imageUrl: '/unused.png' }], schema,
      { frame: { x: 100, y: 100, width: 20, height: 20 } }))
      .rejects.toThrow(schema.presentation.text['error.export_empty']);
    await expect(exportCanvasToBlob([textNode()], schema,
      { frame: { x: 0, y: 0, width: 0, height: 20 } }))
      .rejects.toThrow(schema.presentation.text['error.export_failed']);
  });

  it('does not discard a vector stroke that enters the frame from outside', async () => {
    const schema = canvasSchemaFixture();
    const strokeRect = vi.fn();
    const context = {
      fillStyle: '', strokeStyle: '', lineWidth: 0, globalAlpha: 1,
      save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(), rect: vi.fn(),
      clip: vi.fn(), fillRect: vi.fn(), strokeRect,
      translate: vi.fn(), rotate: vi.fn(),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => ({
      width: 0, height: 0, getContext: () => context,
      toBlob: (callback: (blob: Blob | null) => void) => callback(new Blob(['stroke'])),
    })) });
    await exportCanvasToBlob([{
      ...textNode(), type: 'shape', shapeType: 'rectangle', x: 0, y: 0,
      width: 10, height: 10, strokeWidth: 6,
    }], schema, { frame: { x: 12, y: 0, width: 20, height: 20 } });
    expect(strokeRect).toHaveBeenCalledWith(0, 0, 10, 10);
    expect(context.clip).toHaveBeenCalledOnce();
  });
});

import { artboardContent, exportArtboardToBlob } from './export-canvas';

describe('export board', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });
  const uploaded: CanvasNode = {
    id: 'up', type: 'image', imageUrl: '/api/v1/assets/u1', taskId: '', x: 100, y: 100, width: 300, height: 200, zIndex: 1,
  };
  const artboard = { x: 0, y: 0, width: 1024, height: 768 };

  it('an uploaded picture alone counts as content and exports at the chosen pixel size', async () => {
    expect(artboardContent([uploaded], artboard)).toHaveLength(1);
    const schema = canvasSchemaFixture();
    const context = {
      fillStyle: '', globalAlpha: 1, save: vi.fn(), restore: vi.fn(), translate: vi.fn(), rotate: vi.fn(),
      fillRect: vi.fn(), scale: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(), drawImage: vi.fn(),
    };
    const canvas = {
      width: 0, height: 0, getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => callback(new Blob(['png']))),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['i']) })));
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:x', revokeObjectURL: () => undefined }));
    vi.stubGlobal('Image', class { onload: () => void = () => {}; naturalWidth = 1; set src(_: string) { queueMicrotask(() => this.onload()); } });

    await exportArtboardToBlob([uploaded], schema, artboard, { width: 1536, height: 1152 });

    expect(canvas.width).toBe(1536);
    expect(canvas.height).toBe(1152);
    expect(context.scale).toHaveBeenCalledWith(1.5, 1.5);
    expect(context.drawImage).toHaveBeenCalledTimes(1);
  });

  it('decodes the layers off the main thread into bitmaps when the browser can, and releases them after drawing (#290)', async () => {
    const schema = canvasSchemaFixture();
    const context = {
      fillStyle: '', globalAlpha: 1, save: vi.fn(), restore: vi.fn(), translate: vi.fn(), rotate: vi.fn(),
      fillRect: vi.fn(), scale: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(), drawImage: vi.fn(),
    };
    const canvas = {
      width: 0, height: 0, getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void) => callback(new Blob(['png']))),
    };
    const bytes = new Blob(['i']);
    const bitmap = { width: 300, height: 200, close: vi.fn() };
    const decode = vi.fn(async () => bitmap);
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => bytes })));
    vi.stubGlobal('createImageBitmap', decode);
    vi.stubGlobal('Image', class { constructor() { throw new Error('no <img> decode on this path'); } });

    await exportArtboardToBlob([uploaded], schema, artboard, { width: 1024, height: 768 });

    expect(decode).toHaveBeenCalledWith(bytes);
    expect(context.drawImage).toHaveBeenCalledWith(bitmap, 0, 0, uploaded.width, uploaded.height);
    expect(bitmap.close).toHaveBeenCalledTimes(1);
  });

  it('reports a picture the browser cannot decode as a decode failure (#290)', async () => {
    const schema = canvasSchemaFixture();
    const canvas = { width: 0, height: 0, getContext: vi.fn(() => ({ scale: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn() })), toBlob: vi.fn() };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['broken'], { type: 'image/png' }) })));
    vi.stubGlobal('createImageBitmap', vi.fn(async () => { throw new Error('InvalidStateError'); }));

    await expect(exportArtboardToBlob([uploaded], schema, artboard, { width: 1024, height: 768 })).rejects.toMatchObject({ step: 'decode' });
    expect(canvas.toBlob).not.toHaveBeenCalled();
  });

  // #271: the board leaves like a Photoshop layer. Empty canvas is not painted, so it stays alpha 0 in the PNG.
  function stubArtboardCanvas() {
    const fills: string[] = [];
    const context = {
      fillStyle: '', globalAlpha: 1, save: vi.fn(), restore: vi.fn(), translate: vi.fn(), rotate: vi.fn(),
      fillRect: vi.fn(function (this: { fillStyle: string }) { fills.push(this.fillStyle); }),
      scale: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(), drawImage: vi.fn(),
    };
    const canvas = {
      width: 0, height: 0, getContext: vi.fn(() => context),
      toBlob: vi.fn((callback: (blob: Blob | null) => void, _type?: string) => callback(new Blob(['png']))),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['i']) })));
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:x', revokeObjectURL: () => undefined }));
    vi.stubGlobal('Image', class { onload: () => void = () => {}; naturalWidth = 1; set src(_: string) { queueMicrotask(() => this.onload()); } });
    return { canvas, context, fills };
  }

  it('keeps empty canvas transparent: the Schema artboard background paints nothing under the layers', async () => {
    const schema = canvasSchemaFixture();
    expect(schema.frontend.export.artboard_background_color).toBe(schema.frontend.export.transparent_background_value);
    // The display/default background is not content and is not baked in.
    expect(schema.frontend.export.default_background_color).not.toBe(schema.frontend.export.transparent_background_value);
    const { canvas, context, fills } = stubArtboardCanvas();

    await exportArtboardToBlob([uploaded], schema, artboard, { width: 1024, height: 768 });

    expect(fills).toEqual([]);
    expect(context.drawImage).toHaveBeenCalledTimes(1);
    expect(canvas.toBlob).toHaveBeenCalledWith(expect.any(Function), 'image/png');
  });

  it('paints the Schema artboard colour first when one is declared instead of transparency', async () => {
    const schema = canvasSchemaFixture();
    schema.frontend.export.artboard_background_color = '#123456';
    const { context, fills } = stubArtboardCanvas();

    await exportArtboardToBlob([uploaded], schema, artboard, null);

    expect(fills).toEqual(['#123456']);
    expect(context.fillRect).toHaveBeenCalledWith(0, 0, 1024, 768);
    expect(context.fillRect.mock.invocationCallOrder[0]).toBeLessThan(context.drawImage.mock.invocationCallOrder[0]);
  });

  it('the export button state follows visible content inside the artboard', () => {
    expect(artboardContent([], artboard)).toHaveLength(0);
    expect(artboardContent([{ ...uploaded, visible: false }], artboard)).toHaveLength(0);
    expect(artboardContent([{ ...uploaded, x: 5000 }], artboard)).toHaveLength(0);
    expect(artboardContent([{ ...uploaded, type: 'frame' }], artboard)).toHaveLength(0);
  });
});

describe('Canvas export failure steps (#286)', () => {
  afterEach(() => vi.unstubAllGlobals());

  const frame = { x: 0, y: 0, width: 40, height: 20 };
  const picture = (): CanvasNode => ({
    id: 'picture', type: 'image', imageUrl: '/api/v1/tasks/task_1/assets/asset_1?sig=secret', taskId: 'task_1',
    x: 0, y: 0, width: 40, height: 20, zIndex: 1,
  });

  /** A board whose canvas, pictures and encoder behave as each test says. */
  function stubBrowser(options: {
    fetch?: () => Promise<Response>;
    decodes?: boolean;
    drawImage?: () => void;
    toBlob?: (callback: (blob: Blob | null) => void) => void;
  }) {
    const context = {
      fillStyle: '', globalAlpha: 1, save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(),
      fillRect: vi.fn(), translate: vi.fn(), rotate: vi.fn(), scale: vi.fn(), drawImage: vi.fn(options.drawImage ?? (() => undefined)),
    };
    const canvas = {
      width: 0, height: 0, getContext: vi.fn(() => context),
      toBlob: vi.fn(options.toBlob ?? ((callback: (blob: Blob | null) => void) => callback(new Blob(['png'])))),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });
    vi.stubGlobal('fetch', vi.fn(options.fetch ?? (async () => new Response(new Blob(['x'.repeat(12)], { type: 'image/png' })))));
    const revoked: string[] = [];
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:local/picture');
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation((url: string) => { revoked.push(url); });
    vi.stubGlobal('Image', class {
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      set src(_value: string) {
        queueMicrotask(() => (options.decodes === false ? this.onerror?.() : this.onload?.()));
      }
    });
    return { canvas, revoked };
  }

  async function failure(run: Promise<unknown>): Promise<CanvasExportError> {
    const error = await run.then(() => null, (cause: unknown) => cause);
    expect(error).toBeInstanceOf(CanvasExportError);
    return error as CanvasExportError;
  }

  afterEach(() => vi.restoreAllMocks());

  it('says a picture could not be fetched, with its status and path but not its query', async () => {
    const schema = canvasSchemaFixture();
    stubBrowser({ fetch: async () => new Response('', { status: 404 }) });
    const error = await failure(exportCanvasToBlob([picture()], schema, { frame }));
    expect(error.message).toBe(schema.presentation.text['error.export_failed']);
    expect(error.step).toBe('fetch');
    expect(error.detail).toEqual({ asset: '/api/v1/tasks/task_1/assets/asset_1', status: 404 });
  });

  it('says the body of a 200 response did not arrive', async () => {
    const schema = canvasSchemaFixture();
    const response = new Response('partial');
    vi.spyOn(response, 'blob').mockRejectedValue(new TypeError('network error'));
    stubBrowser({ fetch: async () => response });
    const error = await failure(exportCanvasToBlob([picture()], schema, { frame }));
    expect(error.step).toBe('fetch');
    expect(error.detail).toMatchObject({ status: 200, reason: 'TypeError: network error' });
  });

  it('says a fetched picture could not be decoded, with its bytes and type', async () => {
    const schema = canvasSchemaFixture();
    const { canvas, revoked } = stubBrowser({ decodes: false });
    const error = await failure(exportCanvasToBlob([picture()], schema, { frame }));
    expect(error.step).toBe('decode');
    expect(error.detail).toEqual({ asset: '/api/v1/tasks/task_1/assets/asset_1', bytes: 12, type: 'image/png' });
    expect(canvas.toBlob).not.toHaveBeenCalled();
    expect(revoked).toEqual(['blob:local/picture']);
  });

  it('says drawing failed when the browser refuses to draw a decoded picture', async () => {
    const schema = canvasSchemaFixture();
    const { revoked } = stubBrowser({ drawImage: () => { throw new DOMException('broken image', 'InvalidStateError'); } });
    const error = await failure(exportCanvasToBlob([picture()], schema, { frame }));
    expect(error.step).toBe('draw');
    expect(error.detail).toMatchObject({ width: 40, height: 20, images: 1, sourceBytes: 12, reason: 'InvalidStateError: broken image' });
    expect(revoked).toEqual(['blob:local/picture']);
  });

  it('says encoding failed when the browser hands back no picture, with the output size', async () => {
    const schema = canvasSchemaFixture();
    const { revoked } = stubBrowser({ toBlob: (callback) => callback(null) });
    const error = await failure(exportCanvasToBlob([picture()], schema, { frame, size: { width: 3840, height: 2160 } }));
    expect(error.step).toBe('encode');
    expect(error.detail).toEqual({ width: 3840, height: 2160, images: 1, sourceBytes: 12, reason: 'toBlob returned null' });
    expect(revoked).toEqual(['blob:local/picture']);
  });

  it('says encoding failed when toBlob throws', async () => {
    const schema = canvasSchemaFixture();
    stubBrowser({ toBlob: () => { throw new DOMException('tainted', 'SecurityError'); } });
    const error = await failure(exportCanvasToBlob([picture()], schema, { frame }));
    expect(error.step).toBe('encode');
    expect(error.detail.reason).toBe('SecurityError: tainted');
  });

  it('says a picture layer has no source before reading anything', async () => {
    const schema = canvasSchemaFixture();
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    const error = await failure(exportCanvasToBlob([{ ...picture(), imageUrl: '' }], schema, { frame }));
    expect(error.step).toBe('source');
    expect(fetcher).not.toHaveBeenCalled();
  });
});
