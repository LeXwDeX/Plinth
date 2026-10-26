/**
 * 合规尺寸图片导出（AGENTS 工作区 2D 画布轻量工具）。
 *
 * 导出尺寸受 Product Schema canvas.limits.maximum_dimension 约束：
 * 最长边超限即等比缩小，绝不放大；编码媒体类型与文件名来自
 * canvas.frontend.export 配置，不在代码中写死产品参数。
 */

export interface CompliantExportSize {
  width: number;
  height: number;
  scale: number;
}

export interface CompliantExportConfig {
  maximumDimension: number;
  outputMediaType: string;
}

export type NaturalSizedImageSource = CanvasImageSource & {
  naturalWidth: number;
  naturalHeight: number;
};

/** 等比钳制到 Schema 最大边长；非法输入直接抛错（由调用方映射文案）。 */
export function compliantExportDimensions(
  width: number,
  height: number,
  maximumDimension: number,
): CompliantExportSize {
  if (
    !Number.isFinite(width) ||
    !Number.isFinite(height) ||
    !Number.isFinite(maximumDimension) ||
    width <= 0 ||
    height <= 0 ||
    maximumDimension <= 0
  ) {
    throw new Error('invalid export dimensions');
  }
  const longest = Math.max(width, height);
  const scale = longest > maximumDimension ? maximumDimension / longest : 1;
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
    scale,
  };
}

/** 把已解码图片按合规尺寸绘制并编码为 Blob（媒体类型来自 Schema）。 */
export async function exportImageElementToBlob(
  image: NaturalSizedImageSource,
  config: CompliantExportConfig,
): Promise<Blob> {
  const dimensions = compliantExportDimensions(
    image.naturalWidth,
    image.naturalHeight,
    config.maximumDimension,
  );
  const canvas = document.createElement('canvas');
  canvas.width = dimensions.width;
  canvas.height = dimensions.height;
  const context = canvas.getContext('2d');
  if (!context) {
    throw new Error('canvas 2d context unavailable');
  }
  context.drawImage(image, 0, 0, dimensions.width, dimensions.height);
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error('canvas encoding failed'));
    }, config.outputMediaType);
  });
}

/** 用真实 object URL 触发浏览器下载并即时释放（绝不使用 '#' 链接）。 */
export function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}
