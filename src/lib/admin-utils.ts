'use client';

/**
 * 管理后台共享工具函数
 *
 * generations-panel 和 user-assets 共用的 view-model 逻辑统一收口于此，
 * 不得在各 admin 组件中重复定义。
 *
 * 架构层级：UI 层共享工具（客户端）
 */

// ── 缩略图 URL 构造 ──
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { formatFileSize } from '@/lib/size-text';

/**
 * 构造前端可用的缩略图 src
 * - 本地路径（/api/gpt-images/xxx）→ 直接使用
 * - 旧远程 URL 不可在浏览器中访问
 * - resultUrls 回退：取第一个图片 URL
 */
export function getThumbnailSrc(
  thumbnailUrl: string | null,
  resultUrlsRaw?: string,
  _taskId?: string,
): string | null {
  if (thumbnailUrl && safeAssetURL(thumbnailUrl)) return safeAssetURL(thumbnailUrl);
  // 回退：从 resultUrls 取第一个图片 URL
  if (resultUrlsRaw) {
    try {
      const urls = JSON.parse(resultUrlsRaw) as string[];
      const first = urls.find((url) => safeAssetURL(url));
      if (!first) return null;
      return safeAssetURL(first) || null;
    } catch {
      // ignore
    }
  }
  return null;
}

/**
 * 构造下载/访问记录的缩略图 src（基于 resourceType + resourceId）
 */
export function getDownloadThumbnailSrc(resourceType: string, resourceId: string): string | null {
  if (!resourceId) return null;
  if (resourceType === 'gpt-image') {
    return safeAssetURL(`/api/gpt-images/${encodeURIComponent(resourceId)}`) || null;
  }
  return safeAssetURL(resourceId) || null;
}

// ── 参数解析 ──

/** 从 JSON 字符串中提取 ai_model 字段 */
export function extractAiModel(paramsStr: string): string {
  try {
    const parsed = JSON.parse(paramsStr);
    return parsed.ai_model || '—';
  } catch {
    return '—';
  }
}

/** 将 JSON 字符串格式化为可读的 tooltip 文本 */
export function formatParamsTooltip(paramsStr: string): string {
  try {
    return JSON.stringify(JSON.parse(paramsStr), null, 2);
  } catch {
    return paramsStr;
  }
}

/** 安全解析 resultUrls JSON 字符串 */
export function parseResultUrls(raw: string): string[] {
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

// ── 格式化工具 ──

/** 格式化字节数为可读字符串：与服务端和 Agent 同一种写法（十进制 MB，#327）。 */
export function formatBytes(bytes: number): string {
  return formatFileSize(bytes);
}


// ── 下载编排（客户端） ──

/**
 * 触发管理后台任务资产下载。
 * 处理二进制文件（直接下载）和 JSON 响应（redirect / error）两种情况。
 *
 * @throws Error 当请求失败或服务端返回错误时
 */
export interface AdminTaskAssetDownloadConfig {
  url: string;
  errorField: string;
  redirectField: string;
  failedMessage: string;
  unknownResponseMessage: string;
  fallbackFilename: string;
}

export async function downloadAdminTaskAsset(
  config: AdminTaskAssetDownloadConfig,
): Promise<void> {
  const requestURL = safeAssetURL(config.url);
  if (!requestURL) throw new Error(config.failedMessage);
  const res = await fetch(requestURL);
  if (!res.ok) {
    const payload = await res.json().catch(() => ({})) as Record<
      string,
      unknown
    >;
    const message = payload[config.errorField];
    throw new Error(
      typeof message === 'string' && message
        ? message
        : config.failedMessage,
    );
  }

  const contentType = res.headers.get('content-type') || '';
  if (contentType.includes('application/json')) {
    const payload = await res.json() as Record<string, unknown>;
    const redirect = payload[config.redirectField];
    if (typeof redirect === 'string' && redirect) {
      const destination = safeAssetURL(redirect);
      if (!destination) throw new Error(config.failedMessage);
      window.open(destination, '_blank', 'noopener,noreferrer');
      return;
    }
    const message = payload[config.errorField];
    throw new Error(
      typeof message === 'string' && message
        ? message
        : config.unknownResponseMessage,
    );
  }

  // 二进制文件 → 触发浏览器下载
  const blob = await res.blob();
  const disposition = res.headers.get('content-disposition') || '';
  const filenameMatch = disposition.match(/filename\*?=(?:UTF-8'')?(.+)/i);
  const filename = filenameMatch
    ? decodeURIComponent(filenameMatch[1])
    : config.fallbackFilename;
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(a.href);
}
