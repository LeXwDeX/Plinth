import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import {
  compliantExportDimensions,
  exportImageElementToBlob,
  triggerBlobDownload,
} from './compliant-export';

function canvasSchemaFixture(): CanvasProductSchema {
  return structuredClone(
    defaultCatalog.application.canvas,
  ) as unknown as CanvasProductSchema;
}

describe('compliantExportDimensions', () => {
  it('keeps dimensions untouched when within the schema maximum', () => {
    expect(compliantExportDimensions(800, 600, 4096)).toEqual({
      width: 800,
      height: 600,
      scale: 1,
    });
  });

  it('scales the longest edge down to the schema maximum, preserving ratio', () => {
    const result = compliantExportDimensions(8000, 2000, 4000);
    expect(result).toEqual({ width: 4000, height: 1000, scale: 0.5 });
  });

  it('clamps portrait images by height and never upscales', () => {
    expect(compliantExportDimensions(100, 9000, 3000)).toEqual({
      width: 33,
      height: 3000,
      scale: 1 / 3,
    });
    expect(compliantExportDimensions(10, 10, 3000).scale).toBe(1);
  });

  it('rejects invalid inputs instead of exporting garbage', () => {
    expect(() => compliantExportDimensions(0, 100, 4096)).toThrow();
    expect(() => compliantExportDimensions(100, -1, 4096)).toThrow();
    expect(() => compliantExportDimensions(100, 100, 0)).toThrow();
    expect(() => compliantExportDimensions(Number.NaN, 100, 4096)).toThrow();
  });
});

describe('exportImageElementToBlob', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('draws the clamped dimensions and encodes with the schema media type', async () => {
    const schema = canvasSchemaFixture();
    schema.limits.maximum_dimension = 400;
    schema.frontend.export.output_media_type = 'image/configured';
    const drawCalls: unknown[][] = [];
    const context = {
      drawImage: vi.fn((...values: unknown[]) => {
        drawCalls.push(values);
      }),
    };
    let encodedMediaType = '';
    const canvas = {
      width: 0,
      height: 0,
      getContext: vi.fn(() => context),
      toBlob: vi.fn(
        (callback: (blob: Blob | null) => void, mediaType: string) => {
          encodedMediaType = mediaType;
          callback(new Blob(['export']));
        },
      ),
    };
    vi.stubGlobal('document', { createElement: vi.fn(() => canvas) });

    const image = { naturalWidth: 1600, naturalHeight: 800 } as unknown as {
      naturalWidth: number;
      naturalHeight: number;
    } & CanvasImageSource;
    const blob = await exportImageElementToBlob(image, {
      maximumDimension: schema.limits.maximum_dimension,
      outputMediaType: schema.frontend.export.output_media_type,
    });

    expect(blob).toBeInstanceOf(Blob);
    expect(canvas.width).toBe(400);
    expect(canvas.height).toBe(200);
    expect(encodedMediaType).toBe('image/configured');
    expect(drawCalls).toEqual([[image, 0, 0, 400, 200]]);
  });

  it('rejects degenerate images and failed encodings', async () => {
    const image = { naturalWidth: 0, naturalHeight: 0 } as unknown as {
      naturalWidth: number;
      naturalHeight: number;
    } & CanvasImageSource;
    await expect(
      exportImageElementToBlob(image, {
        maximumDimension: 4096,
        outputMediaType: 'image/png',
      }),
    ).rejects.toThrow();
  });
});

describe('triggerBlobDownload', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('clicks a real object URL with the schema filename and revokes it', () => {
    const created: string[] = [];
    const revoked: string[] = [];
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn(() => {
        const url = `blob:mock-${created.length}`;
        created.push(url);
        return url;
      }),
      revokeObjectURL: vi.fn((url: string) => {
        revoked.push(url);
      }),
    });
    const clicked: Array<{ href: string; download: string }> = [];
    vi.stubGlobal('document', {
      createElement: vi.fn(() => {
        const anchor = { href: '', download: '', click: vi.fn() };
        clicked.push(anchor);
        return anchor;
      }),
    });

    triggerBlobDownload(new Blob(['x']), 'workspace-export.png');

    expect(clicked[0].href).toBe('blob:mock-0');
    expect(clicked[0].download).toBe('workspace-export.png');
    expect(revoked).toEqual(['blob:mock-0']);
  });
});
